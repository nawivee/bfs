# bfs

Resumable BFS over a graph with a known number of vertices, a prioritized
frontier, per-vertex min/max depth tracking, and a live stats HTTP endpoint.

- **Visit callback** — you supply `VisitFunc(ctx, vertex, depth) ([]int, error)`;
  it returns the neighbours to enqueue. Visits may be slow.
- **Queue** — priority queue ordered by min depth, then min vertex. A vertex is
  enqueued at most once (bitmap-backed dedup) and visited exactly once.
- **Depths** — `Depth(v)` returns the min and max depth `v` was ever reached at,
  counting every discovery, not just the one that enqueued it.
- **Stats** — `StatsHandler()` serves JSON: start time, elapsed, visited counter,
  progress, queue size, queue min/max element, overall and last-10-minutes
  rates, and ETAs from both (ETA assumes all vertices are reachable).
- **Persistence** — `Save(path)` (atomic, safe during a run) and
  `Load(path, visit)`. On ctx cancellation the in-flight vertex goes back into
  the queue, so nothing is lost. Start time and elapsed survive restarts.

## Example app

Each graph element is `struct{ A, B, Vertex int }`; visiting sleeps 1s and
returns `A` and `B`.

```bash
go run ./cmd/example -n 500 -addr :8080 -state bfs.state
curl localhost:8080/stats        # progress, rates, ETA
# Ctrl-C saves progress; rerun the same command to resume.
```

Flags: `-n` graph size, `-state` snapshot file, `-addr` stats address,
`-save-every` autosave interval, `-seed-vertex` starting vertex.

## Library usage

```go
e := bfs.New(nodeCount, visit) // or bfs.Load(path, visit) to resume
e.Seed(0, 42)                  // initial queue elements at depth 0
http.Handle("/stats", e.StatsHandler())
err := e.Run(ctx)              // until queue empty or ctx cancelled
e.Save(path)
min, max, ok := e.Depth(v)
```

