package api

import (
	"context"
	"postit/internal/models"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-resty/resty/v2"
)

type HammerResults struct {
	TotalRequests int64
	SuccessCount  int64
	FailureCount  int64
	TotalDuration time.Duration
	AverageLatency time.Duration
	P95Latency     time.Duration
	P99Latency     time.Duration
	RPS           float64
	StatusCodes   map[int]int64
	Latencies     []time.Duration
	Mutex         sync.Mutex
}

func (c *Client) Hammer(req *models.Request, workers int, duration time.Duration) *HammerResults {
	results := &HammerResults{
		StatusCodes: make(map[int]int64),
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var wg sync.WaitGroup
	startTime := time.Now()

	// Pre-build the request template to avoid repeated processing
	// We use a shared client for all workers
	client := resty.New()
	url := c.Processor.ResolveVariables(req.URL.Raw)
	
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					r := client.R()
					// Re-apply headers and body for each request in case they contain dynamic parts (though here we pre-resolve mostly)
					// For a true "hammer", we might want to resolve variables once per hammer run or once per request.
					// Let's resolve once per run for now for maximum speed.
					
					for _, h := range c.Storage.GlobalHeaders {
						r.SetHeader(h.Key, c.Processor.ResolveVariables(h.Value))
					}
					for _, h := range req.Header {
						r.SetHeader(h.Key, c.Processor.ResolveVariables(h.Value))
					}
					
					if req.Body != nil && req.Body.Mode == "raw" {
						r.SetBody(c.Processor.ResolveVariables(req.Body.Raw))
					}

					reqStartTime := time.Now()
					var resp *resty.Response
					var err error

					switch req.Method {
					case "GET": resp, err = r.Get(url)
					case "POST": resp, err = r.Post(url)
					case "PUT": resp, err = r.Put(url)
					case "DELETE": resp, err = r.Delete(url)
					case "PATCH": resp, err = r.Patch(url)
					}

					latency := time.Since(reqStartTime)
					atomic.AddInt64(&results.TotalRequests, 1)

					results.Mutex.Lock()
					results.Latencies = append(results.Latencies, latency)
					if err != nil || resp.IsError() {
						results.FailureCount++
					} else {
						results.SuccessCount++
					}
					if resp != nil {
						results.StatusCodes[resp.StatusCode()]++
					}
					results.AverageLatency += latency 
					results.Mutex.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	actualDuration := time.Since(startTime)
	results.TotalDuration = actualDuration

	if results.TotalRequests > 0 {
		results.AverageLatency = time.Duration(int64(results.AverageLatency) / results.TotalRequests)
		results.RPS = float64(results.TotalRequests) / actualDuration.Seconds()

		sort.Slice(results.Latencies, func(i, j int) bool {
			return results.Latencies[i] < results.Latencies[j]
		})
		
		p95Idx := int(float64(len(results.Latencies)) * 0.95)
		if p95Idx >= len(results.Latencies) { p95Idx = len(results.Latencies) - 1 }
		results.P95Latency = results.Latencies[p95Idx]

		p99Idx := int(float64(len(results.Latencies)) * 0.99)
		if p99Idx >= len(results.Latencies) { p99Idx = len(results.Latencies) - 1 }
		results.P99Latency = results.Latencies[p99Idx]
	}

	return results
}
