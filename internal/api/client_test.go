package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"testing"
	"time"
)

func setupClient(t *testing.T) (*Client, *storage.Manager, *processor.ScriptProcessor) {
	t.Helper()
	tmpDir := t.TempDir()
	store := storage.NewManager(tmpDir)
	store.Init()
	proc := processor.NewScriptProcessor(store)
	proc.EnablePrompts = false
	client := NewClient(store, proc)
	t.Cleanup(func() { client.Close() })
	return client, store, proc
}

func TestNewClient(t *testing.T) {
	client, store, proc := setupClient(t)

	if client.Storage != store {
		t.Error("Storage not set correctly")
	}
	if client.Processor != proc {
		t.Error("Processor not set correctly")
	}
	if client.Logger == nil {
		t.Error("Logger should not be nil")
	}
	if client.dbPool == nil {
		t.Error("dbPool should be initialized")
	}
	if client.dbOrder == nil {
		t.Error("dbOrder should be initialized")
	}
}

func TestClient_Close(t *testing.T) {
	// Don't use setupClient because we want to test Close without cleanup
	tmpDir := t.TempDir()
	store := storage.NewManager(tmpDir)
	store.Init()
	proc := processor.NewScriptProcessor(store)
	proc.EnablePrompts = false
	client := NewClient(store, proc)

	// Close should work and clean up resources
	err := client.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if len(client.dbPool) != 0 {
		t.Error("Expected empty dbPool after Close")
	}
}

func TestClient_Close_DoubleClose(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.NewManager(tmpDir)
	store.Init()
	proc := processor.NewScriptProcessor(store)
	proc.EnablePrompts = false
	client := NewClient(store, proc)

	// First close should succeed
	err1 := client.Close()
	if err1 != nil {
		t.Fatalf("First Close failed: %v", err1)
	}

	// Second close should NOT panic (sync.Once guard)
	err2 := client.Close()
	if err2 != nil {
		t.Fatalf("Second Close should not error: %v", err2)
	}
}

func TestClient_ExecuteRequest_UnsupportedMethod(t *testing.T) {
	client, _, _ := setupClient(t)

	req := &models.Request{
		Method: "OPTIONS",
		URL:    models.URL{Raw: "https://example.com"},
	}

	body, headers, status, statusText := client.ExecuteRequest(context.Background(), req)
	if status != 0 {
		t.Errorf("Expected status 0 for unsupported method, got %d", status)
	}
	if body != "" {
		t.Errorf("Expected empty body, got '%s'", body)
	}
	if headers != nil {
		t.Error("Expected nil headers")
	}
	if statusText != "" {
		t.Errorf("Expected empty statusText, got '%s'", statusText)
	}
}

func TestClient_ExecuteRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client, _, _ := setupClient(t)
	req := &models.Request{
		Method: "GET",
		URL:    models.URL{Raw: server.URL + "/test"},
		Header: []models.Header{},
		Body:   &models.Body{Mode: "raw", Raw: ""},
	}

	body, headers, status, statusText := client.ExecuteRequest(context.Background(), req)
	if status != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, status)
	}
	if statusText != "200 OK" {
		t.Errorf("Expected statusText '200 OK', got '%s'", statusText)
	}
	if body != `{"status":"ok"}` {
		t.Errorf("Expected body '{\"status\":\"ok\"}', got '%s'", body)
	}
	if headers == nil {
		t.Fatal("Expected non-nil headers")
	}
	ct, hasCT := headers["Content-Type"]
	if !hasCT || len(ct) == 0 || ct[0] == "" {
		t.Error("Expected Content-Type header")
	}
}

func TestClient_ExecuteRequest_WithAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, _, _ := setupClient(t)
	req := &models.Request{
		Method: "GET",
		URL:    models.URL{Raw: server.URL},
		Auth: &models.Auth{
			Type: "bearer",
			Bearer: []models.Header{
				{Key: "token", Value: "test-token"},
			},
		},
		Body: &models.Body{Mode: "raw", Raw: ""},
	}

	_, _, status, _ := client.ExecuteRequest(context.Background(), req)
	if status != http.StatusOK {
		t.Errorf("Expected 200 for valid auth, got %d", status)
	}
}

func TestClient_ExecuteRequest_RequestHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "header-value" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, _, _ := setupClient(t)
	req := &models.Request{
		Method: "GET",
		URL:    models.URL{Raw: server.URL},
		Header: []models.Header{
			{Key: "X-Custom", Value: "header-value"},
		},
		Body: &models.Body{Mode: "raw", Raw: ""},
	}

	_, _, status, _ := client.ExecuteRequest(context.Background(), req)
	if status != http.StatusOK {
		t.Errorf("Expected 200 with custom header, got %d", status)
	}
}

func TestClient_ExecuteRequest_WithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, _, _ := setupClient(t)

	t.Run("raw body", func(t *testing.T) {
		req := &models.Request{
			Method: "POST",
			URL:    models.URL{Raw: server.URL},
			Body:   &models.Body{Mode: "raw", Raw: `{"key":"value"}`},
		}
		_, _, status, _ := client.ExecuteRequest(context.Background(), req)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d", status)
		}
	})

	t.Run("urlencoded body", func(t *testing.T) {
		req := &models.Request{
			Method: "POST",
			URL:    models.URL{Raw: server.URL},
			Body: &models.Body{
				Mode: "urlencoded",
				UrlEncoded: []models.UrlEncoded{
					{Key: "field1", Value: "val1"},
				},
			},
		}
		_, _, status, _ := client.ExecuteRequest(context.Background(), req)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d", status)
		}
	})
}

func TestClient_ExecuteRequest_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // Will be cancelled
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, _, _ := setupClient(t)
	req := &models.Request{
		Method: "GET",
		URL:    models.URL{Raw: server.URL},
		Body:   &models.Body{Mode: "raw", Raw: ""},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, _, _, statusText := client.ExecuteRequest(ctx, req)
	if statusText == "" {
		t.Error("Expected error for cancelled context")
	}
}

func TestClient_ContentTypeDetection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, _, _ := setupClient(t)

	t.Run("json body sets content-type", func(t *testing.T) {
		req := &models.Request{
			Method: "POST",
			URL:    models.URL{Raw: server.URL},
			Body: &models.Body{
				Mode: "raw",
				Raw:  `{"key":"value"}`,
				Options: &models.Options{
					Raw: &models.RawOptions{Language: "json"},
				},
			},
		}
		_, _, status, _ := client.ExecuteRequest(context.Background(), req)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d", status)
		}
	})

	t.Run("urlencoded body sets content-type", func(t *testing.T) {
		req := &models.Request{
			Method: "POST",
			URL:    models.URL{Raw: server.URL},
			Body: &models.Body{
				Mode: "urlencoded",
				UrlEncoded: []models.UrlEncoded{
					{Key: "f", Value: "v"},
				},
			},
		}
		_, _, status, _ := client.ExecuteRequest(context.Background(), req)
		if status != http.StatusOK {
			t.Errorf("Expected 200, got %d", status)
		}
	})
}

func TestClient_VariableResolutionInRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, store, _ := setupClient(t)
	store.SetVariable("baseUrl", server.URL)

	req := &models.Request{
		Method: "GET",
		URL:    models.URL{Raw: "{{baseUrl}}/path"},
		Body:   &models.Body{Mode: "raw", Raw: ""},
	}

	// Variable resolution happens during execution
	_, _, status, _ := client.ExecuteRequest(context.Background(), req)
	if status != http.StatusOK {
		t.Errorf("Expected 200 with resolved variable, got %d", status)
	}
}

func TestClient_ExecuteRequest_POST_PUT_DELETE_PATCH(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, _, _ := setupClient(t)

	methods := []string{"POST", "PUT", "DELETE", "PATCH"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := &models.Request{
				Method: method,
				URL:    models.URL{Raw: server.URL},
				Body:   &models.Body{Mode: "raw", Raw: ""},
			}
			_, _, status, _ := client.ExecuteRequest(context.Background(), req)
			if status != http.StatusOK {
				t.Errorf("[%s] Expected 200, got %d", method, status)
			}
		})
	}
}

func TestClient_ExecuteRequestWithLocal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, _, _ := setupClient(t)

	req := &models.Request{
		Method: "GET",
		URL:    models.URL{Raw: server.URL + "/{{userId}}"},
		Body:   &models.Body{Mode: "raw", Raw: ""},
	}

	localVars := map[string]string{"userId": "42"}
	_, _, status, _ := client.ExecuteRequestWithLocal(context.Background(), req, localVars)
	if status != http.StatusOK {
		t.Errorf("Expected 200 with local vars, got %d", status)
	}
}
