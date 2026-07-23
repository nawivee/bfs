package bfs

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// graph: 0 -> {1, 2}, 1 -> {3, 0}, 2 -> {3, 3}, 3 -> {3, 3}
var testEdges = [][2]int{0: {1, 2}, 1: {3, 0}, 2: {3, 3}, 3: {3, 3}}

func testVisit(order *[]Item) VisitFunc {
	return func(_ context.Context, v, d int) ([]int, error) {
		*order = append(*order, Item{Vertex: v, Depth: d})
		return testEdges[v][:], nil
	}
}

func TestOrderDedupAndDepths(t *testing.T) {
	var order []Item
	e := New(4, testVisit(&order))
	e.Seed(0)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []Item{{0, 0}, {1, 1}, {2, 1}, {3, 2}}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("visit order = %v, want %v", order, want)
	}
	if got := e.Stats().Visited; got != 4 {
		t.Errorf("visited = %d, want 4 (each node exactly once)", got)
	}

	// 0 is reached at depth 0 (seed) and again at depth 2 (from 1).
	// 3 is reached at depth 2 (from 1 and 2) and depth 3 (from itself).
	checks := []struct{ v, min, max int }{{0, 0, 2}, {1, 1, 1}, {2, 1, 1}, {3, 2, 3}}
	for _, c := range checks {
		min, max, ok := e.Depth(c.v)
		if !ok || min != c.min || max != c.max {
			t.Errorf("Depth(%d) = (%d, %d, %v), want (%d, %d, true)", c.v, min, max, ok, c.min, c.max)
		}
	}
}

func TestSaveLoadResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")

	// Cancel after the first visit completes: visit 0, then stop.
	var order []Item
	inner := testVisit(&order)
	ctx, cancel := context.WithCancel(context.Background())
	e := New(4, func(c context.Context, v, d int) ([]int, error) {
		next, err := inner(c, v, d)
		cancel()
		return next, err
	})
	e.Seed(0)
	if err := e.Run(ctx); err != context.Canceled {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if err := e.Save(path); err != nil {
		t.Fatal(err)
	}

	e2, err := Load(path, testVisit(&order))
	if err != nil {
		t.Fatal(err)
	}
	if st := e2.Stats(); st.Visited != 1 || st.QueueSize != 2 {
		t.Fatalf("after load: visited=%d queue=%d, want 1 and 2", st.Visited, st.QueueSize)
	}
	if err := e2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []Item{{0, 0}, {1, 1}, {2, 1}, {3, 2}}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("combined visit order = %v, want %v", order, want)
	}
	if min, max, ok := e2.Depth(3); !ok || min != 2 || max != 3 {
		t.Errorf("Depth(3) after resume = (%d, %d, %v), want (2, 3, true)", min, max, ok)
	}
}
