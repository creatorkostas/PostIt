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

func setupWorkflowTest(t *testing.T) (*Client, *storage.Manager, []models.RequestInfo) {
	t.Helper()
	tmpDir := t.TempDir()
	store := storage.NewManager(tmpDir)
	store.Init()
	proc := processor.NewScriptProcessor(store)
	proc.EnablePrompts = false
	client := NewClient(store, proc)
	t.Cleanup(func() { client.Close() })

	requests := []models.RequestInfo{
		{
			Path:    "API > Get Users",
			Request: &models.Request{Method: "GET", URL: models.URL{Raw: "https://api.example.com/users"}, Body: &models.Body{Mode: "raw", Raw: ""}},
			Order:   0,
		},
		{
			Path:    "API > Create User",
			Request: &models.Request{Method: "POST", URL: models.URL{Raw: "https://api.example.com/users"}, Body: &models.Body{Mode: "raw", Raw: `{"name":"test"}`}},
			Order:   1,
		},
	}

	return client, store, requests
}

func TestRunWorkflow_EmptyWorkflow(t *testing.T) {
	client, _, requests := setupWorkflowTest(t)

	workflow := &models.Workflow{
		ID:    "test-wf",
		Name:  "Empty",
		Nodes: []models.WorkflowNode{},
	}

	logs, err := client.RunWorkflow(context.Background(), workflow, requests, "")
	if err != nil {
		t.Fatalf("Empty workflow should not error: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("Expected 0 logs for empty workflow, got %d", len(logs))
	}
}

func TestRunWorkflow_RequestNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":123}`))
	}))
	defer server.Close()

	store := setupStorage(t)
	proc := processor.NewScriptProcessor(store)
	proc.EnablePrompts = false
	client := NewClient(store, proc)
	t.Cleanup(func() { client.Close() })

	requests := []models.RequestInfo{
		{
			Path:    "API > Get Data",
			Request: &models.Request{Method: "GET", URL: models.URL{Raw: server.URL + "/data"}, Body: &models.Body{Mode: "raw", Raw: ""}},
			Order:   0,
		},
	}

	workflow := &models.Workflow{
		ID:   "wf-1",
		Name: "Test",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "request", RequestPath: "API > Get Data"},
		},
		Edges: []models.WorkflowEdge{},
	}

	logs, err := client.RunWorkflow(context.Background(), workflow, requests, "")
	if err != nil {
		t.Fatalf("Workflow execution failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Expected 1 log entry, got %d", len(logs))
	}
	if logs[0].StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", logs[0].StatusCode)
	}
}

func TestRunWorkflow_RequestNodeNotFound(t *testing.T) {
	client, _, requests := setupWorkflowTest(t)

	workflow := &models.Workflow{
		ID:   "wf-2",
		Name: "Bad",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "request", RequestPath: "Non > Existent"},
		},
		Edges: []models.WorkflowEdge{},
	}

	logs, err := client.RunWorkflow(context.Background(), workflow, requests, "")
	if err != nil {
		t.Fatalf("Workflow should not error on not-found: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Expected 1 log entry, got %d", len(logs))
	}
	if logs[0].Error != "Request not found" {
		t.Errorf("Expected 'Request not found' error, got '%s'", logs[0].Error)
	}
}

func TestRunWorkflow_WaitNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := setupStorage(t)
	proc := processor.NewScriptProcessor(store)
	proc.EnablePrompts = false
	client := NewClient(store, proc)
	t.Cleanup(func() { client.Close() })

	requests := []models.RequestInfo{
		{
			Path:    "API > Get Data",
			Request: &models.Request{Method: "GET", URL: models.URL{Raw: server.URL}, Body: &models.Body{Mode: "raw", Raw: ""}},
			Order:   0,
		},
	}

	workflow := &models.Workflow{
		ID:   "wf-wait",
		Name: "Wait Test",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "request", RequestPath: "API > Get Data"},
			{ID: "node2", Type: "wait", WaitTime: 1},
		},
		Edges: []models.WorkflowEdge{
			{FromNode: "node1", ToNode: "node2"},
		},
	}

	logs, err := client.RunWorkflow(context.Background(), workflow, requests, "")
	if err != nil {
		t.Fatalf("Workflow failed: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("Expected 2 log entries, got %d", len(logs))
	}
	if logs[1].StatusText != "Waited 1ms" {
		t.Errorf("Expected 'Waited 1ms', got '%s'", logs[1].StatusText)
	}
}

func TestRunWorkflow_InputNode(t *testing.T) {
	client, _, requests := setupWorkflowTest(t)

	workflow := &models.Workflow{
		ID:   "wf-input",
		Name: "Input Test",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "input", VariableName: "userInput"},
		},
		Edges: []models.WorkflowEdge{},
	}

	logs, err := client.RunWorkflow(context.Background(), workflow, requests, "")
	if err != nil {
		t.Fatalf("Workflow failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Expected 1 log entry, got %d", len(logs))
	}
	if workflow.Status != "paused" {
		t.Errorf("Expected workflow status 'paused', got '%s'", workflow.Status)
	}
	if workflow.WaitingFor != "userInput" {
		t.Errorf("Expected WaitingFor 'userInput', got '%s'", workflow.WaitingFor)
	}
}

func TestRunWorkflow_MaxTasksLimit(t *testing.T) {
	client, _, requests := setupWorkflowTest(t)

	// Create a workflow that loops to exceed max tasks
	workflow := &models.Workflow{
		ID:   "wf-loop",
		Name: "Loop Limit",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "request", RequestPath: "API > Get Users"},
		},
		Edges: []models.WorkflowEdge{
			{FromNode: "node1", ToNode: "node1"}, // Self-loop
		},
	}

	_, err := client.RunWorkflow(context.Background(), workflow, requests, "")
	if err == nil {
		t.Error("Expected error for excessive task limit")
	}
}

func TestRunWorkflow_CustomStartNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := setupStorage(t)
	proc := processor.NewScriptProcessor(store)
	proc.EnablePrompts = false
	client := NewClient(store, proc)
	t.Cleanup(func() { client.Close() })

	requests := []models.RequestInfo{
		{
			Path:    "API > Step1",
			Request: &models.Request{Method: "GET", URL: models.URL{Raw: server.URL}, Body: &models.Body{Mode: "raw", Raw: ""}},
			Order:   0,
		},
		{
			Path:    "API > Step2",
			Request: &models.Request{Method: "GET", URL: models.URL{Raw: server.URL}, Body: &models.Body{Mode: "raw", Raw: ""}},
			Order:   1,
		},
	}

	workflow := &models.Workflow{
		ID:   "wf-start",
		Name: "Start At",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "request", RequestPath: "API > Step1"},
			{ID: "node2", Type: "request", RequestPath: "API > Step2"},
		},
		Edges: []models.WorkflowEdge{
			{FromNode: "node1", ToNode: "node2"},
		},
	}

	// Start at node2 (skip node1)
	logs, err := client.RunWorkflow(context.Background(), workflow, requests, "node2")
	if err != nil {
		t.Fatalf("Workflow failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Expected 1 log entry (only node2), got %d", len(logs))
	}
	if logs[0].NodeID != "node2" {
		t.Errorf("Expected node2, got '%s'", logs[0].NodeID)
	}
}

func setupStorage(t *testing.T) *storage.Manager {
	t.Helper()
	tmpDir := t.TempDir()
	store := storage.NewManager(tmpDir)
	store.Init()
	return store
}
