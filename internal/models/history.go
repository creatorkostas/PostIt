package models

import "time"

type HistoryRecord struct {
	Timestamp  time.Time `json:"timestamp"`
	Path       string    `json:"path"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	StatusCode int       `json:"statusCode"`
	StatusText string    `json:"statusText"`
	Duration   int64     `json:"duration"` // in milliseconds
}
