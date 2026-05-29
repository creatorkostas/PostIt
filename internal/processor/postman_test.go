package processor

import (
	"postit/internal/models"
	"testing"
)

func TestParsePostmanCollection(t *testing.T) {
	t.Run("basic collection", func(t *testing.T) {
		data := []byte(`{
			"info": {
				"_postman_id": "abc123",
				"name": "Test Collection",
				"schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
			},
			"item": [{
				"name": "Get Users",
				"request": {
					"method": "GET",
					"url": { "raw": "https://api.example.com/users" }
				}
			}]
		}`)

		col, requests, err := ParsePostmanCollection(data)
		if err != nil {
			t.Fatalf("ParsePostmanCollection failed: %v", err)
		}
		if col.Info.Name != "Test Collection" {
			t.Errorf("Expected collection name 'Test Collection', got '%s'", col.Info.Name)
		}
		if len(requests) != 1 {
			t.Fatalf("Expected 1 request, got %d", len(requests))
		}
		if requests[0].Path != "Test Collection > Get Users" {
			t.Errorf("Expected path 'Test Collection > Get Users', got '%s'", requests[0].Path)
		}
		if requests[0].Request.Method != "GET" {
			t.Errorf("Expected method GET, got '%s'", requests[0].Request.Method)
		}
	})

	t.Run("nested folders", func(t *testing.T) {
		data := []byte(`{
			"info": { "name": "API", "schema": "" },
			"item": [{
				"name": "Users",
				"item": [{
					"name": "Create User",
					"request": {
						"method": "POST",
						"url": { "raw": "https://api.example.com/users" }
					}
				}]
			}]
		}`)

		_, requests, err := ParsePostmanCollection(data)
		if err != nil {
			t.Fatalf("ParsePostmanCollection failed: %v", err)
		}
		if len(requests) != 1 {
			t.Fatalf("Expected 1 request, got %d", len(requests))
		}
		if requests[0].Path != "API > Users > Create User" {
			t.Errorf("Expected path 'API > Users > Create User', got '%s'", requests[0].Path)
		}
	})

	t.Run("item with events", func(t *testing.T) {
		data := []byte(`{
			"info": { "name": "Events Test", "schema": "" },
			"item": [{
				"name": "Test Req",
				"event": [{
					"listen": "test",
					"script": {
						"exec": ["pm.test('status', function() {", "  pm.response.to.have.status(200);", "});"],
						"type": "text/javascript"
					}
				}],
				"request": {
					"method": "GET",
					"url": { "raw": "https://example.com" }
				}
			}]
		}`)

		_, requests, err := ParsePostmanCollection(data)
		if err != nil {
			t.Fatalf("ParsePostmanCollection failed: %v", err)
		}
		if len(requests) != 1 {
			t.Fatalf("Expected 1 request, got %d", len(requests))
		}
		if len(requests[0].Events) != 1 {
			t.Fatalf("Expected 1 event, got %d", len(requests[0].Events))
		}
		if requests[0].Events[0].Listen != "test" {
			t.Errorf("Expected event listen 'test', got '%s'", requests[0].Events[0].Listen)
		}
		if len(requests[0].Events[0].Script.Exec) != 3 {
			t.Errorf("Expected 3 script lines, got %d", len(requests[0].Events[0].Script.Exec))
		}
	})

	t.Run("empty collection returns no requests", func(t *testing.T) {
		data := []byte(`{
			"info": { "name": "Empty", "schema": "" },
			"item": []
		}`)

		_, requests, err := ParsePostmanCollection(data)
		if err != nil {
			t.Fatalf("ParsePostmanCollection failed: %v", err)
		}
		if len(requests) != 0 {
			t.Errorf("Expected 0 requests, got %d", len(requests))
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, _, err := ParsePostmanCollection([]byte(`{invalid}`))
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})

	t.Run("folder with multiple items maintains order", func(t *testing.T) {
		data := []byte(`{
			"info": { "name": "Ordered", "schema": "" },
			"item": [{
				"name": "Folder",
				"item": [
					{
						"name": "Second",
						"request": { "method": "GET", "url": { "raw": "https://a.com" } }
					},
					{
						"name": "First",
						"request": { "method": "GET", "url": { "raw": "https://b.com" } }
					}
				]
			}]
		}`)

		_, requests, err := ParsePostmanCollection(data)
		if err != nil {
			t.Fatalf("ParsePostmanCollection failed: %v", err)
		}
		if len(requests) != 2 {
			t.Fatalf("Expected 2 requests, got %d", len(requests))
		}
		// First item in array should get order 0, second gets order 1
		// The name is derived from the last part of the Path (collection prefix + folder + item name)
		expectedPath0 := "Ordered > Folder > Second"
		expectedPath1 := "Ordered > Folder > First"
		if requests[0].Path != expectedPath0 || requests[0].Order != 0 {
			t.Errorf("Expected path '%s' with order 0, got '%s' order %d", expectedPath0, requests[0].Path, requests[0].Order)
		}
		if requests[1].Path != expectedPath1 || requests[1].Order != 1 {
			t.Errorf("Expected path '%s' with order 1, got '%s' order %d", expectedPath1, requests[1].Path, requests[1].Order)
		}
	})
}

func TestFlattenItems(t *testing.T) {
	t.Run("inherits parent events", func(t *testing.T) {
		parentEvents := []models.Event{
			{Listen: "prerequest", Script: models.Script{Exec: []string{"console.log('pre')"}}},
		}

		items := []models.Item{
			{
				Name:  "Child Req",
				Request: &models.Request{Method: "GET", URL: models.URL{Raw: "https://example.com"}},
				Event: []models.Event{
					{Listen: "test", Script: models.Script{Exec: []string{"console.log('test')"}}},
				},
			},
		}

		var result []models.RequestInfo
		counter := 0
		flattenItems(items, "Parent", parentEvents, &result, &counter)

		if len(result) != 1 {
			t.Fatalf("Expected 1 request, got %d", len(result))
		}
		if len(result[0].Events) != 2 {
			t.Fatalf("Expected 2 events (1 inherited + 1 own), got %d", len(result[0].Events))
		}
	})

	t.Run("folder without request produces no request info", func(t *testing.T) {
		items := []models.Item{
			{Name: "Empty Folder", Item: []models.Item{}},
		}

		var result []models.RequestInfo
		counter := 0
		flattenItems(items, "", []models.Event{}, &result, &counter)

		if len(result) != 0 {
			t.Errorf("Expected 0 requests for folder without requests, got %d", len(result))
		}
	})
}
