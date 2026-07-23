package bfs

import (
	"container/heap"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// persistedState is the on-disk snapshot of an Engine (gob encoded).
type persistedState struct {
	N            int
	Enqueued     []uint64
	Visited      []uint64
	MinDepth     []int32
	MaxDepth     []int32
	Queue        []Item
	VisitedCount int64
	FirstStart   time.Time
	TotalElapsed time.Duration
}

// Save writes the engine state to path atomically (temp file + rename). It is
// safe to call while Run is in progress; an in-flight visit that has not
// completed yet is simply not part of the snapshot and will be redone.
func (e *Engine) Save(path string) error {
	e.mu.Lock()
	st := persistedState{
		N:            e.n,
		Enqueued:     append([]uint64(nil), e.enqueued...),
		Visited:      append([]uint64(nil), e.visited...),
		MinDepth:     append([]int32(nil), e.minDepth...),
		MaxDepth:     append([]int32(nil), e.maxDepth...),
		Queue:        append([]Item(nil), e.queue...),
		VisitedCount: e.visitedCount,
		FirstStart:   e.firstStart,
		TotalElapsed: e.elapsedLocked(),
	}
	e.mu.Unlock()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".bfs-state-*")
	if err != nil {
		return fmt.Errorf("bfs: save: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := gob.NewEncoder(tmp).Encode(&st); err != nil {
		tmp.Close()
		return fmt.Errorf("bfs: save: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("bfs: save: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("bfs: save: %w", err)
	}
	return nil
}

// Load restores an engine previously written with Save. The visit function is
// not persisted and must be supplied again; the graph it walks must be the
// same one the saved run used.
func Load(path string, visit VisitFunc) (*Engine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bfs: load: %w", err)
	}
	defer f.Close()

	var st persistedState
	if err := gob.NewDecoder(f).Decode(&st); err != nil {
		return nil, fmt.Errorf("bfs: load: decode %s: %w", path, err)
	}
	if st.N < 0 || len(st.MinDepth) != st.N || len(st.MaxDepth) != st.N ||
		len(st.Enqueued) != (st.N+63)/64 || len(st.Visited) != (st.N+63)/64 {
		return nil, fmt.Errorf("bfs: load: %s is corrupt or from a different graph size", path)
	}

	e := &Engine{
		n:            st.N,
		visit:        visit,
		enqueued:     st.Enqueued,
		visited:      st.Visited,
		minDepth:     st.MinDepth,
		maxDepth:     st.MaxDepth,
		queue:        itemHeap(st.Queue),
		visitedCount: st.VisitedCount,
		firstStart:   st.FirstStart,
		prevElapsed:  st.TotalElapsed,
	}
	heap.Init(&e.queue)
	return e, nil
}
