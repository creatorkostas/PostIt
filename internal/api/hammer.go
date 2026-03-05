package api

import (
	"context"
	"math/rand"
	"postit/internal/models"
	"sort"
	"strings"
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
	totalLatency  int64 // nano
	Mutex         sync.Mutex
}

const maxSamples = 10000

func (c *Client) Hammer(req *models.Request, workers int, duration time.Duration) *HammerResults {
	results := &HammerResults{
		StatusCodes: make(map[int]int64),
		Latencies:   make([]time.Duration, 0, maxSamples),
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var wg sync.WaitGroup
	startTime := time.Now()

	client := resty.New()
	
	// Pre-resolve what we can (PERF-004)
	baseUrl := c.Processor.ResolveVariables(req.URL.Raw)
	isUrlDynamic := strings.Contains(req.URL.Raw, "{{$")

	type resolvedHeader struct {
		key      string
		rawVal   string
		value    string
		isDynamic bool
	}
	
	staticGlobalHeaders := c.Storage.GetGlobalHeaders()
	allHeaders := make([]resolvedHeader, 0, len(staticGlobalHeaders)+len(req.Header))
	
	for _, h := range staticGlobalHeaders {
		allHeaders = append(allHeaders, resolvedHeader{
			key:       h.Key,
			rawVal:    h.Value,
			value:     c.Processor.ResolveVariables(h.Value),
			isDynamic: strings.Contains(h.Value, "{{$"),
		})
	}
	for _, h := range req.Header {
		allHeaders = append(allHeaders, resolvedHeader{
			key:       h.Key,
			rawVal:    h.Value,
			value:     c.Processor.ResolveVariables(h.Value),
			isDynamic: strings.Contains(h.Value, "{{$"),
		})
	}

	var preResolvedBody string
	isBodyDynamic := false
	if req.Body != nil && req.Body.Mode == "raw" {
		preResolvedBody = c.Processor.ResolveVariables(req.Body.Raw)
		isBodyDynamic = strings.Contains(req.Body.Raw, "{{$")
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rGen := rand.New(rand.NewSource(time.Now().UnixNano()))
			for {
				select {
				case <-ctx.Done():
					return
				default:
					r := client.R()
					
					// URL
					hitUrl := baseUrl
					if isUrlDynamic {
						hitUrl = c.Processor.ResolveVariables(req.URL.Raw)
					}

					// Headers
					for _, h := range allHeaders {
						val := h.value
						if h.isDynamic {
							val = c.Processor.ResolveVariables(h.rawVal)
						}
						r.SetHeader(h.key, val)
					}
					
					// Body
					if req.Body != nil && req.Body.Mode == "raw" {
						body := preResolvedBody
						if isBodyDynamic {
							body = c.Processor.ResolveVariables(req.Body.Raw)
						}
						r.SetBody(body)
					}

					reqStartTime := time.Now()
					var resp *resty.Response
					var err error

					switch strings.ToUpper(req.Method) {
					case "GET": resp, err = r.Get(hitUrl)
					case "POST": resp, err = r.Post(hitUrl)
					case "PUT": resp, err = r.Put(hitUrl)
					case "DELETE": resp, err = r.Delete(hitUrl)
					case "PATCH": resp, err = r.Patch(hitUrl)
					default: resp, err = r.Get(hitUrl)
					}

					latency := time.Since(reqStartTime)
					count := atomic.AddInt64(&results.TotalRequests, 1)
					atomic.AddInt64(&results.totalLatency, latency.Nanoseconds())

					if err != nil || (resp != nil && resp.IsError()) {
						atomic.AddInt64(&results.FailureCount, 1)
					} else {
						atomic.AddInt64(&results.SuccessCount, 1)
					}

					results.Mutex.Lock()
					if resp != nil {
						results.StatusCodes[resp.StatusCode()]++
					}
					
					// Reservoir Sampling
					if len(results.Latencies) < maxSamples {
						results.Latencies = append(results.Latencies, latency)
					} else {
						j := rGen.Int63n(count)
						if j < maxSamples {
							results.Latencies[j] = latency
						}
					}
					results.Mutex.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	actualDuration := time.Since(startTime)
	results.TotalDuration = actualDuration

	if results.TotalRequests > 0 {
		results.AverageLatency = time.Duration(results.totalLatency / results.TotalRequests)
		results.RPS = float64(results.TotalRequests) / actualDuration.Seconds()

		if len(results.Latencies) > 0 {
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
	}

	return results
}
