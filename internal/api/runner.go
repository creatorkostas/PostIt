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
		// Inject CSV/JSON row into local variables
		localVars := make(map[string]string)
		for k, v := range row {
			localVars[k] = v
		}

		startTime := time.Now().UnixNano() / int64(time.Millisecond)
		body, _, statusCode, statusText := r.Client.ExecuteRequestWithLocal(ctx, req.Request, localVars)
		duration := (time.Now().UnixNano() / int64(time.Millisecond)) - startTime

		// Post-request scripts (Tests)
		r.Processor.RunScriptsWithLocal(req.Events, "test", []byte(body), nil, req.Request.Header, localVars)

		results = append(results, RunnerResult{
			Iteration:  i + 1,
			StatusCode: statusCode,
			StatusText: statusText,
			Duration:   duration,
		})
	}

	return results
}
