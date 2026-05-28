package processor

import (
	"encoding/json"
	"postit/internal/models"
	"time"
)

type HAR struct {
	Log HARLog `json:"log"`
}

type HARLog struct {
	Version string     `json:"version"`
	Creator HARCreator `json:"creator"`
	Entries []HAREntry `json:"entries"`
}

type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type HAREntry struct {
	StartedDateTime time.Time   `json:"startedDateTime"`
	Time            int64       `json:"time"`
	Request         HARRequest  `json:"request"`
	Response        HARResponse `json:"response"`
	Cache           interface{} `json:"cache"`
	Timings         HARTimings  `json:"timings"`
}

type HARRequest struct {
	Method      string      `json:"method"`
	URL         string      `json:"url"`
	HTTPVersion string      `json:"httpVersion"`
	Headers     []HARHeader `json:"headers"`
	QueryString []interface{} `json:"queryString"`
	Cookies     []interface{} `json:"cookies"`
	HeadersSize int         `json:"headersSize"`
	BodySize    int         `json:"bodySize"`
}

type HARResponse struct {
	Status      int         `json:"status"`
	StatusText  string      `json:"statusText"`
	HTTPVersion string      `json:"httpVersion"`
	Headers     []HARHeader `json:"headers"`
	Cookies     []interface{} `json:"cookies"`
	Content     HARContent  `json:"content"`
	RedirectURL string      `json:"redirectURL"`
	HeadersSize int         `json:"headersSize"`
	BodySize    int         `json:"bodySize"`
}

type HARHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HARContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

type HARTimings struct {
	Send    int `json:"send"`
	Wait    int `json:"wait"`
	Receive int `json:"receive"`
}

func ExportToHAR(history []models.HistoryRecord) ([]byte, error) {
	entries := make([]HAREntry, len(history))
	for i, h := range history {
		headers := []HARHeader{}
		for k, v := range h.ResponseHeaders {
			for _, val := range v {
				headers = append(headers, HARHeader{Name: k, Value: val})
			}
		}

		entries[i] = HAREntry{
			StartedDateTime: h.Timestamp,
			Time:            h.Duration,
			Request: HARRequest{
				Method:      h.Method,
				URL:         h.URL,
				HTTPVersion: "HTTP/1.1",
				Headers:     []HARHeader{}, // We don't save request headers in history yet
			},
			Response: HARResponse{
				Status:     h.StatusCode,
				StatusText: h.StatusText,
				Headers:    headers,
				Content: HARContent{
					Size:     len(h.ResponseBody),
					MimeType: "application/json",
					Text:     h.ResponseBody,
				},
			},
			Timings: HARTimings{
				Wait: int(h.Duration),
			},
		}
	}

	har := HAR{
		Log: HARLog{
			Version: "1.2",
			Creator: HARCreator{Name: "PostIt", Version: "1.0"},
			Entries: entries,
		},
	}

	return json.MarshalIndent(har, "", "  ")
}
