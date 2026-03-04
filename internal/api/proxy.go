package api

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"postit/internal/models"
	"postit/internal/storage"
	"strings"
	"time"
)

type ProxyServer struct {
	Storage *storage.Manager
	Server  *http.Server
	Running bool
}

func NewProxyServer(store *storage.Manager) *ProxyServer {
	return &ProxyServer{Storage: store}
}

func (p *ProxyServer) Start(port int) error {
	if p.Running {
		return fmt.Errorf("Proxy is already running")
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Capture Request Data
		bodyBytes, _ := ioutil.ReadAll(r.Body)
		r.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes)) // Reset body for forwarding

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
		proxyReq, err := http.NewRequest(r.Method, capturedReq.URL.Raw, bytes.NewReader(bodyBytes))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Copy headers to proxy request
		for _, h := range capturedReq.Header {
			proxyReq.Header.Set(h.Key, h.Value)
		}

		client := &http.Client{}
		resp, err := client.Do(proxyReq)
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
		respBody, _ := ioutil.ReadAll(resp.Body)
		w.Write(respBody)
	})

	p.Server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: handler,
	}

	p.Running = true
	go func() {
		if err := p.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			p.Running = false
		}
	}()

	return nil
}

func (p *ProxyServer) Stop() error {
	if !p.Running || p.Server == nil {
		return nil
	}
	err := p.Server.Close()
	p.Running = false
	return err
}
