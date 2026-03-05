package api

import (
	"context"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"time"
)

type RunnerResult struct {
	Iteration  int    `json:"iteration"`
	StatusCode int    `json:"statusCode"`
	StatusText string `json:"statusText"`
	Duration   int64  `json:"duration"`
	Error      string `json:"error,omitempty"`
}

type Runner struct {
	Client    *Client
	Storage   *storage.Manager
	Processor *processor.ScriptProcessor
}

func NewRunner(client *Client, store *storage.Manager, proc *processor.ScriptProcessor) *Runner {
	return &Runner{
		Client:    client,
		Storage:   store,
		Processor: proc,
	}
}

func (r *Runner) RunIteration(ctx context.Context, req models.RequestInfo, data []map[string]string) []RunnerResult {
	results := []RunnerResult{}

	for i, row := range data {
		// Temporary override environment with data row
		originalVars := make(map[string]string)
		for k, v := range r.Storage.VariableMap {
			originalVars[k] = v
		}

		// Inject CSV/JSON row into variables
		for k, v := range row {
			r.Storage.VariableMap[k] = v
		}

		startTime := time.Now().UnixNano() / int64(time.Millisecond)
		body, _, statusCode, statusText := r.Client.ExecuteRequest(ctx, req.Request)
		duration := (time.Now().UnixNano() / int64(time.Millisecond)) - startTime

		// Post-request scripts (Tests)
		r.Processor.RunScripts(req.Events, "test", []byte(body), nil, req.Request.Header)

		results = append(results, RunnerResult{
			Iteration:  i + 1,
			StatusCode: statusCode,
			StatusText: statusText,
			Duration:   duration,
		})

		// Restore original variables
		r.Storage.VariableMap = originalVars
	}

	return results
}
