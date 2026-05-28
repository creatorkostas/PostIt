package api

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"postit/internal/models"
	"postit/internal/storage"
	"strings"
	"sync"
	"time"
)

// blockedHosts contains hostnames that should not be proxied (SSRF protection)
var blockedHosts = []string{
	"localhost",
	"127.0.0.1",
	"::1",
	"0.0.0.0",
	"169.254.169.254", // AWS metadata
}

// isPrivateIP checks if an IP address is in a private range
func isPrivateIP(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

// isURLAllowed validates if a URL should be allowed (SSRF protection)
func isURLAllowed(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	host := u.Hostname()

	// Check blocked hosts
	for _, blocked := range blockedHosts {
		if strings.EqualFold(host, blocked) {
			return fmt.Errorf("URL host '%s' is not allowed", host)
		}
	}

	// Check for private IP ranges
	if isPrivateIP(host) {
		return fmt.Errorf("URL resolves to private IP '%s' which is not allowed", host)
	}

	// Only allow HTTP/HTTPS
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme '%s' is not allowed", u.Scheme)
	}

	return nil
}

type ProxyServer struct {
	Storage *storage.Manager
	Server  *http.Server
	Running bool
	mu      sync.RWMutex
	client  *http.Client
}

func NewProxyServer(store *storage.Manager) *ProxyServer {
	return &ProxyServer{
		Storage: store,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (p *ProxyServer) Start(port int) error {
	p.mu.Lock()
	if p.Running {
		p.mu.Unlock()
		return fmt.Errorf("Proxy is already running")
	}
	p.Running = true
	p.mu.Unlock()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Capture Request Data
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Reset body for forwarding

		// 2. Create PostIt Request Model
		capturedReq := &models.Request{
			Method: r.Method,
			URL:    models.URL{Raw: r.URL.String()},
			Header: []models.Header{},
			Body: &models.Body{
				Mode: "raw",
				Raw:  string(bodyBytes),
			},
		}

		// Clean up URL for model (r.URL might be relative if used as standard proxy)
		if r.URL.Host == "" {
			capturedReq.URL.Raw = r.Host + r.URL.String()
			if !strings.HasPrefix(capturedReq.URL.Raw, "http") {
				capturedReq.URL.Raw = "http://" + capturedReq.URL.Raw
			}
		}

		for k, vv := range r.Header {
			if k == "Proxy-Connection" || k == "Connection" {
				continue
			}
			capturedReq.Header = append(capturedReq.Header, models.Header{
				Key:   k,
				Value: strings.Join(vv, ", "),
			})
		}

		// 3. Save to Storage
		go func() {
			path := fmt.Sprintf("Intercepted > %s > %s %s", 
				time.Now().Format("2006-01-02"), 
				r.Method, 
				time.Now().Format("15:04:05"))
			
			reqInfo := models.RequestInfo{
				Path:    path,
				Request: capturedReq,
			}
			p.Storage.SaveSingleRequest(reqInfo)
		}()

		// 4. Forward the request to the real destination
		// SSRF protection: validate the URL before forwarding
		if err := isURLAllowed(capturedReq.URL.Raw); err != nil {
			http.Error(w, fmt.Sprintf("SSRF protection: %v", err), http.StatusForbidden)
			return
		}

		proxyReq, err := http.NewRequest(r.Method, capturedReq.URL.Raw, bytes.NewReader(bodyBytes))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Copy headers to proxy request
		for _, h := range capturedReq.Header {
			proxyReq.Header.Set(h.Key, h.Value)
		}

		resp, err := p.client.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// 5. Return response to the client
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return
		}
		w.Write(respBody)
	})

	p.mu.Lock()
	p.Server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: handler,
	}
	p.mu.Unlock()

	go func() {
		if err := p.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			p.mu.Lock()
			p.Running = false
			p.mu.Unlock()
		}
	}()

	return nil
}

func (p *ProxyServer) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.Running || p.Server == nil {
		return nil
	}
	err := p.Server.Close()
	p.Running = false
	return err
}

func (p *ProxyServer) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Running
}
