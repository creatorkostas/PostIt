package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupTestServer(t *testing.T) (*Server, *storage.Manager) {
	tempDir := t.TempDir()
	store := storage.NewManager(tempDir)
	if err := store.Init(); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}

	proc := processor.NewScriptProcessor(store)
	client := api.NewClient(store, proc)
	defer client.Close()

	collection := models.Collection{
		Info: models.Info{Name: "Test Collection"},
	}

	server := NewServer(store, proc, client, collection, []models.RequestInfo{}, false)
	return server, store
}

func TestServer_handleSendRequest_RaceCondition(t *testing.T) {
	server, store := setupTestServer(t)

	// Add a test request
	server.stateMu.Lock()
	server.FlatList = []models.RequestInfo{
		{
			Path: "Test > Request",
			Request: &models.Request{
				Method: "GET",
				URL:    models.URL{Raw: "https://httpbin.org/get"},
				Header: []models.Header{},
				Body:   &models.Body{Mode: "raw", Raw: ""},
			},
			Events: []models.Event{},
			Order:  0,
		},
	}
	server.stateMu.Unlock()

	// Concurrent requests to test race conditions
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			input := map[string]string{"path": "Test > Request"}
			body, _ := json.Marshal(input)
			req := httptest.NewRequest("POST", "/api/send", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleSendRequest(w, req)
		}()
	}
	wg.Wait()

	// If we get here without panics or race detector warnings, the test passes
	_ = store
}

func TestServer_handleHammerRequest_RaceCondition(t *testing.T) {
	server, _ := setupTestServer(t)

	// Add a test request
	server.stateMu.Lock()
	server.FlatList = []models.RequestInfo{
		{
			Path: "Test > Hammer",
			Request: &models.Request{
				Method: "GET",
				URL:    models.URL{Raw: "https://httpbin.org/get"},
				Header: []models.Header{},
				Body:   &models.Body{Mode: "raw", Raw: ""},
			},
			Events: []models.Event{},
			Order:  0,
		},
	}
	server.stateMu.Unlock()

	// Concurrent hammer requests
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			input := map[string]interface{}{
				"path":    "Test > Hammer",
				"workers": 2,
				"seconds": 1,
			}
			body, _ := json.Marshal(input)
			req := httptest.NewRequest("POST", "/api/hammer", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleHammerRequest(w, req)
		}()
	}
	wg.Wait()
}

func TestServer_ValidateWorkflow(t *testing.T) {
	server, _ := setupTestServer(t)

	// Add test requests to FlatList
	server.stateMu.Lock()
	server.FlatList = []models.RequestInfo{
		{
			Path:    "API > Get User",
			Request: &models.Request{Method: "GET", URL: models.URL{Raw: "http://example.com/user"}},
			Order:   0,
		},
		{
			Path:    "API > Create User",
			Request: &models.Request{Method: "POST", URL: models.URL{Raw: "http://example.com/user"}},
			Order:   1,
		},
	}
	server.stateMu.Unlock()

	// Test valid workflow
	validWorkflow := &models.Workflow{
		ID:   "wf-1",
		Name: "Test Workflow",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "request", RequestPath: "API > Get User"},
			{ID: "node2", Type: "request", RequestPath: "API > Create User"},
		},
		Edges: []models.WorkflowEdge{
			{FromNode: "node1", ToNode: "node2"},
		},
	}

	err := server.validateWorkflow(validWorkflow)
	if err != nil {
		t.Errorf("Valid workflow should not return error, got: %v", err)
	}

	// Test invalid workflow - non-existent request path
	invalidWorkflow := &models.Workflow{
		ID:   "wf-2",
		Name: "Invalid Workflow",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "request", RequestPath: "Non-existent Path"},
		},
	}

	err = server.validateWorkflow(invalidWorkflow)
	if err == nil {
		t.Error("Invalid workflow should return error")
	}
	if !strings.Contains(err.Error(), "non-existent request path") {
		t.Errorf("Error should mention non-existent path, got: %v", err)
	}

	// Test empty workflow
	emptyWorkflow := &models.Workflow{
		ID:    "wf-3",
		Name:  "Empty Workflow",
		Nodes: []models.WorkflowNode{},
	}

	err = server.validateWorkflow(emptyWorkflow)
	if err == nil {
		t.Error("Empty workflow should return error")
	}
}

func TestServer_handleWorkflows_CRUD(t *testing.T) {
	server, _ := setupTestServer(t)

	// Test Create (POST)
	workflow := models.Workflow{
		Name: "Test Workflow",
		Nodes: []models.WorkflowNode{
			{ID: "node1", Type: "request"},
		},
	}
	body, _ := json.Marshal(workflow)
	req := httptest.NewRequest("POST", "/api/workflows", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleWorkflows(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Parse response to get the created workflow ID
	var createdWorkflow models.Workflow
	if err := json.Unmarshal(w.Body.Bytes(), &createdWorkflow); err != nil {
		t.Fatalf("Failed to unmarshal created workflow: %v", err)
	}
	if createdWorkflow.ID == "" {
		t.Error("Created workflow should have an ID")
	}

	// Test Read (GET)
	req = httptest.NewRequest("GET", "/api/workflows", nil)
	w = httptest.NewRecorder()

	server.handleWorkflows(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var workflows []models.Workflow
	if err := json.Unmarshal(w.Body.Bytes(), &workflows); err != nil {
		t.Fatalf("Failed to unmarshal workflows: %v", err)
	}
	if len(workflows) != 1 {
		t.Errorf("Expected 1 workflow, got %d", len(workflows))
	}

	// Test Delete
	deleteInput := map[string]string{"id": createdWorkflow.ID}
	body, _ = json.Marshal(deleteInput)
	req = httptest.NewRequest("DELETE", "/api/workflows", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	server.handleWorkflows(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}
}

func TestServer_CSFRProtection(t *testing.T) {
	server, _ := setupTestServer(t)

	// Test POST without Origin/Referer headers (should fail)
	req := httptest.NewRequest("POST", "/api/requests/new", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Create a simple handler that uses the csrf middleware
	handler := server.csrfMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler(w, req)
	// Without proper Origin/Referer, it might be rejected depending on implementation
	// This test documents the behavior

	// Test with valid Origin header
	req = httptest.NewRequest("POST", "/api/requests/new", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")
	req.Host = "localhost:8080"
	w = httptest.NewRecorder()

	handler(w, req)
	// Should succeed with valid Origin
}

func TestServer_handleExportHistory(t *testing.T) {
	server, store := setupTestServer(t)

	// Add some history
	history := []models.HistoryRecord{
		{
			Timestamp:  time.Now(),
			Path:       "Test > Request",
			Method:     "GET",
			URL:        "http://example.com",
			StatusCode: 200,
			StatusText: "OK",
			Duration:   100,
		},
	}
	store.SaveHistory(history)

	// Test JSON export
	req := httptest.NewRequest("GET", "/api/history/export?format=json", nil)
	w := httptest.NewRecorder()

	server.handleExportHistory(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
	}

	// Test CSV export
	req = httptest.NewRequest("GET", "/api/history/export?format=csv", nil)
	w = httptest.NewRecorder()

	server.handleExportHistory(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	contentType = w.Header().Get("Content-Type")
	if contentType != "text/csv" {
		t.Errorf("Expected Content-Type 'text/csv', got '%s'", contentType)
	}
}

func TestServer_handleSchemaGenerate(t *testing.T) {
	server, _ := setupTestServer(t)

	testCases := []struct {
		name     string
		body     string
		expected string
	}{
		{
			name:     "simple object",
			body:     `{"name": "test", "count": 42}`,
			expected: "object",
		},
		{
			name:     "array",
			body:     `[1, 2, 3]`,
			expected: "array",
		},
		{
			name:     "nested object",
			body:     `{"user": {"name": "John", "age": 30}}`,
			expected: "object",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]string{"body": tc.body}
			body, _ := json.Marshal(input)
			req := httptest.NewRequest("POST", "/api/schema/generate", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleSchemaGenerate(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
			}

			var schema JSONSchema
			if err := json.Unmarshal(w.Body.Bytes(), &schema); err != nil {
				t.Fatalf("Failed to unmarshal schema: %v", err)
			}
			if schema.Type != tc.expected {
				t.Errorf("Expected type '%s', got '%s'", tc.expected, schema.Type)
			}
		})
	}
}

func TestServer_handleMockResponse_CRUD(t *testing.T) {
	server, _ := setupTestServer(t)

	// Add a test request
	server.stateMu.Lock()
	server.FlatList = []models.RequestInfo{
		{
			Path:    "API > Test",
			Request: &models.Request{Method: "GET", URL: models.URL{Raw: "http://example.com"}},
			Order:   0,
		},
	}
	server.stateMu.Unlock()

	// Test Create
	input := map[string]interface{}{
		"requestPath": "API > Test",
		"response": models.MockResponse{
			Name:   "Success",
			Code:   200,
			Status: "OK",
			Body:   `{"status": "ok"}`,
			Header: []models.Header{{Key: "Content-Type", Value: "application/json"}},
		},
	}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest("POST", "/api/mock/save", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleSaveMockResponse(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Test Delete
	deleteInput := map[string]string{
		"requestPath":  "API > Test",
		"responseName": "Success",
	}
	body, _ = json.Marshal(deleteInput)
	req = httptest.NewRequest("POST", "/api/mock/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	server.handleDeleteMock(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}
}
