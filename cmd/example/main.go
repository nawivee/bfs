// Example app: runs a resumable BFS over a randomly generated graph where
// each vertex has two outgoing edges (A and B). Visiting a vertex takes 1s.
//
//	go run ./cmd/example -n 500 -addr :8080 -state bfs.state
//
// Watch progress:   curl localhost:8080/stats
// Stop (saves):     Ctrl-C (or kill -TERM)
// Resume:           run the same command again — it picks up bfs.state.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nawivee/bfs"
)

// Node is one element of the graph array: vertex number and its two neighbours.
type Node struct {
	A      int
	B      int
	Vertex int
}

func main() {
	n := flag.Int("n", 500, "number of vertices in the graph")
	statePath := flag.String("state", "bfs.state", "file to save/resume progress")
	addr := flag.String("addr", ":8080", "address for the stats endpoint")
	saveEvery := flag.Duration("save-every", 30*time.Second, "autosave interval (0 to disable)")
	seedVertex := flag.Int("seed-vertex", 0, "initial vertex to start the BFS from")
	flag.Parse()

	// The graph must be identical across restarts, so it is generated with a
	// fixed seed. A real app would load it from a file instead.
	graph := makeGraph(*n)

	visit := func(ctx context.Context, vertex, depth int) ([]int, error) {
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		nd := graph[vertex]
		return []int{nd.A, nd.B}, nil
	}

	var engine *bfs.Engine
	if _, err := os.Stat(*statePath); err == nil {
		engine, err = bfs.Load(*statePath, visit)
		if err != nil {
			log.Fatalf("resume: %v", err)
		}
		st := engine.Stats()
		log.Printf("resumed from %s: %d/%d visited, queue %d",
			*statePath, st.Visited, st.TotalNodes, st.QueueSize)
	} else {
		engine = bfs.New(*n, visit)
		engine.Seed(*seedVertex)
		log.Printf("new run: %d vertices, starting from %d", *n, *seedVertex)
	}

	http.Handle("/stats", engine.StatsHandler())
	go func() {
		log.Printf("stats endpoint on http://localhost%s/stats", *addr)
		if err := http.ListenAndServe(*addr, nil); err != nil {
			log.Printf("stats server: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *saveEvery > 0 {
		go func() {
			t := time.NewTicker(*saveEvery)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					if err := engine.Save(*statePath); err != nil {
						log.Printf("autosave: %v", err)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	err := engine.Run(ctx)

	if saveErr := engine.Save(*statePath); saveErr != nil {
		log.Printf("save: %v", saveErr)
	} else {
		log.Printf("progress saved to %s", *statePath)
	}

	st := engine.Stats()
	switch {
	case err == nil:
		log.Printf("finished: visited %d/%d vertices in %.0fs",
			st.Visited, st.TotalNodes, st.ElapsedSeconds)
		printDepths(engine, min(10, *n))
	case errors.Is(err, context.Canceled):
		log.Printf("interrupted: %d/%d visited, queue %d — rerun to resume",
			st.Visited, st.TotalNodes, st.QueueSize)
	default:
		log.Fatalf("bfs: %v", err)
	}
}

func makeGraph(n int) []Node {
	rng := rand.New(rand.NewSource(42))
	graph := make([]Node, n)
	for i := range graph {
		graph[i] = Node{Vertex: i, A: rng.Intn(n), B: rng.Intn(n)}
	}
	return graph
}

func printDepths(e *bfs.Engine, count int) {
	for v := 0; v < count; v++ {
		if min, max, ok := e.Depth(v); ok {
			fmt.Printf("vertex %d: min depth %d, max depth %d\n", v, min, max)
		} else {
			fmt.Printf("vertex %d: unreachable\n", v)
		}
	}
}
