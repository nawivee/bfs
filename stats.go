package bfs

import (
	"encoding/json"
	"net/http"
	"time"
)

// Stats is a snapshot of BFS progress, suitable for JSON serialization.
type Stats struct {
	StartTime      time.Time `json:"start_time"`      // first ever start (survives resume)
	ElapsedSeconds float64   `json:"elapsed_seconds"` // total running time across sessions

	TotalNodes int     `json:"total_nodes"`
	Visited    int64   `json:"visited"`
	Progress   float64 `json:"progress"` // visited / total_nodes

	QueueSize int   `json:"queue_size"`
	QueueMin  *Item `json:"queue_min,omitempty"` // head of the queue (min depth, then min vertex)
	QueueMax  *Item `json:"queue_max,omitempty"` // last element by the same ordering

	OverallRatePerSec float64 `json:"overall_rate_per_sec"` // visited / elapsed since the beginning
	RecentRatePerSec  float64 `json:"recent_rate_per_sec"`  // speed over the last 10 minutes

	// ETAs assume all total_nodes vertices are reachable; if some are not,
	// these are upper bounds. eta_seconds/eta_at use the recent rate when
	// available and fall back to the overall rate.
	EtaOverallSeconds *float64   `json:"eta_overall_seconds,omitempty"`
	EtaRecentSeconds  *float64   `json:"eta_recent_seconds,omitempty"`
	EtaSeconds        *float64   `json:"eta_seconds,omitempty"`
	EtaAt             *time.Time `json:"eta_at,omitempty"`
}

// Stats returns a snapshot of the current progress.
func (e *Engine) Stats() Stats {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()

	e.pruneSamplesLocked(now)

	st := Stats{
		StartTime:      e.firstStart,
		ElapsedSeconds: e.elapsedLocked().Seconds(),
		TotalNodes:     e.n,
		Visited:        e.visitedCount,
		QueueSize:      e.queue.Len(),
	}
	if e.n > 0 {
		st.Progress = float64(e.visitedCount) / float64(e.n)
	}

	if len(e.queue) > 0 {
		min := e.queue[0]
		max := e.queue[0]
		for _, it := range e.queue[1:] {
			if it.Depth > max.Depth || (it.Depth == max.Depth && it.Vertex > max.Vertex) {
				max = it
			}
		}
		st.QueueMin = &min
		st.QueueMax = &max
	}

	if el := e.elapsedLocked().Seconds(); el > 0 {
		st.OverallRatePerSec = float64(e.visitedCount) / el
	}
	if len(e.samples) >= 2 {
		first, last := e.samples[0], e.samples[len(e.samples)-1]
		if dt := last.T.Sub(first.T).Seconds(); dt > 0 {
			st.RecentRatePerSec = float64(last.Count-first.Count) / dt
		}
	}

	remaining := float64(e.n) - float64(e.visitedCount)
	if remaining > 0 {
		if st.OverallRatePerSec > 0 {
			v := remaining / st.OverallRatePerSec
			st.EtaOverallSeconds = &v
		}
		if st.RecentRatePerSec > 0 {
			v := remaining / st.RecentRatePerSec
			st.EtaRecentSeconds = &v
		}
		eta := st.EtaRecentSeconds
		if eta == nil {
			eta = st.EtaOverallSeconds
		}
		if eta != nil {
			st.EtaSeconds = eta
			at := now.Add(time.Duration(*eta * float64(time.Second)))
			st.EtaAt = &at
		}
	}
	return st
}

// StatsHandler returns an http.Handler that serves the current Stats as JSON.
// Mount it wherever convenient, e.g. http.Handle("/stats", e.StatsHandler()).
func (e *Engine) StatsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(e.Stats())
	})
}
