package processor

import (
	"encoding/json"
	"postit/internal/models"
	"testing"
	"time"
)

func TestExportToHAR(t *testing.T) {
	now := time.Now()

	t.Run("single record", func(t *testing.T) {
		history := []models.HistoryRecord{
			{
				Timestamp:  now,
				Path:       "Test > Req",
				Method:     "GET",
				URL:        "https://example.com/api",
				StatusCode: 200,
				StatusText: "OK",
				Duration:   150,
				ResponseBody: `{"status":"ok"}`,
				ResponseHeaders: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
		}

		data, err := ExportToHAR(history)
		if err != nil {
			t.Fatalf("ExportToHAR failed: %v", err)
		}

		var har HAR
		if err := json.Unmarshal(data, &har); err != nil {
			t.Fatalf("Failed to unmarshal HAR output: %v", err)
		}

		if har.Log.Version != "1.2" {
			t.Errorf("Expected version 1.2, got %s", har.Log.Version)
		}
		if har.Log.Creator.Name != "PostIt" {
			t.Errorf("Expected creator PostIt, got %s", har.Log.Creator.Name)
		}

		if len(har.Log.Entries) != 1 {
			t.Fatalf("Expected 1 entry, got %d", len(har.Log.Entries))
		}

		entry := har.Log.Entries[0]
		if entry.Request.Method != "GET" {
			t.Errorf("Expected method GET, got %s", entry.Request.Method)
		}
		if entry.Request.URL != "https://example.com/api" {
			t.Errorf("Expected URL, got %s", entry.Request.URL)
		}
		if entry.Response.Status != 200 {
			t.Errorf("Expected status 200, got %d", entry.Response.Status)
		}
		if entry.Response.Content.Text != `{"status":"ok"}` {
			t.Errorf("Expected body match, got %s", entry.Response.Content.Text)
		}
	})

	t.Run("multiple records", func(t *testing.T) {
		history := []models.HistoryRecord{
			{Timestamp: now, Method: "GET", URL: "https://a.com", StatusCode: 200, Duration: 100},
			{Timestamp: now, Method: "POST", URL: "https://b.com", StatusCode: 201, Duration: 200},
		}

		data, err := ExportToHAR(history)
		if err != nil {
			t.Fatalf("ExportToHAR failed: %v", err)
		}

		var har HAR
		json.Unmarshal(data, &har)

		if len(har.Log.Entries) != 2 {
			t.Fatalf("Expected 2 entries, got %d", len(har.Log.Entries))
		}
	})

	t.Run("empty history", func(t *testing.T) {
		data, err := ExportToHAR([]models.HistoryRecord{})
		if err != nil {
			t.Fatalf("ExportToHAR failed: %v", err)
		}

		var har HAR
		json.Unmarshal(data, &har)

		if len(har.Log.Entries) != 0 {
			t.Errorf("Expected 0 entries, got %d", len(har.Log.Entries))
		}
	})

	t.Run("response headers mapped correctly", func(t *testing.T) {
		history := []models.HistoryRecord{
			{
				Method:     "GET",
				URL:        "https://example.com",
				StatusCode: 200,
				ResponseHeaders: map[string][]string{
					"Content-Type": {"application/json"},
					"X-Custom":     {"value1", "value2"},
				},
			},
		}

		data, err := ExportToHAR(history)
		if err != nil {
			t.Fatalf("ExportToHAR failed: %v", err)
		}

		var har HAR
		json.Unmarshal(data, &har)

		headers := har.Log.Entries[0].Response.Headers
		if len(headers) != 3 {
			t.Fatalf("Expected 3 header entries, got %d", len(headers))
		}

		foundXCustom := false
		foundValue1 := false
		foundValue2 := false
		for _, h := range headers {
			if h.Name == "X-Custom" && h.Value == "value1" {
				foundValue1 = true
			}
			if h.Name == "X-Custom" && h.Value == "value2" {
				foundValue2 = true
			}
			if h.Name == "Content-Type" {
				foundXCustom = true
			}
		}
		if !foundXCustom {
			t.Error("Expected Content-Type header")
		}
		if !foundValue1 || !foundValue2 {
			t.Error("Expected both X-Custom header values")
		}
	})

	t.Run("timing mapping", func(t *testing.T) {
		history := []models.HistoryRecord{
			{
				Method:     "GET",
				URL:        "https://example.com",
				StatusCode: 200,
				Duration:   500,
			},
		}

		data, err := ExportToHAR(history)
		if err != nil {
			t.Fatalf("ExportToHAR failed: %v", err)
		}

		var har HAR
		json.Unmarshal(data, &har)

		entry := har.Log.Entries[0]
		if entry.Time != 500 {
			t.Errorf("Expected Time 500, got %d", entry.Time)
		}
		if entry.Timings.Wait != 500 {
			t.Errorf("Expected Wait 500, got %d", entry.Timings.Wait)
		}
	})
}
