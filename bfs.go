// Package bfs implements a resumable breadth-first search over a graph with
// a fixed, known number of vertices.
//
// Vertices are visited by a user-supplied VisitFunc which returns the
// neighbours to explore next. The frontier is a priority queue ordered by
// (depth asc, vertex asc). Every vertex is enqueued and visited at most once,
// but the minimum and maximum depth at which each vertex was ever reached is
// tracked across all discoveries. State can be saved to disk and resumed.
package bfs

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"time"
)

// Item is one queue entry: a vertex and the depth it will be visited at.
type Item struct {
	Vertex int
	Depth  int
}

// VisitFunc visits a vertex and returns the neighbouring vertices to enqueue.
// It may block for a long time; it should honour ctx cancellation and return
// ctx.Err() in that case. When an error is returned the vertex is NOT marked
// visited — it is pushed back onto the queue so a later resume retries it.
type VisitFunc func(ctx context.Context, vertex, depth int) (next []int, err error)

type sample struct {
	T     time.Time
	Count int64
}

// Engine runs the BFS. Create one with New or Load. All methods are safe for
// concurrent use (e.g. the stats endpoint reads while Run is working).
type Engine struct {
	mu    sync.Mutex
	n     int
	visit VisitFunc

	queue    itemHeap
	enqueued []uint64 // bit set once a vertex has ever been queued (never cleared)
	visited  []uint64
	minDepth []int32 // -1 = never reached
	maxDepth []int32

	visitedCount int64

	firstStart   time.Time     // when the very first Run started (survives resume)
	sessionStart time.Time     // when the current Run started; zero if not running
	prevElapsed  time.Duration // running time accumulated in previous sessions

	samples []sample // (time, visitedCount) points within the recent-rate window
}

// recentWindow is the sliding window used for the "recent speed" estimate.
const recentWindow = 10 * time.Minute

// New creates an engine for a graph with n vertices (numbered 0..n-1).
func New(n int, visit VisitFunc) *Engine {
	e := &Engine{
		n:        n,
		visit:    visit,
		enqueued: make([]uint64, (n+63)/64),
		visited:  make([]uint64, (n+63)/64),
		minDepth: make([]int32, n),
		maxDepth: make([]int32, n),
	}
	for i := range e.minDepth {
		e.minDepth[i] = -1
		e.maxDepth[i] = -1
	}
	return e
}

// Seed puts the initial vertices into the queue at depth 0.
func (e *Engine) Seed(vertices ...int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, v := range vertices {
		e.offerLocked(v, 0)
	}
}

// offerLocked records that v was reached at depth and enqueues it unless it
// is already queued or visited. Out-of-range vertices are ignored.
func (e *Engine) offerLocked(v, depth int) {
	if v < 0 || v >= e.n {
		return
	}
	d := int32(depth)
	if e.minDepth[v] == -1 || d < e.minDepth[v] {
		e.minDepth[v] = d
	}
	if d > e.maxDepth[v] {
		e.maxDepth[v] = d
	}
	if bitGet(e.enqueued, v) {
		return
	}
	bitSet(e.enqueued, v)
	heap.Push(&e.queue, Item{Vertex: v, Depth: depth})
}

// Run processes the queue until it is empty or ctx is cancelled. On
// cancellation the in-flight vertex is returned to the queue and ctx.Err() is
// returned, so Save/resume loses no work. Run may be called again (after
// Load) to continue.
func (e *Engine) Run(ctx context.Context) error {
	now := time.Now()
	e.mu.Lock()
	if e.firstStart.IsZero() {
		e.firstStart = now
	}
	e.sessionStart = now
	e.mu.Unlock()
	defer e.stopClock()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.mu.Lock()
		if e.queue.Len() == 0 {
			e.mu.Unlock()
			return nil
		}
		it := heap.Pop(&e.queue).(Item)
		e.mu.Unlock()

		next, err := e.visit(ctx, it.Vertex, it.Depth)

		e.mu.Lock()
		if err != nil {
			heap.Push(&e.queue, it)
			e.mu.Unlock()
			return err
		}
		bitSet(e.visited, it.Vertex)
		e.visitedCount++
		e.recordSampleLocked(time.Now())
		for _, nb := range next {
			e.offerLocked(nb, it.Depth+1)
		}
		e.mu.Unlock()
	}
}

// stopClock folds the current session's running time into prevElapsed.
func (e *Engine) stopClock() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.sessionStart.IsZero() {
		e.prevElapsed += time.Since(e.sessionStart)
		e.sessionStart = time.Time{}
	}
}

func (e *Engine) elapsedLocked() time.Duration {
	d := e.prevElapsed
	if !e.sessionStart.IsZero() {
		d += time.Since(e.sessionStart)
	}
	return d
}

func (e *Engine) recordSampleLocked(t time.Time) {
	e.samples = append(e.samples, sample{T: t, Count: e.visitedCount})
	e.pruneSamplesLocked(t)
}

func (e *Engine) pruneSamplesLocked(now time.Time) {
	cut := 0
	for cut < len(e.samples) && now.Sub(e.samples[cut].T) > recentWindow {
		cut++
	}
	if cut > 0 {
		e.samples = append(e.samples[:0], e.samples[cut:]...)
	}
}

// Depth reports the minimum and maximum depth at which vertex v was reached.
// reached is false if v was never discovered.
func (e *Engine) Depth(v int) (min, max int, reached bool) {
	if v < 0 || v >= e.n {
		return 0, 0, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.minDepth[v] == -1 {
		return 0, 0, false
	}
	return int(e.minDepth[v]), int(e.maxDepth[v]), true
}

// Visited reports whether vertex v has been visited.
func (e *Engine) Visited(v int) bool {
	if v < 0 || v >= e.n {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return bitGet(e.visited, v)
}

// N returns the number of vertices the engine was created for.
func (e *Engine) N() int { return e.n }

func (e *Engine) String() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fmt.Sprintf("bfs.Engine{n=%d visited=%d queued=%d}", e.n, e.visitedCount, e.queue.Len())
}

// ---- bitmap helpers ----

func bitSet(b []uint64, i int) { b[i>>6] |= 1 << (uint(i) & 63) }
func bitGet(b []uint64, i int) bool {
	return b[i>>6]&(1<<(uint(i)&63)) != 0
}

// ---- priority queue: min depth first, then min vertex ----

type itemHeap []Item

func (h itemHeap) Len() int { return len(h) }
func (h itemHeap) Less(i, j int) bool {
	if h[i].Depth != h[j].Depth {
		return h[i].Depth < h[j].Depth
	}
	return h[i].Vertex < h[j].Vertex
}
func (h itemHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *itemHeap) Push(x any)   { *h = append(*h, x.(Item)) }
func (h *itemHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
