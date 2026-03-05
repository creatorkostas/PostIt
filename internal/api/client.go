package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"sync"

	"github.com/go-resty/resty/v2"
)

type Client struct {
	Storage   *storage.Manager
	Processor *processor.ScriptProcessor
	dbPool    map[string]*sql.DB
	dbMu      sync.Mutex
}

func NewClient(store *storage.Manager, proc *processor.ScriptProcessor) *Client {
	return &Client{
		Storage:   store, 
		Processor: proc,
		dbPool:    make(map[string]*sql.DB),
	}
}

func (c *Client) Close() error {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()
	for _, db := range c.dbPool {
		db.Close()
	}
	return nil
}

func (c *Client) ExecuteRequest(ctx context.Context, req *models.Request) (string, map[string][]string, int, string) {
	client := resty.New()
	url := c.Processor.ResolveVariables(req.URL.Raw)
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
		r.SetHeader(h.Key, c.Processor.ResolveVariables(h.Value))
	}
	for _, h := range req.Header {
		r.SetHeader(h.Key, c.Processor.ResolveVariables(h.Value))
	}
	if contentType != "" && r.Header.Get("Content-Type") == "" {
		r.SetHeader("Content-Type", contentType)
	}

	if req.Body != nil {
		if req.Body.Mode == "raw" {
			r.SetBody(c.Processor.ResolveVariables(req.Body.Raw))
		} else if req.Body.Mode == "urlencoded" {
			formData := make(map[string]string)
			for _, f := range req.Body.UrlEncoded {
				formData[c.Processor.ResolveVariables(f.Key)] = c.Processor.ResolveVariables(f.Value)
			}
			r.SetFormData(formData)
		}
	}

	if req.Auth != nil && req.Auth.Type == "bearer" {
		for _, b := range req.Auth.Bearer {
			if b.Key == "token" {
				r.SetAuthToken(c.Processor.ResolveVariables(b.Value))
			}
		}
	}

	fmt.Printf("\nSending %s %s...\n", method, url)
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
		fmt.Printf("Error: %v\n", err)
		return "", nil, 0, fmt.Sprintf("Error: %v", err)
	}

	fmt.Printf("Status: %s (%v)\n", resp.Status(), resp.Time())
	body := string(resp.Body())
	if body != "" {
		var prettyJSON interface{}
		if err := json.Unmarshal(resp.Body(), &prettyJSON); err == nil {
			formatted, _ := json.MarshalIndent(prettyJSON, "", "  ")
			fmt.Println(string(formatted))
		} else {
			fmt.Println(body)
		}
	}
	return body, resp.Header(), resp.StatusCode(), resp.Status()
}
