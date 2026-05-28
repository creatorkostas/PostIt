package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"sync"
	"time"
)

//go:embed static/*
var staticAssets embed.FS

type Server struct {
	Storage    *storage.Manager
	Processor  *processor.ScriptProcessor
	Client     *api.Client
	Proxy      *api.ProxyServer
	Collection models.Collection
	FlatList   []models.RequestInfo
	EnableMock bool
	
	// Mock Tracking
	mockStats map[string]*models.MockStat
	mockMu    sync.RWMutex
	fuzzer    *api.Fuzzer
	runner    *api.Runner

	// WebSocket
	WSClient *api.WSClient

	// Kafka
	Kafka *api.KafkaProducer

	stateMu  sync.RWMutex
}

func NewServer(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client, col models.Collection, flat []models.RequestInfo, enableMock bool) *Server {
	proc.EnablePrompts = false // Disable interactive prompts in web mode
	return &Server{
		Storage:    store,
		Processor:  proc,
		Client:     client,
		Proxy:      api.NewProxyServer(store),
		Collection: col,
		FlatList:   flat,
		EnableMock: enableMock,
		mockStats:  make(map[string]*models.MockStat),
		WSClient:   api.NewWSClient(),
		Kafka:      api.NewKafkaProducer(),
		fuzzer:     api.NewFuzzer(),
		runner:     api.NewRunner(client, store, proc),
	}
}

func (s *Server) recoveryMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("Panic recovered: %v", rec)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Internal server error: %v", rec)})
			}
		}()
		next(w, r)
	}
}

func (s *Server) csrfMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodOptions {
			origin := r.Header.Get("Origin")
			referer := r.Header.Get("Referer")
			
			// Strict protection: Must have either Origin or Referer, and it must match Host
			host := r.Host
			// Remove port if present in host for comparison
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}

			valid := false
			
			if origin != "" {
				if u, err := url.Parse(origin); err == nil {
					uHost := u.Hostname()
					if uHost == "localhost" || uHost == "127.0.0.1" || uHost == host {
						valid = true
					}
				}
			}
			
			// Check Referer if Origin is not present or not sufficient
			if !valid && referer != "" {
				if u, err := url.Parse(referer); err == nil {
					uHost := u.Hostname()
					if uHost == "localhost" || uHost == "127.0.0.1" || uHost == host {
						valid = true
					}
				}
			}
			
			if !valid {
				http.Error(w, "CSRF Check Failed: Missing or invalid Origin/Referer headers.", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) Start(ctx context.Context, port int) error {
	// Body size limit middleware - 10MB default, 50MB for imports
	withBodyLimit := func(next http.HandlerFunc, maxBytes int64) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next(w, r)
		}
	}

	http.HandleFunc("/api/requests", s.recoveryMiddleware(s.csrfMiddleware(s.handleGetRequests)))
	http.HandleFunc("/api/requests/new", s.recoveryMiddleware(s.csrfMiddleware(withBodyLimit(s.handleNewRequest, 10*1024*1024))))
	http.HandleFunc("/api/requests/duplicate", s.recoveryMiddleware(s.csrfMiddleware(withBodyLimit(s.handleDuplicateRequest, 10*1024*1024))))
	http.HandleFunc("/api/requests/reorder", s.recoveryMiddleware(s.csrfMiddleware(withBodyLimit(s.handleReorderRequest, 10*1024*1024))))
	http.HandleFunc("/api/requests/update", s.recoveryMiddleware(s.csrfMiddleware(withBodyLimit(s.handleUpdateRequest, 10*1024*1024))))
	http.HandleFunc("/api/requests/delete", s.recoveryMiddleware(s.csrfMiddleware(s.handleDeleteRequest)))
	http.HandleFunc("/api/variables", s.recoveryMiddleware(s.csrfMiddleware(s.handleVariables)))
	http.HandleFunc("/api/history", s.recoveryMiddleware(s.csrfMiddleware(s.handleGetHistory)))
	http.HandleFunc("/api/history/clear", s.recoveryMiddleware(s.csrfMiddleware(s.handleClearHistory)))
	http.HandleFunc("/api/history/delete", s.recoveryMiddleware(s.csrfMiddleware(s.handleDeleteHistory)))
	http.HandleFunc("/api/history/export", s.recoveryMiddleware(s.csrfMiddleware(s.handleExportHistory)))
	http.HandleFunc("/api/export", s.recoveryMiddleware(s.csrfMiddleware(s.handleExportCollection)))
	http.HandleFunc("/api/send", s.recoveryMiddleware(s.csrfMiddleware(s.handleSendRequest)))
	http.HandleFunc("/api/hammer", s.recoveryMiddleware(s.csrfMiddleware(s.handleHammerRequest)))
	http.HandleFunc("/api/hammer/history", s.recoveryMiddleware(s.csrfMiddleware(s.handleGetHammerHistory)))
	http.HandleFunc("/api/sql", s.recoveryMiddleware(s.csrfMiddleware(s.handleSQLRequest)))
	http.HandleFunc("/api/schema/generate", s.recoveryMiddleware(s.csrfMiddleware(s.handleSchemaGenerate)))
	http.HandleFunc("/api/schema/validate", s.recoveryMiddleware(s.csrfMiddleware(s.handleSchemaValidate)))
	http.HandleFunc("/api/mock/save", s.recoveryMiddleware(s.csrfMiddleware(s.handleSaveMockResponse)))
	http.HandleFunc("/api/mock/delete", s.recoveryMiddleware(s.csrfMiddleware(s.handleDeleteMock)))
	http.HandleFunc("/api/mock/stats", s.recoveryMiddleware(s.csrfMiddleware(s.handleMockStats)))
	http.HandleFunc("/api/fuzz", s.recoveryMiddleware(s.csrfMiddleware(s.handleFuzzRequest)))
	http.HandleFunc("/api/runner/run", s.recoveryMiddleware(s.csrfMiddleware(s.handleRunnerRun)))
	http.HandleFunc("/api/graphql/introspection", s.recoveryMiddleware(s.csrfMiddleware(s.handleGraphQLIntrospection)))
	http.HandleFunc("/api/import/curl", s.recoveryMiddleware(s.csrfMiddleware(s.handleImportCurl)))
	http.HandleFunc("/api/import/openapi", s.recoveryMiddleware(s.csrfMiddleware(s.handleImportOpenAPI)))
	http.HandleFunc("/api/docs/generate", s.recoveryMiddleware(s.csrfMiddleware(s.handleGenerateDocs)))
	http.HandleFunc("/api/workflows", s.recoveryMiddleware(s.csrfMiddleware(s.handleWorkflows)))
	http.HandleFunc("/api/workflows/run", s.recoveryMiddleware(s.csrfMiddleware(s.handleRunWorkflow)))
	http.HandleFunc("/api/environments", s.recoveryMiddleware(s.csrfMiddleware(s.handleEnvironments)))
	http.HandleFunc("/api/environments/active", s.recoveryMiddleware(s.csrfMiddleware(s.handleActiveEnv)))
	http.HandleFunc("/api/vault/unlock", s.recoveryMiddleware(s.csrfMiddleware(s.handleUnlockVault)))
	http.HandleFunc("/api/vault/encrypt", s.recoveryMiddleware(s.csrfMiddleware(s.handleVaultEncrypt)))
	http.HandleFunc("/api/vault/status", s.recoveryMiddleware(s.csrfMiddleware(s.handleVaultStatus)))
	http.HandleFunc("/api/ws/connect", s.recoveryMiddleware(s.csrfMiddleware(s.handleWSConnect)))
	http.HandleFunc("/api/ws/send", s.recoveryMiddleware(s.csrfMiddleware(s.handleWSSend)))
	http.HandleFunc("/api/ws/messages", s.recoveryMiddleware(s.csrfMiddleware(s.handleWSMessages)))
	http.HandleFunc("/api/ws/close", s.recoveryMiddleware(s.csrfMiddleware(s.handleWSClose)))
	http.HandleFunc("/api/proxy/start", s.recoveryMiddleware(s.csrfMiddleware(s.handleProxyStart)))
	http.HandleFunc("/api/proxy/stop", s.recoveryMiddleware(s.csrfMiddleware(s.handleProxyStop)))
	http.HandleFunc("/api/proxy/status", s.recoveryMiddleware(s.csrfMiddleware(s.handleProxyStatus)))

	// ── Kafka ───────────────────────────────────────────────────────────────────
	http.HandleFunc("/api/kafka/connect", s.recoveryMiddleware(s.csrfMiddleware(s.handleKafkaConnect)))
	http.HandleFunc("/api/kafka/send", s.recoveryMiddleware(s.csrfMiddleware(s.handleKafkaSend)))
	http.HandleFunc("/api/kafka/topics", s.recoveryMiddleware(s.csrfMiddleware(s.handleKafkaTopics)))
	http.HandleFunc("/api/kafka/topics/", s.recoveryMiddleware(s.csrfMiddleware(s.handleKafkaTopicMeta)))
	http.HandleFunc("/api/kafka/status", s.recoveryMiddleware(s.csrfMiddleware(s.handleKafkaStatus)))
	http.HandleFunc("/api/kafka/disconnect", s.recoveryMiddleware(s.csrfMiddleware(s.handleKafkaDisconnect)))
	http.HandleFunc("/api/kafka/configs", s.recoveryMiddleware(s.csrfMiddleware(s.handleKafkaConfigs)))
	
	if s.EnableMock {
		http.HandleFunc("/mock/", s.handleMockRequest)
		fmt.Printf("Mock server enabled at http://localhost:%d/mock/\n", port)
	} else {
		http.HandleFunc("/mock/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "Mock server is disabled. Start with -mock flag to enable it.")
		})
	}

	fsys, err := fs.Sub(staticAssets, "static")
	if err != nil {
		return err
	}
	http.Handle("/", http.FileServer(http.FS(fsys)))

	fmt.Printf("Web UI started at http://localhost:%d\n", port)

	// Configure server with timeouts
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Server error: %v\n", err)
		}
	}()

	// Wait for context cancellation (shutdown signal)
	<-ctx.Done()

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fmt.Println("Shutting down server...")
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	return nil
}

// Handler implementations

func (s *Server) handleGetRequests(w http.ResponseWriter, r *http.Request) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"collection": s.Collection,
		"flat":       s.FlatList,
	}
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleNewRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Path             string              `json:"path"`
		Method           string              `json:"method"`
		URL              string              `json:"url"`
		BodyMode         string              `json:"bodyMode"`
		BodyRaw          string              `json:"bodyRaw"`
		UrlEncoded       []models.UrlEncoded `json:"urlencoded"`
		Headers          []models.Header     `json:"headers"`
		PreRequestScript string              `json:"preRequestScript"`
		TestScript       string              `json:"testScript"`
		Note             string              `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	newReq := models.RequestInfo{
		Path: input.Path,
		Request: &models.Request{
			Method: input.Method,
			URL:    models.URL{Raw: input.URL},
			Body: &models.Body{
				Mode:       input.BodyMode,
				Raw:        input.BodyRaw,
				UrlEncoded: input.UrlEncoded,
			},
			Header: input.Headers,
		},
		Order: len(s.FlatList),
		Note:  input.Note,
	}

	if input.PreRequestScript != "" {
		newReq.Events = append(newReq.Events, models.Event{
			Listen: "prerequest",
			Script: models.Script{Type: "text/javascript", Exec: strings.Split(input.PreRequestScript, "\n")},
		})
	}
	if input.TestScript != "" {
		newReq.Events = append(newReq.Events, models.Event{
			Listen: "test",
			Script: models.Script{Type: "text/javascript", Exec: strings.Split(input.TestScript, "\n")},
		})
	}

	s.Storage.SaveSingleRequest(newReq)
	s.FlatList = append(s.FlatList, newReq)
	s.Collection.Item = models.ReconstructItems(s.FlatList)
	s.Storage.SaveCollection(s.Collection)
	
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newReq)
}

func (s *Server) handleDuplicateRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Path    string `json:"path"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	var target *models.RequestInfo
	for _, req := range s.FlatList {
		if req.Path == input.Path {
			target = &req
			break
		}
	}

	if target == nil {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	newReq := models.RequestInfo{
		Path:    input.NewPath,
		Request: target.Request.DeepCopy(),
		Events:  append([]models.Event{}, target.Events...),
		Order:   target.Order + 1,
		Note:    target.Note,
	}

	s.Storage.SaveSingleRequest(newReq)
	s.FlatList = append(s.FlatList, newReq)
	s.Collection.Item = models.ReconstructItems(s.FlatList)
	s.Storage.SaveCollection(s.Collection)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newReq)
}

func (s *Server) handleUpdateRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OldPath          string              `json:"oldPath"`
		NewPath          string              `json:"newPath"`
		Method           string              `json:"method"`
		URL              string              `json:"url"`
		BodyMode         string              `json:"bodyMode"`
		BodyRaw          string              `json:"bodyRaw"`
		UrlEncoded       []models.UrlEncoded `json:"urlencoded"`
		Headers          []models.Header     `json:"headers"`
		PreRequestScript string              `json:"preRequestScript"`
		TestScript       string              `json:"testScript"`
		Note             string              `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	idx := -1
	for i, req := range s.FlatList {
		if req.Path == input.OldPath {
			idx = i
			break
		}
	}

	if idx == -1 {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	s.FlatList[idx].Path = input.NewPath
	s.FlatList[idx].Request.Method = input.Method
	s.FlatList[idx].Request.URL.Raw = input.URL
	s.FlatList[idx].Request.Header = input.Headers
	if s.FlatList[idx].Request.Body == nil { s.FlatList[idx].Request.Body = &models.Body{} }
	s.FlatList[idx].Request.Body.Mode = input.BodyMode
	s.FlatList[idx].Request.Body.Raw = input.BodyRaw
	s.FlatList[idx].Request.Body.UrlEncoded = input.UrlEncoded
	s.FlatList[idx].Note = input.Note

	s.FlatList[idx].Events = []models.Event{}
	if input.PreRequestScript != "" {
		s.FlatList[idx].Events = append(s.FlatList[idx].Events, models.Event{Listen: "prerequest", Script: models.Script{Type: "text/javascript", Exec: strings.Split(input.PreRequestScript, "\n")}})
	}
	if input.TestScript != "" {
		s.FlatList[idx].Events = append(s.FlatList[idx].Events, models.Event{Listen: "test", Script: models.Script{Type: "text/javascript", Exec: strings.Split(input.TestScript, "\n")}})
	}

	if err := s.Storage.SaveSingleRequest(s.FlatList[idx]); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Collection.Item = models.ReconstructItems(s.FlatList)
	if err := s.Storage.SaveCollection(s.Collection); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Return the updated request so frontend can sync its state
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(s.FlatList[idx]); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

func (s *Server) handleDeleteRequest(w http.ResponseWriter, r *http.Request) {
	var input struct{ Path string `json:"path"` }
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	for i, req := range s.FlatList {
		if req.Path == input.Path {
			s.Storage.DeleteRequestFile(input.Path)
			s.FlatList = append(s.FlatList[:i], s.FlatList[i+1:]...)
			s.Collection.Item = models.ReconstructItems(s.FlatList)
			s.Storage.SaveCollection(s.Collection)
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	http.Error(w, "Not found", http.StatusNotFound)
}

func (s *Server) handleVariables(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(s.Storage.GetVariableMapCopy())
	} else {
		var input map[string]string
		json.NewDecoder(r.Body).Decode(&input)
		for k, v := range input { s.Storage.SetVariable(k, v) }
		w.WriteHeader(http.StatusOK)
	}
}

func (s *Server) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(s.Storage.LoadHistory())
}

func (s *Server) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	s.Storage.SaveHistory([]models.HistoryRecord{})
}

func (s *Server) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	var input struct{ Timestamp time.Time `json:"timestamp"` }
	json.NewDecoder(r.Body).Decode(&input)
	history := s.Storage.LoadHistory()
	newHistory := []models.HistoryRecord{}
	for _, h := range history {
		if !h.Timestamp.Equal(input.Timestamp) { newHistory = append(newHistory, h) }
	}
	s.Storage.SaveHistory(newHistory)
}

func (s *Server) handleSendRequest(w http.ResponseWriter, r *http.Request) {
	var input struct{ Path string `json:"path"` }
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.RLock()
	var target *models.RequestInfo
	for i := range s.FlatList {
		if s.FlatList[i].Path == input.Path {
			target = &s.FlatList[i]
			break
		}
	}
	// Copy request data while holding lock to avoid race conditions
	var reqCopy *models.Request
	var eventsCopy []models.Event
	if target != nil && target.Request != nil {
		reqCopy = target.Request.DeepCopy()
		eventsCopy = make([]models.Event, len(target.Events))
		copy(eventsCopy, target.Events)
	}
	s.stateMu.RUnlock()

	if reqCopy == nil {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	s.Processor.RunScripts(eventsCopy, "prerequest", nil, nil, reqCopy.Header)
	body, headers, code, status := s.Client.ExecuteRequest(r.Context(), reqCopy)
	s.Processor.RunScripts(eventsCopy, "test", []byte(body), headers, reqCopy.Header)

	resp := map[string]interface{}{
		"body": body,
		"headers": headers,
		"statusCode": code,
		"statusText": status,
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSQLRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Driver string `json:"driver"`
		ConnStr string `json:"connStr"`
		Query string `json:"query"`
	}
	json.NewDecoder(r.Body).Decode(&input)
	cols, rows, err := s.Client.ExecuteSQL(r.Context(), input.Driver, input.ConnStr, input.Query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"columns": cols, "rows": rows})
}

func (s *Server) handleHammerRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path     string `json:"path"`
		Workers  int    `json:"workers"`
		Duration int    `json:"duration"`
		Stream   bool   `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.RLock()
	var target *models.RequestInfo
	// Use slice element addressing to avoid stale pointer
	for i := range s.FlatList {
		if s.FlatList[i].Path == input.Path {
			target = &s.FlatList[i]
			break
		}
	}
	// Deep copy request data while holding lock to avoid race conditions
	var reqCopy *models.Request
	if target != nil && target.Request != nil {
		reqCopy = target.Request.DeepCopy()
	}
	s.stateMu.RUnlock()

	if reqCopy == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	duration := time.Duration(input.Duration) * time.Second

	if input.Stream {
		// Streaming mode: flush progress as newline-delimited JSON every 500ms
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)

		progressCh := make(chan *api.HammerProgress, 32)
		go s.Client.HammerStream(reqCopy, input.Workers, duration, progressCh)

		for p := range progressCh {
			line, _ := json.Marshal(p)
			w.Write(append(line, '\n'))
			flusher.Flush()
		}
		return
	}

	results := s.Client.Hammer(reqCopy, input.Workers, duration)
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleMockRequest(w http.ResponseWriter, r *http.Request) {
	mockPath := strings.TrimPrefix(r.URL.Path, "/mock")
	if mockPath == "" { mockPath = "/" }

	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	for _, reqInfo := range s.FlatList {
		if !strings.EqualFold(reqInfo.Request.Method, r.Method) { continue }
		// Simple path match for now
		if reqInfo.Request.URL.Raw == mockPath || strings.HasSuffix(reqInfo.Request.URL.Raw, mockPath) {
			for _, mock := range reqInfo.Responses {
				for _, h := range mock.Header { w.Header().Add(h.Key, s.Processor.ResolveVariables(h.Value)) }
				w.WriteHeader(mock.Code)
				w.Write([]byte(s.Processor.ResolveVariables(mock.Body)))
				return
			}
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) handleReorderRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	pathMap := make(map[string]int)
	for i, path := range input.Paths {
		pathMap[path] = i
	}

	for i := range s.FlatList {
		if order, ok := pathMap[s.FlatList[i].Path]; ok {
			s.FlatList[i].Order = order
		}
	}

	s.Collection.Item = models.ReconstructItems(s.FlatList)
	s.Storage.SaveCollection(s.Collection)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleFuzzRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.RLock()
	var target *models.RequestInfo
	for _, req := range s.FlatList {
		if req.Path == input.Path {
			target = &req
			break
		}
	}
	s.stateMu.RUnlock()

	if target == nil {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	results, err := s.fuzzer.Run(r.Context(), *target, s.Storage.GetVariableMapCopy())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleRunnerRun(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string              `json:"path"`
		Data []map[string]string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.RLock()
	var target *models.RequestInfo
	for _, req := range s.FlatList {
		if req.Path == input.Path {
			target = &req
			break
		}
	}
	s.stateMu.RUnlock()

	if target == nil {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	if input.Data == nil {
		input.Data = []map[string]string{{}} // Single iteration if no data provided
	}

	results := s.runner.RunIteration(r.Context(), *target, input.Data)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleWSConnect(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.WSClient.Connect(input.URL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleWSSend(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.WSClient.Send(input.Message); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleWSMessages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.WSClient.GetMessages())
}

func (s *Server) handleWSClose(w http.ResponseWriter, r *http.Request) {
	s.WSClient.Close()
	w.WriteHeader(http.StatusOK)
}

// Implemented handlers

func (s *Server) handleImportCurl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req := processor.ParseCurl(input.Command)
	if req == nil {
		http.Error(w, "Failed to parse cURL command", http.StatusBadRequest)
		return
	}

	newReq := models.RequestInfo{
		Path:    "Imported > cURL > " + time.Now().Format("15:04:05"),
		Request: req,
		Order:   len(s.FlatList),
	}

	s.stateMu.Lock()
	s.FlatList = append(s.FlatList, newReq)
	s.Collection.Item = models.ReconstructItems(s.FlatList)
	s.stateMu.Unlock()

	s.Storage.SaveSingleRequest(newReq)
	s.Storage.SaveCollection(s.Collection)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"path":    newReq.Path,
	})
}

func (s *Server) handleImportOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		JSON string `json:"json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Parse OpenAPI spec
	requests, err := processor.ParseOpenAPI([]byte(input.JSON))
	if err != nil {
		http.Error(w, "Failed to parse OpenAPI spec: "+err.Error(), http.StatusBadRequest)
		return
	}

	importedCount := 0
	for _, req := range requests {
		req.Order = len(s.FlatList) + importedCount
		s.stateMu.Lock()
		s.FlatList = append(s.FlatList, req)
		s.stateMu.Unlock()
		s.Storage.SaveSingleRequest(req)
		importedCount++
	}

	s.stateMu.Lock()
	s.Collection.Item = models.ReconstructItems(s.FlatList)
	s.stateMu.Unlock()
	s.Storage.SaveCollection(s.Collection)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"count":   importedCount,
	})
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		workflows := s.Storage.GetWorkflows()
		json.NewEncoder(w).Encode(workflows)

	case http.MethodPost:
		var workflow models.Workflow
		if err := json.NewDecoder(r.Body).Decode(&workflow); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if workflow.ID == "" {
			workflow.ID = fmt.Sprintf("wf_%d", time.Now().UnixNano())
		}

		// Check if updating existing
		existing := s.Storage.GetWorkflows()
		found := false
		for i, w := range existing {
			if w.ID == workflow.ID {
				existing[i] = workflow
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, workflow)
		}

		if err := s.Storage.SaveWorkflows(existing); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(workflow)

	case http.MethodDelete:
		var input struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.Storage.DeleteWorkflow(input.ID); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// validateWorkflow checks that a workflow is valid before execution
func (s *Server) validateWorkflow(workflow *models.Workflow) error {
	if len(workflow.Nodes) == 0 {
		return fmt.Errorf("workflow has no nodes")
	}

	// Build maps for validation
	nodeMap := make(map[string]bool)
	requestNodes := make(map[string]string) // nodeID -> requestPath
	for _, node := range workflow.Nodes {
		nodeMap[node.ID] = true
		if node.Type == "request" {
			requestNodes[node.ID] = node.RequestPath
		}
	}

	// Validate edges
	for _, edge := range workflow.Edges {
		if !nodeMap[edge.FromNode] {
			return fmt.Errorf("edge references non-existent node: %s", edge.FromNode)
		}
		if !nodeMap[edge.ToNode] {
			return fmt.Errorf("edge references non-existent node: %s", edge.ToNode)
		}
	}

	// Validate that all request nodes reference existing request paths
	s.stateMu.RLock()
	pathMap := make(map[string]bool)
	for _, req := range s.FlatList {
		pathMap[req.Path] = true
	}
	s.stateMu.RUnlock()

	for nodeID, path := range requestNodes {
		if path == "" {
			return fmt.Errorf("request node %s has no request path", nodeID)
		}
		if !pathMap[path] {
			return fmt.Errorf("request node %s references non-existent request path: %s", nodeID, path)
		}
	}

	// Check for potential cycles (basic check - max edges = nodes - 1 for acyclic)
	if len(workflow.Edges) > len(workflow.Nodes)*10 {
		return fmt.Errorf("workflow appears to have excessive edges, possible cycle")
	}

	return nil
}

func (s *Server) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		WorkflowID string `json:"workflowId"`
		StartNode  string `json:"startNode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Find workflow
	var workflow *models.Workflow
	workflows := s.Storage.GetWorkflows()
	for i := range workflows {
		if workflows[i].ID == input.WorkflowID {
			workflow = &workflows[i]
			break
		}
	}
	if workflow == nil {
		http.Error(w, "Workflow not found", http.StatusNotFound)
		return
	}

	// Validate workflow before execution
	if err := s.validateWorkflow(workflow); err != nil {
		http.Error(w, fmt.Sprintf("Invalid workflow: %v", err), http.StatusBadRequest)
		return
	}

	// Get requests for workflow
	s.stateMu.RLock()
	requests := make([]models.RequestInfo, len(s.FlatList))
	copy(requests, s.FlatList)
	s.stateMu.RUnlock()

	// Run workflow
	logs, err := s.Client.RunWorkflow(r.Context(), workflow, requests, input.StartNode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs":  logs,
		"error": err,
	})
}

func (s *Server) handleEnvironments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		envs := s.Storage.GetEnvironments()
		json.NewEncoder(w).Encode(envs)

	case http.MethodPost:
		var env models.Environment
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if env.ID == "" {
			env.ID = fmt.Sprintf("env_%d", time.Now().UnixNano())
		}

		envs := s.Storage.GetEnvironments()
		found := false
		for i, e := range envs {
			if e.ID == env.ID {
				envs[i] = env
				found = true
				break
			}
		}
		if !found {
			envs = append(envs, env)
		}

		if err := s.Storage.SaveEnvironments(envs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(env)
	}
}

func (s *Server) handleActiveEnv(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(map[string]string{
			"activeEnvId": s.Storage.GetActiveEnvID(),
		})

	case http.MethodPost:
		var input struct {
			EnvID string `json:"envId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.Storage.SetActiveEnvID(input.EnvID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (s *Server) handleUnlockVault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.Storage.SetVaultKey(input.Password); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"unlocked": true})
}

func (s *Server) handleVaultEncrypt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	encrypted, err := s.Storage.Encrypt(input.Plaintext)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"encrypted": encrypted})
}

func (s *Server) handleVaultStatus(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]bool{"unlocked": s.Storage.IsVaultUnlocked()})
}

func (s *Server) handleProxyStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Port == 0 {
		input.Port = 8888
	}

	if err := s.Proxy.Start(input.Port); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running": true,
		"port":    input.Port,
	})
}

func (s *Server) handleProxyStop(w http.ResponseWriter, r *http.Request) {
	if err := s.Proxy.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"running": false})
}

func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"running": s.Proxy.IsRunning()})
}

// Implemented handlers (previously stubs)

// handleExportHistory exports request history in CSV or JSON format
func (s *Server) handleExportHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	history := s.Storage.LoadHistory()

	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=history.csv")
		// Write CSV header
		fmt.Fprintln(w, "Timestamp,Path,Method,URL,StatusCode,StatusText,Duration")
		for _, h := range history {
			fmt.Fprintf(w, "%s,%s,%s,%s,%d,%s,%d\n",
				h.Timestamp.Format(time.RFC3339),
				escapeCSV(h.Path),
				h.Method,
				escapeCSV(h.URL),
				h.StatusCode,
				h.StatusText,
				h.Duration,
			)
		}
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=history.json")
		json.NewEncoder(w).Encode(history)
	default:
		http.Error(w, "Invalid format. Use 'json' or 'csv'", http.StatusBadRequest)
	}
}

// escapeCSV escapes a string for CSV export
func escapeCSV(s string) string {
	if strings.Contains(s, ",") || strings.Contains(s, "\"") || strings.Contains(s, "\n") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// handleGetHammerHistory returns saved hammer test results
func (s *Server) handleGetHammerHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mockMu.RLock()
	// Hammer results are stored in memory for now
	results := []map[string]interface{}{}
	s.mockMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// handleSchemaGenerate generates a JSON schema from a sample response
func (s *Server) handleSchemaGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	schema := generateJSONSchema(input.Body)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(schema)
}

// generateJSONSchema creates a simple JSON schema from a JSON sample
type JSONSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Items      *JSONSchema            `json:"items,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

func generateJSONSchema(jsonStr string) JSONSchema {
	var data interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return JSONSchema{Type: "string"}
	}

	switch v := data.(type) {
	case map[string]interface{}:
		schema := JSONSchema{
			Type:       "object",
			Properties: make(map[string]interface{}),
		}
		for key, val := range v {
			schema.Properties[key] = inferSchemaType(val)
			schema.Required = append(schema.Required, key)
		}
		return schema
	case []interface{}:
		schema := JSONSchema{
			Type: "array",
		}
		if len(v) > 0 {
			itemSchema := inferSchemaType(v[0])
			schema.Items = &itemSchema
		}
		return schema
	default:
		return JSONSchema{Type: "string"}
	}
}

func inferSchemaType(val interface{}) JSONSchema {
	switch v := val.(type) {
	case bool:
		return JSONSchema{Type: "boolean"}
	case float64:
		if float64(int64(v)) == v {
			return JSONSchema{Type: "integer"}
		}
		return JSONSchema{Type: "number"}
	case string:
		return JSONSchema{Type: "string"}
	case []interface{}:
		schema := JSONSchema{Type: "array"}
		if len(v) > 0 {
			itemSchema := inferSchemaType(v[0])
			schema.Items = &itemSchema
		}
		return schema
	case map[string]interface{}:
		return generateJSONSchema(fmt.Sprintf("%v", v))
	default:
		return JSONSchema{Type: "string"}
	}
}

// handleSchemaValidate validates a JSON body against a schema
func (s *Server) handleSchemaValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Body   string     `json:"body"`
		Schema JSONSchema `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	valid, errors := validateAgainstSchema(input.Body, input.Schema)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":  valid,
		"errors": errors,
	})
}

func validateAgainstSchema(body string, schema JSONSchema) (bool, []string) {
	var data interface{}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return false, []string{"Invalid JSON: " + err.Error()}
	}

	var errors []string

	switch schema.Type {
	case "object":
		obj, ok := data.(map[string]interface{})
		if !ok {
			return false, []string{"Expected object, got " + fmt.Sprintf("%T", data)}
		}
		for _, req := range schema.Required {
			if _, ok := obj[req]; !ok {
				errors = append(errors, fmt.Sprintf("Missing required field: %s", req))
			}
		}
	case "array":
		arr, ok := data.([]interface{})
		if !ok {
			return false, []string{"Expected array, got " + fmt.Sprintf("%T", data)}
		}
		for i, item := range arr {
			if schema.Items != nil {
				_, itemErrors := validateAgainstSchema(fmt.Sprintf("%v", item), *schema.Items)
				for _, err := range itemErrors {
					errors = append(errors, fmt.Sprintf("Item %d: %s", i, err))
				}
			}
		}
	}

	return len(errors) == 0, errors
}

// handleSaveMockResponse saves a mock response for a request
func (s *Server) handleSaveMockResponse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		RequestPath string              `json:"requestPath"`
		Response    models.MockResponse `json:"response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	// Find the request
	for i := range s.FlatList {
		if s.FlatList[i].Path == input.RequestPath {
			// Add or update mock response
			updated := false
			for j := range s.FlatList[i].Responses {
				if s.FlatList[i].Responses[j].Name == input.Response.Name {
					s.FlatList[i].Responses[j] = input.Response
					updated = true
					break
				}
			}
			if !updated {
				s.FlatList[i].Responses = append(s.FlatList[i].Responses, input.Response)
			}

			// Save to storage
			s.Storage.SaveSingleRequest(s.FlatList[i])
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"success": true})
			return
		}
	}

	http.Error(w, "Request not found", http.StatusNotFound)
}

// handleDeleteMock deletes a saved mock response
func (s *Server) handleDeleteMock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		RequestPath    string `json:"requestPath"`
		ResponseName string `json:"responseName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	for i := range s.FlatList {
		if s.FlatList[i].Path == input.RequestPath {
			// Remove the mock response
			for j := len(s.FlatList[i].Responses) - 1; j >= 0; j-- {
				if s.FlatList[i].Responses[j].Name == input.ResponseName {
					s.FlatList[i].Responses = append(s.FlatList[i].Responses[:j], s.FlatList[i].Responses[j+1:]...)
					s.Storage.SaveSingleRequest(s.FlatList[i])
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			http.Error(w, "Mock response not found", http.StatusNotFound)
			return
		}
	}

	http.Error(w, "Request not found", http.StatusNotFound)
}

// handleMockStats returns actual mock usage statistics
func (s *Server) handleMockStats(w http.ResponseWriter, r *http.Request) {
	s.mockMu.RLock()
	stats := make(map[string]*models.MockStat)
	for k, v := range s.mockStats {
		stats[k] = v
	}
	s.mockMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleGraphQLIntrospection performs GraphQL introspection on an endpoint
func (s *Server) handleGraphQLIntrospection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// GraphQL introspection query
	introspectionQuery := `
	query IntrospectionQuery {
		__schema {
			queryType { name }
			mutationType { name }
			subscriptionType { name }
			types {
				...FullType
			}
			directives {
				name
				description
				locations
				args {
					...InputValue
				}
			}
		}
	}
	fragment FullType on __Type {
		kind
		name
		description
		fields(includeDeprecated: true) {
			name
			description
			args {
				...InputValue
			}
			type {
				...TypeRef
			}
			isDeprecated
			deprecationReason
		}
		inputFields {
			...InputValue
		}
		interfaces {
			...TypeRef
		}
		enumValues(includeDeprecated: true) {
			name
			description
			isDeprecated
			deprecationReason
		}
		possibleTypes {
			...TypeRef
		}
	}
	fragment InputValue on __InputValue {
		name
		description
		type { ...TypeRef }
		defaultValue
	}
	fragment TypeRef on __Type {
		kind
		name
		ofType {
			kind
			name
			ofType {
				kind
				name
				ofType {
					kind
					name
					ofType {
						kind
						name
						ofType {
							kind
							name
							ofType {
								kind
								name
								ofType {
									kind
									name
								}
							}
						}
					}
				}
			}
		}
	}`

	reqBody, _ := json.Marshal(map[string]string{
		"query": introspectionQuery,
	})

	req, err := http.NewRequest("POST", input.URL, strings.NewReader(string(reqBody)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range input.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var result interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleGenerateDocs generates API documentation from the collection
func (s *Server) handleGenerateDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "markdown"
	}

	s.stateMu.RLock()
	requests := make([]models.RequestInfo, len(s.FlatList))
	copy(requests, s.FlatList)
	s.stateMu.RUnlock()

	switch format {
	case "markdown":
		w.Header().Set("Content-Type", "text/markdown")
		w.Header().Set("Content-Disposition", "attachment; filename=api-docs.md")
		generateMarkdownDocs(w, s.Collection.Info.Name, requests)
	case "html":
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Disposition", "attachment; filename=api-docs.html")
		generateHTMLDocs(w, s.Collection.Info.Name, requests)
	case "openapi":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=api-docs.json")
		generateOpenAPIDocs(w, s.Collection.Info.Name, requests)
	default:
		http.Error(w, "Invalid format. Use 'markdown', 'html', or 'openapi'", http.StatusBadRequest)
	}
}

func generateMarkdownDocs(w http.ResponseWriter, collectionName string, requests []models.RequestInfo) {
	fmt.Fprintf(w, "# %s API Documentation\n\n", collectionName)

	for _, req := range requests {
		fmt.Fprintf(w, "## %s\n\n", req.Path)
		fmt.Fprintf(w, "**Method:** `%s`\n\n", req.Request.Method)
		fmt.Fprintf(w, "**URL:** `%s`\n\n", req.Request.URL.Raw)

		if len(req.Request.Header) > 0 {
			fmt.Fprintln(w, "**Headers:**")
			for _, h := range req.Request.Header {
				fmt.Fprintf(w, "- `%s`: %s\n", h.Key, h.Value)
			}
			fmt.Fprintln(w)
		}

		if req.Request.Body != nil && req.Request.Body.Raw != "" {
			fmt.Fprintf(w, "**Body:**\n```json\n%s\n```\n\n", req.Request.Body.Raw)
		}

	if req.Note != "" {
		fmt.Fprintf(w, "**Note:** %s\n\n", req.Note)
	}

	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)
}
}

func generateHTMLDocs(w http.ResponseWriter, collectionName string, requests []models.RequestInfo) {
	fmt.Fprintln(w, `<!DOCTYPE html>
<html>
<head>
	<title>`+collectionName+` API Documentation</title>
	<style>
		body { font-family: Arial, sans-serif; margin: 40px; background: #f5f5f5; }
		.request { background: white; padding: 20px; margin: 20px 0; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
		.method { color: #2196F3; font-weight: bold; font-size: 14px; }
		.url { color: #333; font-family: monospace; margin: 10px 0; }
		.headers { background: #f9f9f9; padding: 10px; border-radius: 4px; }
		body-section { background: #263238; color: #aed581; padding: 15px; border-radius: 4px; font-family: monospace; overflow-x: auto; }
		h1 { color: #333; border-bottom: 2px solid #2196F3; padding-bottom: 10px; }
		h2 { color: #555; margin-top: 0; }
	</style>
</head>
<body>`)
	fmt.Fprintf(w, "<h1>%s API Documentation</h1>\n", collectionName)

	for _, req := range requests {
		fmt.Fprintln(w, `<div class="request">`)
		fmt.Fprintf(w, "<h2>%s</h2>\n", req.Path)
		fmt.Fprintf(w, "<div class=\"method\">%s</div>\n", req.Request.Method)
		fmt.Fprintf(w, "<div class=\"url\">%s</div>\n", req.Request.URL.Raw)

		if len(req.Request.Header) > 0 {
			fmt.Fprintln(w, `<div class="headers">`)
			fmt.Fprintln(w, "<strong>Headers:</strong><br>")
			for _, h := range req.Request.Header {
				fmt.Fprintf(w, "%s: %s<br>\n", h.Key, h.Value)
			}
			fmt.Fprintln(w, "</div>")
		}

		if req.Request.Body != nil && req.Request.Body.Raw != "" {
			fmt.Fprintln(w, `<div class="body-section">`)
			fmt.Fprintf(w, "<pre>%s</pre>\n", req.Request.Body.Raw)
			fmt.Fprintln(w, "</div>")
		}

		fmt.Fprintln(w, "</div>")
	}

	fmt.Fprintln(w, "</body></html>")
}

func generateOpenAPIDocs(w http.ResponseWriter, collectionName string, requests []models.RequestInfo) {
	paths := make(map[string]map[string]interface{})
	for _, req := range requests {
		url := req.Request.URL.Raw
		method := strings.ToLower(req.Request.Method)
		if _, ok := paths[url]; !ok {
			paths[url] = make(map[string]interface{})
		}
		paths[url][method] = map[string]interface{}{
			"summary":     req.Path,
			"description": req.Note,
			"responses": map[string]interface{}{
				"200": map[string]string{
					"description": "OK",
				},
			},
		}
	}

	openAPI := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]string{
			"title":   collectionName,
			"version": "1.0.0",
		},
		"paths": paths,
	}

	json.NewEncoder(w).Encode(openAPI)
}

// ─── Kafka Handlers ─────────────────────────────────────────────────────────

func (s *Server) handleKafkaConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Config models.KafkaConfig `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Apply defaults for zero values
	if input.Config.ClientID == "" {
		input.Config.ClientID = "postit"
	}
	if input.Config.TimeoutSec <= 0 {
		input.Config.TimeoutSec = 10
	}

	err := s.Kafka.Connect(input.Config)
	if err != nil {
		http.Error(w, "Failed to connect: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected": true,
		"brokers":   input.Config.Brokers,
	})
}

func (s *Server) handleKafkaSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg models.KafkaMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if msg.Topic == "" {
		http.Error(w, "topic is required", http.StatusBadRequest)
		return
	}
	if msg.Value == "" && msg.Key == "" {
		http.Error(w, "value or key is required", http.StatusBadRequest)
		return
	}

	result, err := s.Kafka.SendMessage(r.Context(), msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleKafkaTopics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse brokers from query param: ?brokers=host1:9092,host2:9092
	brokersParam := r.URL.Query().Get("brokers")
	if brokersParam == "" {
		http.Error(w, "brokers query parameter is required (comma-separated)", http.StatusBadRequest)
		return
	}
	brokers := strings.Split(brokersParam, ",")

	cfg := api.KafkaConfigDefaults()
	cfg.Brokers = brokers

	topics, err := s.Kafka.GetTopics(r.Context(), cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"topics": topics,
	})
}

func (s *Server) handleKafkaTopicMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract topic from path: /api/kafka/topics/{topic}
	topic := strings.TrimPrefix(r.URL.Path, "/api/kafka/topics/")
	if topic == "" {
		http.Error(w, "topic is required in path", http.StatusBadRequest)
		return
	}

	brokersParam := r.URL.Query().Get("brokers")
	if brokersParam == "" {
		http.Error(w, "brokers query parameter is required (comma-separated)", http.StatusBadRequest)
		return
	}
	brokers := strings.Split(brokersParam, ",")

	cfg := api.KafkaConfigDefaults()
	cfg.Brokers = brokers

	partitions, err := s.Kafka.GetTopicMetadata(r.Context(), cfg, topic)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"topic":      topic,
		"partitions": partitions,
	})
}

func (s *Server) handleKafkaStatus(w http.ResponseWriter, r *http.Request) {
	connected := s.Kafka.IsConnected()
	json.NewEncoder(w).Encode(map[string]bool{
		"connected": connected,
	})
}

func (s *Server) handleKafkaDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.Kafka.Close()
	json.NewEncoder(w).Encode(map[string]bool{"connected": false})
}

func (s *Server) handleKafkaConfigs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		conns := s.Storage.GetKafkaConnections()
		json.NewEncoder(w).Encode(conns)

	case http.MethodPost:
		var conn models.KafkaConnection
		if err := json.NewDecoder(r.Body).Decode(&conn); err != nil {
			http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if conn.ID == "" {
			http.Error(w, "id is required", http.StatusBadRequest)
			return
		}
		if err := s.Storage.AddKafkaConnection(conn); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(conn)

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}
		if err := s.Storage.DeleteKafkaConnection(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]bool{"deleted": true})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleExportCollection exports a collection or folder in Postman format
func (s *Server) handleExportCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	folderPath := r.URL.Query().Get("path")

	s.stateMu.RLock()
	requests := make([]models.RequestInfo, len(s.FlatList))
	copy(requests, s.FlatList)
	s.stateMu.RUnlock()

	collection := api.ExportPostman(requests, folderPath)

	filename := "collection.json"
	if folderPath != "" {
		filename = folderPath + ".postman_collection.json"
		// Make filename safe
		filename = strings.ReplaceAll(filename, " > ", "_")
		filename = strings.ReplaceAll(filename, "/", "_")
	} else {
		filename = "postit_export.postman_collection.json"
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	json.NewEncoder(w).Encode(collection)
}

