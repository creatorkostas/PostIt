package processor

import (
	"testing"
)

func TestParseCurl(t *testing.T) {
	tests := []struct {
		name     string
		curl     string
		expectedMethod string
		expectedURL    string
		expectedHeader string
	}{
		{
			"Basic GET",
			"curl https://google.com",
			"GET",
			"https://google.com",
			"",
		},
		{
			"POST with Headers and Body",
			`curl -X POST "https://api.test.com/v1/users" -H "Content-Type: application/json" -H "Authorization: Bearer mytoken" -d '{"name": "test user"}'`,
			"POST",
			"https://api.test.com/v1/users",
			"Content-Type",
		},
		{
			"CURL with Basic Auth",
			`curl -u user:pass https://example.com`,
			"GET",
			"https://example.com",
			"",
		},
		{
			"CURL with Multi-line and flags",
			"curl -X PUT https://api.io/data \\\n -H 'Accept: */*' \\\n -d 'id=123'",
			"PUT",
			"https://api.io/data",
			"Accept",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ParseCurl(tt.curl)
			if req == nil {
				t.Fatalf("ParseCurl returned nil for %s", tt.name)
			}
			if req.Method != tt.expectedMethod {
				t.Errorf("Expected method %s, got %s", tt.expectedMethod, req.Method)
			}
			if req.URL.Raw != tt.expectedURL {
				t.Errorf("Expected URL %s, got %s", tt.expectedURL, req.URL.Raw)
			}
			if tt.expectedHeader != "" {
				found := false
				for _, h := range req.Header {
					if h.Key == tt.expectedHeader {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected header %s not found", tt.expectedHeader)
				}
			}
		})
	}
}
