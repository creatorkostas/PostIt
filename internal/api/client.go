package api

import (
	"container/list"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/go-resty/resty/v2"
)

const (
	dbMaxPoolSize     = 50
	dbConnMaxAge      = time.Hour
	dbCleanupInterval = 5 * time.Minute
)

type dbConnEntry struct {
	db        *sql.DB
	lastUsed  time.Time
	createdAt time.Time
}

type Client struct {
	Storage      *storage.Manager
	Processor    *processor.ScriptProcessor
	Logger       *log.Logger
	dbPool       map[string]*dbConnEntry
	dbMu         sync.Mutex
	dbOrder      *list.List // LRU tracking
	dbStopCleanup chan struct{}
	closeOnce    sync.Once
}

func NewClient(store *storage.Manager, proc *processor.ScriptProcessor) *Client {
	c := &Client{
		Storage:       store,
		Processor:     proc,
		Logger:        log.Default(),
		dbPool:        make(map[string]*dbConnEntry),
		dbOrder:       list.New(),
		dbStopCleanup: make(chan struct{}),
	}
	// Start background cleanup goroutine
	go c.dbCleanupWorker()
	return c
}

// dbCleanupWorker periodically removes old connections
func (c *Client) dbCleanupWorker() {
	ticker := time.NewTicker(dbCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanupOldConnections()
		case <-c.dbStopCleanup:
			return
		}
	}
}

// cleanupOldConnections removes connections older than max age
func (c *Client) cleanupOldConnections() {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()

	now := time.Now()
	for connStr, entry := range c.dbPool {
		if now.Sub(entry.createdAt) > dbConnMaxAge {
			entry.db.Close()
			delete(c.dbPool, connStr)
			// Remove from LRU list
			for e := c.dbOrder.Front(); e != nil; e = e.Next() {
				if e.Value == connStr {
					c.dbOrder.Remove(e)
					break
				}
			}
		}
	}
}

func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		// Signal cleanup worker to stop
		close(c.dbStopCleanup)

		c.dbMu.Lock()
		defer c.dbMu.Unlock()
		for _, entry := range c.dbPool {
			entry.db.Close()
		}
		c.dbPool = make(map[string]*dbConnEntry)
		c.dbOrder.Init()
	})
	return err
}

func (c *Client) ExecuteRequest(ctx context.Context, req *models.Request) (string, map[string][]string, int, string) {
	return c.ExecuteRequestWithLocal(ctx, req, nil)
}

func (c *Client) ExecuteRequestWithLocal(ctx context.Context, req *models.Request, localVars map[string]string) (string, map[string][]string, int, string) {
	client := resty.New()
	url := c.Processor.ResolveVariablesWithLocal(req.URL.Raw, localVars)
	method := strings.ToUpper(req.Method)
	r := client.R().SetContext(ctx)

	contentType := ""
	if req.Body != nil {
		if req.Body.Mode == "urlencoded" {
			contentType = "application/x-www-form-urlencoded"
		} else if req.Body.Mode == "raw" && req.Body.Options != nil && req.Body.Options.Raw != nil {
			if req.Body.Options.Raw.Language == "json" {
				contentType = "application/json"
			}
		}
	}

	for _, h := range c.Storage.GlobalHeaders {
		r.SetHeader(h.Key, c.Processor.ResolveVariablesWithLocal(h.Value, localVars))
	}
	for _, h := range req.Header {
		r.SetHeader(h.Key, c.Processor.ResolveVariablesWithLocal(h.Value, localVars))
	}
	if contentType != "" && r.Header.Get("Content-Type") == "" {
		r.SetHeader("Content-Type", contentType)
	}

	if req.Body != nil {
		if req.Body.Mode == "raw" {
			r.SetBody(c.Processor.ResolveVariablesWithLocal(req.Body.Raw, localVars))
		} else if req.Body.Mode == "urlencoded" {
			formData := make(map[string]string)
			for _, f := range req.Body.UrlEncoded {
				formData[c.Processor.ResolveVariablesWithLocal(f.Key, localVars)] = c.Processor.ResolveVariablesWithLocal(f.Value, localVars)
			}
			r.SetFormData(formData)
		}
	}

	if req.Auth != nil && req.Auth.Type == "bearer" {
		for _, b := range req.Auth.Bearer {
			if b.Key == "token" {
				r.SetAuthToken(c.Processor.ResolveVariablesWithLocal(b.Value, localVars))
			}
		}
	}

	c.Logger.Info("Sending Request", "method", method, "url", url)
	var resp *resty.Response
	var err error

	switch method {
	case "GET": resp, err = r.Get(url)
	case "POST": resp, err = r.Post(url)
	case "PUT": resp, err = r.Put(url)
	case "DELETE": resp, err = r.Delete(url)
	case "PATCH": resp, err = r.Patch(url)
	default:
		return "", nil, 0, ""
	}

	if err != nil {
		c.Logger.Error("Request Failed", "error", err)
		return "", nil, 0, fmt.Sprintf("Error: %v", err)
	}

	c.Logger.Info("Response Received", "status", resp.Status(), "time", resp.Time())
	body := string(resp.Body())
	if body != "" {
		var prettyJSON interface{}
		if err := json.Unmarshal(resp.Body(), &prettyJSON); err == nil {
			formatted, _ := json.MarshalIndent(prettyJSON, "", "  ")
			c.Logger.Debug("Response Body", "content", string(formatted))
		} else {
			c.Logger.Debug("Response Body", "content", body)
		}
	}
	return body, resp.Header(), resp.StatusCode(), resp.Status()
}
