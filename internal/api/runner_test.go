package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"testing"
)

func setupRunner(t *testing.T) (*Runner, *storage.Manager, *Client) {
	t.Helper()
	tmpDir := t.TempDir()
	store := storage.NewManager(tmpDir)
	store.Init()
	proc := processor.NewScriptProcessor(store)
	proc.EnablePrompts = false
	client := NewClient(store, proc)
	t.Cleanup(func() { client.Close() })
	runner := NewRunner(client, store, proc)
	return runner, store, client
}

func TestNewRunner(t *testing.T) {
	runner, store, client := setupRunner(t)

	if runner == nil {
		t.Fatal("NewRunner returned nil")
	}
	if runner.Client != client {
		t.Error("Client not set correctly")
	}
	if runner.Storage != store {
		t.Error("Storage not set correctly")
	}
}

func TestRunner_RunIteration_EmptyData(t *testing.T) {
	runner, _, _ := setupRunner(t)

	req := models.RequestInfo{
		Path: "Test > Req",
		Request: &models.Request{
			Method: "GET",
			URL:    models.URL{Raw: "https://example.com"},
			Body:   &models.Body{Mode: "raw", Raw: ""},
		},
	}

	results := runner.RunIteration(context.Background(), req, []map[string]string{})
	if len(results) != 0 {
		t.Errorf("Expected 0 results for empty data, got %d", len(results))
	}
}

func TestRunner_RunIteration_WithDataRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runner, _, _ := setupRunner(t)

	req := models.RequestInfo{
		Path: "Test > Req",
		Request: &models.Request{
			Method: "GET",
			URL:    models.URL{Raw: server.URL},
			Body:   &models.Body{Mode: "raw", Raw: ""},
		},
		Events: []models.Event{},
	}

	data := []map[string]string{
		{"row": "1"},
		{"row": "2"},
		{"row": "3"},
	}

	results := runner.RunIteration(context.Background(), req, data)
	if len(results) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Iteration != i+1 {
			t.Errorf("Result %d: expected iteration %d, got %d", i, i+1, r.Iteration)
		}
		if r.StatusCode != http.StatusOK {
			t.Errorf("Result %d: expected status %d, got %d", i, http.StatusOK, r.StatusCode)
		}
	}
}

func TestRunner_RunIteration_WithLocalVars(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runner, _, _ := setupRunner(t)

	req := models.RequestInfo{
		Path: "Test > DynamicReq",
		Request: &models.Request{
			Method: "GET",
			URL:    models.URL{Raw: server.URL + "/{{id}}"},
			Body:   &models.Body{Mode: "raw", Raw: ""},
		},
	}

	data := []map[string]string{
		{"id": "1"},
		{"id": "2"},
	}

	results := runner.RunIteration(context.Background(), req, data)
	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	for i, r := range results {
		if r.StatusCode != http.StatusOK {
			t.Errorf("Result %d: expected 200 with variable resolution, got %d", i, r.StatusCode)
		}
	}
}

func TestRunner_RunIteration_RequestError(t *testing.T) {
	runner, _, _ := setupRunner(t)

	req := models.RequestInfo{
		Path: "Test > BadReq",
		Request: &models.Request{
			Method: "GET",
			URL:    models.URL{Raw: "http://nonexistent.invalid"},
			Body:   &models.Body{Mode: "raw", Raw: ""},
		},
	}

	data := []map[string]string{
		{"row": "1"},
	}

	results := runner.RunIteration(context.Background(), req, data)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0].StatusCode != 0 {
		t.Errorf("Expected status 0 for failed request, got %d", results[0].StatusCode)
	}
	// Note: RunIteration does NOT currently populate the Error field
	// Status 0 and missing result data indicates the request failure
	if results[0].Error == "" {
		t.Error("Expected error for failed request")
	}
}
