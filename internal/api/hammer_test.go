package api

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildSnapshot(t *testing.T) {
	t.Run("empty results", func(t *testing.T) {
		r := &HammerResults{
			StatusCodes: make(map[int]int64),
			Latencies:   make([]time.Duration, 0),
		}
		snapshot := buildSnapshot(r, 0, false)

		if snapshot.TotalRequests != 0 {
			t.Errorf("Expected 0 total requests, got %d", snapshot.TotalRequests)
		}
		if snapshot.Done {
			t.Error("Expected done=false")
		}
		if snapshot.RPS != 0 {
			t.Errorf("Expected 0 RPS, got %f", snapshot.RPS)
		}
	})

	t.Run("with some results", func(t *testing.T) {
		r := &HammerResults{
			StatusCodes: make(map[int]int64),
			Latencies:   make([]time.Duration, 0, maxSamples),
		}
		atomic.AddInt64(&r.TotalRequests, 10)
		atomic.AddInt64(&r.SuccessCount, 8)
		atomic.AddInt64(&r.FailureCount, 2)
		atomic.AddInt64(&r.totalLatency, 100*int64(time.Millisecond)) // 100ms total
		r.Mutex.Lock()
		r.StatusCodes[200] = 8
		r.StatusCodes[500] = 2
		r.Mutex.Unlock()

		snapshot := buildSnapshot(r, time.Second, true)

		if snapshot.TotalRequests != 10 {
			t.Errorf("Expected 10 requests, got %d", snapshot.TotalRequests)
		}
		if snapshot.SuccessCount != 8 {
			t.Errorf("Expected 8 success, got %d", snapshot.SuccessCount)
		}
		if snapshot.FailureCount != 2 {
			t.Errorf("Expected 2 failures, got %d", snapshot.FailureCount)
		}
		if !snapshot.Done {
			t.Error("Expected done=true")
		}
		if snapshot.RPS != 10.0 {
			t.Errorf("Expected 10 RPS, got %f", snapshot.RPS)
		}
		if snapshot.AvgLatencyMs != 10.0 {
			t.Errorf("Expected 10ms avg latency, got %f", snapshot.AvgLatencyMs)
		}
		if snapshot.StatusCodes[200] != 8 {
			t.Errorf("Expected 8 status 200, got %d", snapshot.StatusCodes[200])
		}
		if snapshot.StatusCodes[500] != 2 {
			t.Errorf("Expected 2 status 500, got %d", snapshot.StatusCodes[500])
		}
	})

	t.Run("status code copy doesn't leak", func(t *testing.T) {
		r := &HammerResults{
			StatusCodes: map[int]int64{200: 5},
		}
		snapshot := buildSnapshot(r, time.Second, false)

		// Modify original after snapshot
		r.Mutex.Lock()
		r.StatusCodes[200] = 999
		r.Mutex.Unlock()

		if snapshot.StatusCodes[200] != 5 {
			t.Error("Snapshot should be independent of original")
		}
	})
}

func TestHammerResults_InitialState(t *testing.T) {
	r := &HammerResults{
		StatusCodes: make(map[int]int64),
		Latencies:   make([]time.Duration, 0, maxSamples),
	}

	if r.TotalRequests != 0 {
		t.Errorf("Expected 0 TotalRequests, got %d", r.TotalRequests)
	}
	if r.SuccessCount != 0 {
		t.Errorf("Expected 0 SuccessCount, got %d", r.SuccessCount)
	}
	if r.FailureCount != 0 {
		t.Errorf("Expected 0 FailureCount, got %d", r.FailureCount)
	}
	if r.RPS != 0 {
		t.Errorf("Expected 0 RPS, got %f", r.RPS)
	}
	if r.Latencies == nil {
		t.Error("Latencies slice should be initialized")
	}
}

func TestHammerProgress_PopulatedFields(t *testing.T) {
	p := &HammerProgress{
		Done:          true,
		TotalRequests: 100,
		SuccessCount:  95,
		FailureCount:  5,
		ElapsedMs:     1000,
		RPS:           100.0,
		AvgLatencyMs:  9.5,
		P95LatencyMs:  20.0,
		P99LatencyMs:  50.0,
		StatusCodes: map[int]int64{
			200: 95,
			500: 5,
		},
	}

	if p.TotalRequests != 100 {
		t.Errorf("Expected 100, got %d", p.TotalRequests)
	}
	if p.AvgLatencyMs != 9.5 {
		t.Errorf("Expected 9.5, got %f", p.AvgLatencyMs)
	}
	if p.StatusCodes[200] != 95 {
		t.Errorf("Expected 95, got %d", p.StatusCodes[200])
	}
}
