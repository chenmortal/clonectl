// Package agent — NormalizedProgress helpers shared by drivers and
// the runner.
//
// Drivers call TranslateProgress with a RawProgress payload extracted
// from TaskStatus.Raw; the runner then stores NormalizedProgress on
// SyncRun / CheckRun rows so the UI renders a single shape across
// every upstream tool.
//
// Adding a new tool = adding a switch case in the translator below.
// Everything else (scheduler, runner, UI) keeps using NormalizedProgress.
package agent

// ClampPercent returns Done / Total as a percent in [0, 100]. Returns
// -1 when total is unknown or non-positive.
func ClampPercent(done, total int64) int {
	if total <= 0 {
		return -1
	}
	p := (done * 100) / total
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return int(p)
}

// MergeLatest keeps the monotonically-advancing counter view of two
// snapshots. Used when a poll arrives slightly out of order — we want
// the UI to never show Done going backwards.
func MergeLatest(prev, next DoneThroughput) DoneThroughput {
	if next.Done < prev.Done {
		next.Done = prev.Done
	}
	if next.Throughput < 0 {
		next.Throughput = prev.Throughput
	}
	if next.LagSeconds < 0 {
		next.LagSeconds = prev.LagSeconds
	}
	return next
}

// DoneThroughput is the common subset of NormalizedProgress used for
// "is this run actually advancing" checks. Defined here so the runner
// can reason about liveness without re-parsing NormalizedProgress.
type DoneThroughput struct {
	Done        int64
	Throughput  float64
	LagSeconds  float64
}

// ExtractDoneThroughput pulls the monotonic-view fields out of a
// NormalizedProgress for liveness accounting.
func ExtractDoneThroughput(p NormalizedProgress) DoneThroughput {
	return DoneThroughput{
		Done:       p.Done,
		Throughput: p.Throughput,
		LagSeconds: p.LagSeconds,
	}
}
