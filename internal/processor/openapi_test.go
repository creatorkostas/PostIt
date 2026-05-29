package processor

import (
	"testing"
)

func TestParseOpenAPI(t *testing.T) {
	t.Run("basic spec", func(t *testing.T) {
		data := []byte(`{
			"openapi": "3.0.0",
			"info": { "title": "Pet Store", "version": "1.0" },
			"paths": {
				"/pets": {
					"get": {
						"summary": "List pets",
						"operationId": "listPets"
					}
				}
			}
		}`)

		requests, err := ParseOpenAPI(data)
		if err != nil {
			t.Fatalf("ParseOpenAPI failed: %v", err)
		}
		if len(requests) != 1 {
			t.Fatalf("Expected 1 request, got %d", len(requests))
		}
		if requests[0].Path != "Pet Store > List pets" {
			t.Errorf("Expected path 'Pet Store > List pets', got '%s'", requests[0].Path)
		}
		if requests[0].Request.Method != "GET" {
			t.Errorf("Expected method GET, got %s", requests[0].Request.Method)
		}
		if requests[0].Request.URL.Raw != "/pets" {
			t.Errorf("Expected URL /pets, got %s", requests[0].Request.URL.Raw)
		}
	})

	t.Run("multiple paths and methods", func(t *testing.T) {
		data := []byte(`{
			"openapi": "3.0.0",
			"info": { "title": "API", "version": "1.0" },
			"paths": {
				"/users": {
					"get": { "summary": "List users" },
					"post": { "summary": "Create user" }
				},
				"/users/{id}": {
					"get": { "summary": "Get user" }
				}
			}
		}`)

		requests, err := ParseOpenAPI(data)
		if err != nil {
			t.Fatalf("ParseOpenAPI failed: %v", err)
		}
		if len(requests) != 3 {
			t.Fatalf("Expected 3 requests, got %d", len(requests))
		}

		// Build lookup maps since map iteration order is non-deterministic
		pathByMethod := make(map[string]string)
		for _, r := range requests {
			pathByMethod[r.Request.Method+" "+r.Request.URL.Raw] = r.Path
		}
		tests := []struct {
			key      string
			wantPath string
		}{
			{"GET /users", "API > List users"},
			{"POST /users", "API > Create user"},
			{"GET /users/{id}", "API > Get user"},
		}
		for _, tc := range tests {
			got, ok := pathByMethod[tc.key]
			if !ok {
				t.Errorf("Missing request for %s", tc.key)
				continue
			}
			if got != tc.wantPath {
				t.Errorf("For %s: expected path '%s', got '%s'", tc.key, tc.wantPath, got)
			}
		}
	})

	t.Run("no summary - falls back to method+path", func(t *testing.T) {
		data := []byte(`{
			"openapi": "3.0.0",
			"info": { "title": "API", "version": "1.0" },
			"paths": {
				"/items": {
					"get": {}
				}
			}
		}`)

		requests, err := ParseOpenAPI(data)
		if err != nil {
			t.Fatalf("ParseOpenAPI failed: %v", err)
		}
		if len(requests) != 1 {
			t.Fatalf("Expected 1 request, got %d", len(requests))
		}
		if requests[0].Path != "API > GET /items" {
			t.Errorf("Expected path 'API > GET /items', got '%s'", requests[0].Path)
		}
	})

	t.Run("with request body adds Content-Type", func(t *testing.T) {
		data := []byte(`{
			"openapi": "3.0.0",
			"info": { "title": "Test", "version": "1.0" },
			"paths": {
				"/submit": {
					"post": {
						"summary": "Submit",
						"requestBody": {
							"content": {
								"application/json": {
									"schema": { "type": "object" }
								}
							}
						}
					}
				}
			}
		}`)

		requests, err := ParseOpenAPI(data)
		if err != nil {
			t.Fatalf("ParseOpenAPI failed: %v", err)
		}
		if len(requests) != 1 {
			t.Fatalf("Expected 1 request, got %d", len(requests))
		}
		if requests[0].Request.Body == nil {
			t.Fatal("Expected non-nil Body for request with requestBody")
		}
		if requests[0].Request.Body.Mode != "raw" {
			t.Errorf("Expected body mode 'raw', got '%s'", requests[0].Request.Body.Mode)
		}

		foundContentType := false
		for _, h := range requests[0].Request.Header {
			if h.Key == "Content-Type" && h.Value == "application/json" {
				foundContentType = true
				break
			}
		}
		if !foundContentType {
			t.Error("Expected Content-Type header application/json")
		}
	})

	t.Run("no info title - uses default", func(t *testing.T) {
		data := []byte(`{
			"openapi": "3.0.0",
			"info": { "version": "1.0" },
			"paths": {
				"/test": {
					"get": { "summary": "Test" }
				}
			}
		}`)

		requests, err := ParseOpenAPI(data)
		if err != nil {
			t.Fatalf("ParseOpenAPI failed: %v", err)
		}
		if len(requests) != 1 {
			t.Fatalf("Expected 1 request, got %d", len(requests))
		}
		if requests[0].Path != "Imported OpenAPI > Test" {
			t.Errorf("Expected path 'Imported OpenAPI > Test', got '%s'", requests[0].Path)
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, err := ParseOpenAPI([]byte(`{invalid}`))
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})

	t.Run("empty paths returns empty", func(t *testing.T) {
		data := []byte(`{
			"openapi": "3.0.0",
			"info": { "title": "Empty", "version": "1.0" },
			"paths": {}
		}`)

		requests, err := ParseOpenAPI(data)
		if err != nil {
			t.Fatalf("ParseOpenAPI failed: %v", err)
		}
		if len(requests) != 0 {
			t.Errorf("Expected 0 requests, got %d", len(requests))
		}
	})
}

func TestParseOpenAPI_RequestBodyContentType(t *testing.T) {
	// Ensure the Content-Type header is only added once (break only on first content type)
	data := []byte(`{
		"openapi": "3.0.0",
		"info": { "title": "Test", "version": "1.0" },
		"paths": {
			"/upload": {
				"post": {
					"summary": "Upload",
					"requestBody": {
						"content": {
							"application/json": { "schema": { "type": "object" } },
							"application/xml": { "schema": { "type": "object" } }
						}
					}
				}
			}
		}
	}`)

	requests, err := ParseOpenAPI(data)
	if err != nil {
		t.Fatalf("ParseOpenAPI failed: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(requests))
	}

	count := 0
	for _, h := range requests[0].Request.Header {
		if h.Key == "Content-Type" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Expected exactly 1 Content-Type header, got %d", count)
	}
}
