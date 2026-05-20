package api

import (
	"bytes"
	"context"
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

	clog "github.com/charmbracelet/log"
)

// blockedHosts contains hostnames that should not be proxied (SSRF protection)
var blockedHosts = []string{
	"localhost",
	"127.0.0.1",
	"::1",
	"0.0.0.0",
	"169.254.169.254", // AWS metadata
}

// hopByHopHeaders are headers that must not be forwarded per RFC 7230 §6.1
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"TE":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Proxy-Connection":    true,
}

// isPrivateIP checks if an IP address is in a private range
func isPrivateIP(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

// isURLAllowed validates if a URL should be allowed (SSRF protection).
// It resolves DNS for domain names to verify the resolved IP is not private.
func isURLAllowed(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	// Fast-path: check blocked hostnames
	for _, blocked := range blockedHosts {
		if strings.EqualFold(host, blocked) {
			return fmt.Errorf("URL host '%s' is not allowed", host)
		}
	}

	// Only allow HTTP/HTTPS
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme '%s' is not allowed", u.Scheme)
	}

	// Fast-path: if it's already an IP address, check directly
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(host) {
			return fmt.Errorf("URL resolves to private IP '%s' which is not allowed", host)
		}
		return nil
	}

	// Resolve DNS with timeout to check all resolved IPs
	var resolver net.Resolver
	resolveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := resolver.LookupHost(resolveCtx, host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed for '%s': %v", host, err)
	}

	for _, ipStr := range ips {
		if isPrivateIP(ipStr) {
			return fmt.Errorf("URL resolves to private IP '%s' which is not allowed", ipStr)
		}
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

		// Collect extension hop-by-hop headers from the Connection header
		extHopByHop := map[string]bool{}
		if connHeader := r.Header.Get("Connection"); connHeader != "" {
			for _, ext := range strings.Split(connHeader, ",") {
				ext = strings.TrimSpace(ext)
				if ext != "" {
					extHopByHop[ext] = true
				}
			}
		}

		for k, vv := range r.Header {
			// Skip hop-by-hop headers (RFC 7230 §6.1)
			if hopByHopHeaders[k] || extHopByHop[k] {
				continue
			}
			capturedReq.Header = append(capturedReq.Header, models.Header{
				Key:   k,
				Value: strings.Join(vv, ", "),
			})
		}

		// 3. Save to Storage (synchronous with panic recovery)
		func() {
			defer func() {
				if r := recover(); r != nil {
					clog.Error("panic saving intercepted request", "recover", r)
				}
			}()
			path := fmt.Sprintf("Intercepted > %s > %s %s", 
				time.Now().Format("2006-01-02"), 
				r.Method, 
				time.Now().Format("15:04:05"))
			
			reqInfo := models.RequestInfo{
				Path:    path,
				Request: capturedReq,
			}
			if err := p.Storage.SaveSingleRequest(reqInfo); err != nil {
				clog.Error("failed to save intercepted request", "error", err)
			}
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
