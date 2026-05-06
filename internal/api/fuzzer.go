package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"postit/internal/models"
	"sync"
	"time"
)

type FuzzResult struct {
	Field        string `json:"field"`
	Payload      string `json:"payload"`
	StatusCode   int    `json:"statusCode"`
	ResponseTime int64  `json:"responseTime"`
	Error        string `json:"error,omitempty"`
}

type Fuzzer struct {
	client *http.Client
}

func NewFuzzer() *Fuzzer {
	return &Fuzzer{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

var payloads = map[string][]string{
	"SQLi": {
		"' OR 1=1 --",
		"admin'--",
		"' UNION SELECT NULL, NULL, NULL --",
		"sleep(5)",
		"'; DROP TABLE users; --",
	},
	"XSS": {
		"<script>alert(1)</script>",
		"\"><img src=x onerror=alert(1)>",
		"javascript:alert(1)",
		"<svg/onload=alert(1)>",
	},
	"Robustness": {
		"null",
		"0",
		"-1",
		"99999999999999999999999999",
		"",
		"undefined",
		"[object Object]",
	},
}

const maxConcurrentFuzz = 20

func (f *Fuzzer) Run(ctx context.Context, reqInfo models.RequestInfo, variables map[string]string) ([]FuzzResult, error) {
	results := []FuzzResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, maxConcurrentFuzz)

	// 1. Identify injection points in Body (JSON only for now)
	var bodyMap map[string]interface{}
	if reqInfo.Request.Body != nil && reqInfo.Request.Body.Mode == "raw" {
		json.Unmarshal([]byte(reqInfo.Request.Body.Raw), &bodyMap)
	}

	// 2. Identify injection points in URL Params
	u, err := url.Parse(reqInfo.Request.URL.Raw)
	var queryParams url.Values
	if err == nil && u != nil {
		queryParams = u.Query()
	}

	// Fuzz Body
	for key := range bodyMap {
		for category, pList := range payloads {
			for _, p := range pList {
				wg.Add(1)
				sem <- struct{}{}
				go func(k, p, cat string) {
					defer wg.Done()
					defer func() { <-sem }()
					res := f.executeFuzz(ctx, reqInfo, k, p, "body", bodyMap, queryParams)
					mu.Lock()
					results = append(results, res)
					mu.Unlock()
				}(key, p, category)
			}
		}
	}

	// Fuzz Query Params
	for key := range queryParams {
		for category, pList := range payloads {
			for _, p := range pList {
				wg.Add(1)
				sem <- struct{}{}
				go func(k, p, cat string) {
					defer wg.Done()
					defer func() { <-sem }()
					res := f.executeFuzz(ctx, reqInfo, k, p, "query", bodyMap, queryParams)
					mu.Lock()
					results = append(results, res)
					mu.Unlock()
				}(key, p, category)
			}
		}
	}

	wg.Wait()
	return results, nil
}

func (f *Fuzzer) executeFuzz(ctx context.Context, reqInfo models.RequestInfo, key, payload, target string, originalBody map[string]interface{}, originalQuery url.Values) FuzzResult {
	// Clone and Inject
	fuzzedUrl := reqInfo.Request.URL.Raw
	var fuzzedBody []byte

	if target == "query" {
		u, err := url.Parse(fuzzedUrl)
		if err != nil {
			return FuzzResult{Field: key, Payload: payload, Error: "Invalid URL: " + err.Error()}
		}
		q := u.Query()
		q.Set(key, payload)
		u.RawQuery = q.Encode()
		fuzzedUrl = u.String()
		if originalBody != nil {
			fuzzedBody, _ = json.Marshal(originalBody)
		}
	} else {
		clonedBody := make(map[string]interface{})
		for k, v := range originalBody {
			clonedBody[k] = v
		}
		clonedBody[key] = payload
		fuzzedBody, _ = json.Marshal(clonedBody)
	}

	req, err := http.NewRequest(reqInfo.Request.Method, fuzzedUrl, bytes.NewBuffer(fuzzedBody))
	if err != nil {
		return FuzzResult{Field: key, Payload: payload, Error: "Failed to create request: " + err.Error(), ResponseTime: 0}
	}
	for _, h := range reqInfo.Request.Header {
		req.Header.Add(h.Key, h.Value)
	}
	if fuzzedBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := f.client.Do(req.WithContext(ctx))
	duration := time.Since(start).Milliseconds()

	if err != nil {
		return FuzzResult{Field: key, Payload: payload, Error: err.Error(), ResponseTime: duration}
	}
	// Safe to defer after confirming resp is not nil
	defer resp.Body.Close()

	// Consume body to ensure connection can be reused
	io.ReadAll(resp.Body)

	return FuzzResult{
		Field:        key,
		Payload:      payload,
		StatusCode:   resp.StatusCode,
		ResponseTime: duration,
	}
}
