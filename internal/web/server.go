package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
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
		fuzzer:     api.NewFuzzer(),
		runner:     api.NewRunner(client, store, proc),
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

func (s *Server) Start(port int) error {
	http.HandleFunc("/api/requests", s.csrfMiddleware(s.handleGetRequests))
	http.HandleFunc("/api/requests/new", s.csrfMiddleware(s.handleNewRequest))
	http.HandleFunc("/api/requests/duplicate", s.csrfMiddleware(s.handleDuplicateRequest))
	http.HandleFunc("/api/requests/reorder", s.csrfMiddleware(s.handleReorderRequest))
	http.HandleFunc("/api/requests/update", s.csrfMiddleware(s.handleUpdateRequest))
	http.HandleFunc("/api/requests/delete", s.csrfMiddleware(s.handleDeleteRequest))
	http.HandleFunc("/api/variables", s.csrfMiddleware(s.handleVariables))
	http.HandleFunc("/api/history", s.csrfMiddleware(s.handleGetHistory))
	http.HandleFunc("/api/history/clear", s.csrfMiddleware(s.handleClearHistory))
	http.HandleFunc("/api/history/delete", s.csrfMiddleware(s.handleDeleteHistory))
	http.HandleFunc("/api/history/export", s.csrfMiddleware(s.handleExportHistory))
	http.HandleFunc("/api/send", s.csrfMiddleware(s.handleSendRequest))
	http.HandleFunc("/api/hammer", s.csrfMiddleware(s.handleHammerRequest))
	http.HandleFunc("/api/hammer/history", s.csrfMiddleware(s.handleGetHammerHistory))
	http.HandleFunc("/api/sql", s.csrfMiddleware(s.handleSQLRequest))
	http.HandleFunc("/api/schema/generate", s.csrfMiddleware(s.handleSchemaGenerate))
	http.HandleFunc("/api/schema/validate", s.csrfMiddleware(s.handleSchemaValidate))
	http.HandleFunc("/api/mock/save", s.csrfMiddleware(s.handleSaveMockResponse))
	http.HandleFunc("/api/mock/delete", s.csrfMiddleware(s.handleDeleteMock))
	http.HandleFunc("/api/mock/stats", s.csrfMiddleware(s.handleMockStats))
	http.HandleFunc("/api/fuzz", s.csrfMiddleware(s.handleFuzzRequest))
	http.HandleFunc("/api/runner/run", s.csrfMiddleware(s.handleRunnerRun))
	http.HandleFunc("/api/graphql/introspection", s.csrfMiddleware(s.handleGraphQLIntrospection))
	http.HandleFunc("/api/import/curl", s.csrfMiddleware(s.handleImportCurl))
	http.HandleFunc("/api/import/openapi", s.csrfMiddleware(s.handleImportOpenAPI))
	http.HandleFunc("/api/docs/generate", s.csrfMiddleware(s.handleGenerateDocs))
	http.HandleFunc("/api/workflows", s.csrfMiddleware(s.handleWorkflows))
	http.HandleFunc("/api/workflows/run", s.csrfMiddleware(s.handleRunWorkflow))
	http.HandleFunc("/api/environments", s.csrfMiddleware(s.handleEnvironments))
	http.HandleFunc("/api/environments/active", s.csrfMiddleware(s.handleActiveEnv))
	http.HandleFunc("/api/vault/unlock", s.csrfMiddleware(s.handleUnlockVault))
	http.HandleFunc("/api/vault/encrypt", s.csrfMiddleware(s.handleVaultEncrypt))
	http.HandleFunc("/api/vault/status", s.csrfMiddleware(s.handleVaultStatus))
	http.HandleFunc("/api/ws/connect", s.csrfMiddleware(s.handleWSConnect))
	http.HandleFunc("/api/ws/send", s.csrfMiddleware(s.handleWSSend))
	http.HandleFunc("/api/ws/messages", s.csrfMiddleware(s.handleWSMessages))
	http.HandleFunc("/api/ws/close", s.csrfMiddleware(s.handleWSClose))
	http.HandleFunc("/api/proxy/start", s.csrfMiddleware(s.handleProxyStart))
	http.HandleFunc("/api/proxy/stop", s.csrfMiddleware(s.handleProxyStop))
	http.HandleFunc("/api/proxy/status", s.csrfMiddleware(s.handleProxyStatus))
	
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
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
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

	s.FlatList[idx].Events = []models.Event{}
	if input.PreRequestScript != "" {
		s.FlatList[idx].Events = append(s.FlatList[idx].Events, models.Event{Listen: "prerequest", Script: models.Script{Type: "text/javascript", Exec: strings.Split(input.PreRequestScript, "\n")}})
	}
	if input.TestScript != "" {
		s.FlatList[idx].Events = append(s.FlatList[idx].Events, models.Event{Listen: "test", Script: models.Script{Type: "text/javascript", Exec: strings.Split(input.TestScript, "\n")}})
	}

	s.Storage.SaveSingleRequest(s.FlatList[idx])
	s.Collection.Item = models.ReconstructItems(s.FlatList)
	s.Storage.SaveCollection(s.Collection)
	w.WriteHeader(http.StatusOK)
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
	json.NewDecoder(r.Body).Decode(&input)

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

	s.Processor.RunScripts(target.Events, "prerequest", nil, nil, target.Request.Header)
	body, headers, code, status := s.Client.ExecuteRequest(r.Context(), target.Request)
	s.Processor.RunScripts(target.Events, "test", []byte(body), headers, target.Request.Header)

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
		ConnStr string `json:"connStr"`
		Query   string `json:"query"`
	}
	json.NewDecoder(r.Body).Decode(&input)
	cols, rows, err := s.Client.ExecuteSQL(r.Context(), input.ConnStr, input.Query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"columns": cols, "rows": rows})
}

func (s *Server) handleHammerRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path    string `json:"path"`
		Workers int    `json:"workers"`
		Seconds int    `json:"seconds"`
	}
	json.NewDecoder(r.Body).Decode(&input)

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
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	results := s.Client.Hammer(target.Request, input.Workers, time.Duration(input.Seconds)*time.Second)
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

// Stub remaining required methods to fix build
func (s *Server) handleExportHistory(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleGetHammerHistory(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleSchemaGenerate(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleSchemaValidate(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleSaveMockResponse(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleDeleteMock(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleMockStats(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleGraphQLIntrospection(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleImportCurl(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleImportOpenAPI(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleGenerateDocs(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleEnvironments(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleActiveEnv(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleUnlockVault(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleVaultEncrypt(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleVaultStatus(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleProxyStart(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleProxyStop(w http.ResponseWriter, r *http.Request) {}
func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request) {}
