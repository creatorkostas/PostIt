package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
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
	MockStats   map[string]*models.MockStat
	mockMu      sync.Mutex

	// WebSocket
	WSClient *api.WSClient
}

func NewServer(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client, col models.Collection, flat []models.RequestInfo, enableMock bool) *Server {
	return &Server{
		Storage:    store,
		Processor:  proc,
		Client:     client,
		Proxy:      api.NewProxyServer(store),
		Collection: col,
		FlatList:   flat,
		EnableMock: enableMock,
		MockStats:   make(map[string]*models.MockStat),
		WSClient:   api.NewWSClient(),
	}
}

func (s *Server) Start(port int) error {
	http.HandleFunc("/api/requests", s.handleGetRequests)
	http.HandleFunc("/api/requests/new", s.handleNewRequest)
	http.HandleFunc("/api/requests/duplicate", s.handleDuplicateRequest)
	http.HandleFunc("/api/requests/reorder", s.handleReorderRequest)
	http.HandleFunc("/api/requests/update", s.handleUpdateRequest)
	http.HandleFunc("/api/requests/delete", s.handleDeleteRequest)
	http.HandleFunc("/api/variables", s.handleVariables)
	http.HandleFunc("/api/history", s.handleGetHistory)
	http.HandleFunc("/api/history/clear", s.handleClearHistory)
	http.HandleFunc("/api/history/delete", s.handleDeleteHistory)
	http.HandleFunc("/api/history/export", s.handleExportHistory)
	http.HandleFunc("/api/send", s.handleSendRequest)
	http.HandleFunc("/api/hammer", s.handleHammerRequest)
	http.HandleFunc("/api/hammer/history", s.handleGetHammerHistory)
	http.HandleFunc("/api/sql", s.handleSQLRequest)
	http.HandleFunc("/api/mock/save", s.handleSaveMockResponse)
	http.HandleFunc("/api/mock/stats", s.handleMockStats)
	http.HandleFunc("/api/import/curl", s.handleImportCurl)
	http.HandleFunc("/api/import/openapi", s.handleImportOpenAPI)
	http.HandleFunc("/api/docs/generate", s.handleGenerateDocs)
	http.HandleFunc("/api/workflows", s.handleWorkflows)
	http.HandleFunc("/api/workflows/run", s.handleRunWorkflow)
	http.HandleFunc("/api/environments", s.handleEnvironments)
	http.HandleFunc("/api/environments/active", s.handleActiveEnv)
	http.HandleFunc("/api/vault/unlock", s.handleUnlockVault)
	http.HandleFunc("/api/vault/encrypt", s.handleVaultEncrypt)
	http.HandleFunc("/api/vault/status", s.handleVaultStatus)
	http.HandleFunc("/api/ws/connect", s.handleWSConnect)
	http.HandleFunc("/api/ws/send", s.handleWSSend)
	http.HandleFunc("/api/ws/messages", s.handleWSMessages)
	http.HandleFunc("/api/ws/close", s.handleWSClose)
	http.HandleFunc("/api/proxy/start", s.handleProxyStart)
	http.HandleFunc("/api/proxy/stop", s.handleProxyStop)
	http.HandleFunc("/api/proxy/status", s.handleProxyStatus)
	
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

func (s *Server) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	history := s.Storage.LoadHistory()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	s.Storage.SaveHistory([]models.HistoryRecord{})
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Timestamp time.Time `json:"timestamp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	history := s.Storage.LoadHistory()
	var newHistory []models.HistoryRecord
	for _, h := range history {
		if !h.Timestamp.Equal(input.Timestamp) {
			newHistory = append(newHistory, h)
		}
	}
	s.Storage.SaveHistory(newHistory)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSaveMockResponse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Path     string              `json:"path"`
		Name     string              `json:"name"`
		Code     int                 `json:"code"`
		Status   string              `json:"status"`
		Body     string              `json:"body"`
		Headers  []models.Header     `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for i, req := range s.FlatList {
		if req.Path == input.Path {
			newMock := models.MockResponse{
				Name:   input.Name,
				Code:   input.Code,
				Status: input.Status,
				Body:   input.Body,
				Header: input.Headers,
			}
			s.FlatList[i].Responses = append(s.FlatList[i].Responses, newMock)
			s.Storage.SaveSingleRequest(s.FlatList[i])
			
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(newMock)
			return
		}
	}

	http.Error(w, "Request not found", http.StatusNotFound)
}

func (s *Server) handleMockRequest(w http.ResponseWriter, r *http.Request) {
	mockPath := strings.TrimPrefix(r.URL.Path, "/mock")
	if mockPath == "" { mockPath = "/" }

	bodyBytes, _ := ioutil.ReadAll(r.Body)
	bodyStr := string(bodyBytes)

	for _, reqInfo := range s.FlatList {
		if !strings.EqualFold(reqInfo.Request.Method, r.Method) {
			continue
		}

		resolvedURL := s.Processor.ResolveVariables(reqInfo.Request.URL.Raw)
		savedPath := resolvedURL
		if idx := strings.Index(savedPath, "://"); idx != -1 {
			savedPath = savedPath[idx+3:]
		}
		if idx := strings.Index(savedPath, "/"); idx != -1 {
			savedPath = savedPath[idx:]
		} else {
			savedPath = "/"
		}
		savedPathOnly := strings.Split(savedPath, "?")[0]

		params, matched := s.matchMockPath(savedPathOnly, mockPath)
		if !matched {
			continue
		}

		// Inject path params as variables
		for k, v := range params {
			s.Storage.VariableMap[k] = v
		}

		for _, mock := range reqInfo.Responses {
			// Check condition if present
			if mock.Condition != "" {
				// Evaluate condition against incoming body
				val := gjson.Get(bodyStr, mock.Condition)
				if !val.Exists() || val.Type == gjson.False || val.String() == "" {
					continue
				}
			}

			// Found a match
			s.mockMu.Lock()
			statKey := reqInfo.Path + " > " + mock.Name
			if s.MockStats[statKey] == nil {
				s.MockStats[statKey] = &models.MockStat{}
			}
			s.MockStats[statKey].Hits++
			s.MockStats[statKey].LastAccess = time.Now()
			s.mockMu.Unlock()

			for _, h := range mock.Header {
				w.Header().Add(h.Key, s.Processor.ResolveVariables(h.Value))
			}
			w.WriteHeader(mock.Code)
			w.Write([]byte(s.Processor.ResolveVariables(mock.Body)))
			return
		}
	}

	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "No mock response found for %s %s", r.Method, mockPath)
}

func (s *Server) matchMockPath(pattern, path string) (map[string]string, bool) {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")

	if len(patternParts) != len(pathParts) {
		return nil, false
	}

	params := make(map[string]string)
	for i := range patternParts {
		if strings.HasPrefix(patternParts[i], ":") {
			params[patternParts[i][1:]] = pathParts[i]
		} else if patternParts[i] != pathParts[i] {
			return nil, false
		}
	}
	return params, true
}

func (s *Server) handleDeleteRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var targetIdx = -1
	for i, req := range s.FlatList {
		if req.Path == input.Path {
			targetIdx = i
			break
		}
	}

	if targetIdx == -1 {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	// Delete file
	s.Storage.DeleteRequestFile(input.Path)

	// Remove from list
	s.FlatList = append(s.FlatList[:targetIdx], s.FlatList[targetIdx+1:]...)
	s.Collection.Item = models.ReconstructItems(s.FlatList)

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleVariables(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.Storage.VariableMap)
		return
	}

	if r.Method == http.MethodPost {
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		s.Storage.VariableMap = input
		s.Storage.SaveVariables()
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}


func (s *Server) handleUpdateRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

	var targetIdx = -1
	for i, req := range s.FlatList {
		if req.Path == input.OldPath {
			targetIdx = i
			break
		}
	}

	if targetIdx == -1 {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	// Update fields
	s.FlatList[targetIdx].Path = input.NewPath
	s.FlatList[targetIdx].Request.Method = input.Method
	s.FlatList[targetIdx].Request.URL.Raw = input.URL
	
	if s.FlatList[targetIdx].Request.Body == nil {
		s.FlatList[targetIdx].Request.Body = &models.Body{}
	}
	s.FlatList[targetIdx].Request.Body.Mode = input.BodyMode
	s.FlatList[targetIdx].Request.Body.Raw = input.BodyRaw
	s.FlatList[targetIdx].Request.Body.UrlEncoded = input.UrlEncoded
	
	s.FlatList[targetIdx].Request.Header = input.Headers


	// Update Events (Scripts)
	s.FlatList[targetIdx].Events = []models.Event{}
	if input.PreRequestScript != "" {
		s.FlatList[targetIdx].Events = append(s.FlatList[targetIdx].Events, models.Event{
			Listen: "prerequest",
			Script: models.Script{
				Type: "text/javascript",
				Exec: strings.Split(input.PreRequestScript, "\n"),
			},
		})
	}
	if input.TestScript != "" {
		s.FlatList[targetIdx].Events = append(s.FlatList[targetIdx].Events, models.Event{
			Listen: "test",
			Script: models.Script{
				Type: "text/javascript",
				Exec: strings.Split(input.TestScript, "\n"),
			},
		})
	}

	// Save to storage
	s.Storage.SaveSingleRequest(s.FlatList[targetIdx])


	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(s.FlatList[targetIdx])
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

	// Update Events (Scripts)
	if input.PreRequestScript != "" {
		newReq.Events = append(newReq.Events, models.Event{
			Listen: "prerequest",
			Script: models.Script{
				Type: "text/javascript",
				Exec: strings.Split(input.PreRequestScript, "\n"),
			},
		})
	}
	if input.TestScript != "" {
		newReq.Events = append(newReq.Events, models.Event{
			Listen: "test",
			Script: models.Script{
				Type: "text/javascript",
				Exec: strings.Split(input.TestScript, "\n"),
			},
		})
	}

	s.Storage.SaveSingleRequest(newReq)
	s.FlatList = append(s.FlatList, newReq)
	
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

	// Clone events
	var eventsCopy []models.Event
	if target.Events != nil {
		eventsCopy = make([]models.Event, len(target.Events))
		for i, e := range target.Events {
			eventsCopy[i] = e
			if e.Script.Exec != nil {
				eventsCopy[i].Script.Exec = make([]string, len(e.Script.Exec))
				copy(eventsCopy[i].Script.Exec, e.Script.Exec)
			}
		}
	}

	newReq := models.RequestInfo{
		Path:    input.NewPath,
		Request: target.Request.DeepCopy(),
		Events:  eventsCopy,
		Order:   target.Order + 1,
	}

	s.Storage.SaveSingleRequest(newReq)
	s.FlatList = append(s.FlatList, newReq)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newReq)
}


func (s *Server) handleReorderRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Path1 string `json:"path1"`
		Path2 string `json:"path2"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var idx1, idx2 = -1, -1
	for i, req := range s.FlatList {
		if req.Path == input.Path1 {
			idx1 = i
		}
		if req.Path == input.Path2 {
			idx2 = i
		}
	}

	if idx1 == -1 || idx2 == -1 {
		http.Error(w, "One or more requests not found", http.StatusNotFound)
		return
	}

	// Swap orders
	s.FlatList[idx1].Order, s.FlatList[idx2].Order = s.FlatList[idx2].Order, s.FlatList[idx1].Order
	if s.FlatList[idx1].Order == s.FlatList[idx2].Order {
		s.FlatList[idx1].Order++
	}

	s.Storage.SaveSingleRequest(s.FlatList[idx1])
	s.Storage.SaveSingleRequest(s.FlatList[idx2])

	s.Collection.Item = models.ReconstructItems(s.FlatList)

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetRequests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// Send both nested tree and flat list for easy access
	response := map[string]interface{}{
		"collection": s.Collection,
		"flat":       s.FlatList,
	}
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleSendRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path       string              `json:"path"`
		BodyMode   string              `json:"bodyMode"`
		BodyRaw    string              `json:"bodyRaw"`
		UrlEncoded []models.UrlEncoded `json:"urlencoded"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

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

	if target.Request.Body == nil {
		target.Request.Body = &models.Body{}
	}
	target.Request.Body.Mode = input.BodyMode
	target.Request.Body.Raw = input.BodyRaw
	target.Request.Body.UrlEncoded = input.UrlEncoded

	s.Processor.RunScripts(target.Events, "prerequest", nil, nil, target.Request.Header)


	s.Processor.RunScripts(target.Events, "test", nil, nil, target.Request.Header)

	startTime := javaTimeNow()
	body, headers, statusCode, statusText := s.Client.ExecuteRequest(target.Request)
	duration := javaTimeNow() - startTime
	
	// Record History
	go func() {
		history := s.Storage.LoadHistory()
		record := models.HistoryRecord{
			Timestamp:       javaTimeFromMillis(startTime),
			Path:            target.Path,
			Method:          target.Request.Method,
			URL:             s.Processor.ResolveVariables(target.Request.URL.Raw),
			StatusCode:      statusCode,
			StatusText:      statusText,
			Duration:        duration,
			ResponseBody:    body,
			ResponseHeaders: headers,
		}
		history = append(history, record)
		s.Storage.SaveHistory(history)
	}()

	if body != "" {
		s.Processor.RunScripts(target.Events, "test", []byte(body), headers, target.Request.Header)
	}

	if headers == nil {
		headers = make(map[string][]string)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"body":       body,
		"headers":    headers,
		"statusCode": statusCode,
		"statusText": statusText,
	})
}

func (s *Server) handleHammerRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Path     string `json:"path"`
		Workers  int    `json:"workers"`
		Duration int    `json:"duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

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

	results := s.Client.Hammer(target.Request, input.Workers, time.Duration(input.Duration)*time.Second)

	// Persist Report
	go func() {
		reportDir := filepath.Join(s.Storage.OutputDir, "hammer_reports")
		os.MkdirAll(reportDir, 0755)
		reportPath := filepath.Join(reportDir, fmt.Sprintf("report_%d.json", time.Now().Unix()))
		data, _ := json.MarshalIndent(results, "", "  ")
		ioutil.WriteFile(reportPath, data, 0644)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleGetHammerHistory(w http.ResponseWriter, r *http.Request) {
	reportDir := filepath.Join(s.Storage.OutputDir, "hammer_reports")
	files, _ := ioutil.ReadDir(reportDir)
	var reports []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".json") {
			reports = append(reports, f.Name())
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reports)
}

func (s *Server) handleSQLRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Path      string `json:"path"`
		DBPath    string `json:"db_path"`
		Query     string `json:"query"`
		TargetVar string `json:"targetVar"`
		TargetCol string `json:"targetCol"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Save SQL info to request if path provided
	if input.Path != "" {
		for i, req := range s.FlatList {
			if req.Path == input.Path {
				s.FlatList[i].DBPath = input.DBPath
				s.FlatList[i].SQLQuery = input.Query
				s.Storage.SaveSingleRequest(s.FlatList[i])
				break
			}
		}
	}

	cols, rows, err := s.Client.ExecuteSQL(input.DBPath, input.Query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Extract to variable if requested
	if input.TargetVar != "" && input.TargetCol != "" && len(rows) > 0 {
		colIdx := -1
		for i, c := range cols {
			if strings.EqualFold(c, input.TargetCol) {
				colIdx = i
				break
			}
		}
		if colIdx != -1 {
			s.Storage.VariableMap[input.TargetVar] = rows[0][colIdx]
			s.Storage.SaveVariables()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"columns": cols,
		"rows":    rows,
	})
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		workflows := s.Storage.LoadWorkflows()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(workflows)
		return
	}

	if r.Method == http.MethodPost {
		var workflows []models.Workflow
		if err := json.NewDecoder(r.Body).Decode(&workflows); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		s.Storage.SaveWorkflows(workflows)
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var workflow models.Workflow
	if err := json.NewDecoder(r.Body).Decode(&workflow); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	logs, err := s.Client.RunWorkflow(&workflow, s.FlatList, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleEnvironments(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		envs := s.Storage.LoadEnvironments()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(envs)
		return
	}

	if r.Method == http.MethodPost {
		var envs []models.Environment
		if err := json.NewDecoder(r.Body).Decode(&envs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.Storage.SaveEnvironments(envs)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleActiveEnv(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		id := s.Storage.LoadActiveEnv()
		json.NewEncoder(w).Encode(map[string]string{"id": id})
		return
	}

	if r.Method == http.MethodPost {
		var input struct{ ID string `json:"id"` }
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.Storage.SaveActiveEnv(input.ID)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleImportCurl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Curl string `json:"curl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req := processor.ParseCurl(input.Curl)
	newReq := models.RequestInfo{
		Path:    "Imported > cURL " + time.Now().Format("15:04:05"),
		Request: req,
		Order:   len(s.FlatList),
	}

	s.Storage.SaveSingleRequest(newReq)
	s.FlatList = append(s.FlatList, newReq)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(newReq)
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

	requests, err := processor.ParseOpenAPI([]byte(input.JSON))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, req := range requests {
		req.Order = len(s.FlatList)
		s.Storage.SaveSingleRequest(req)
		s.FlatList = append(s.FlatList, req)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"count": len(requests)})
}

func (s *Server) handleGenerateDocs(w http.ResponseWriter, r *http.Request) {
	var html strings.Builder
	html.WriteString(`<!DOCTYPE html>
<html>
<head>
    <title>PostIt - API Documentation</title>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-page: #f8fafc;
            --bg-sidebar: #ffffff;
            --bg-card: #ffffff;
            --border: #e2e8f0;
            --text-main: #0f172a;
            --text-secondary: #64748b;
            --accent: #ff6c37;
            --accent-soft: #fff1eb;
            --code-bg: #1e293b;
            --code-text: #e2e8f0;
            --method-get: #10b981;
            --method-post: #f59e0b;
            --method-put: #3b82f6;
            --method-delete: #ef4444;
            --method-patch: #8b5cf6;
        }
        body { margin: 0; font-family: 'Inter', sans-serif; background: var(--bg-page); color: var(--text-main); display: flex; height: 100vh; overflow: hidden; }
        
        #sidebar { width: 300px; background: var(--bg-sidebar); border-right: 1px solid var(--border); display: flex; flex-direction: column; overflow-y: auto; }
        #sidebar-header { padding: 24px; border-bottom: 1px solid var(--border); }
        #sidebar-header h1 { margin: 0; font-size: 18px; font-weight: 700; color: var(--accent); }
        .sidebar-item { padding: 12px 24px; font-size: 13px; font-weight: 500; color: var(--text-secondary); cursor: pointer; border-left: 3px solid transparent; transition: all 0.2s; text-decoration: none; display: block; }
        .sidebar-item:hover { background: #f1f5f9; color: var(--text-main); }
        .sidebar-item.active { background: var(--accent-soft); color: var(--accent); border-left-color: var(--accent); }

        #main { flex: 1; overflow-y: auto; padding: 48px; scroll-behavior: smooth; }
        .container { max-width: 900px; margin: 0 auto; }
        
        .req-card { background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px; padding: 32px; margin-bottom: 48px; box-shadow: 0 1px 3px rgba(0,0,0,0.05); }
        .req-header { display: flex; align-items: center; gap: 16px; margin-bottom: 24px; }
        .method-badge { padding: 4px 10px; border-radius: 6px; font-size: 11px; font-weight: 800; text-transform: uppercase; color: white; }
        .method-GET { background: var(--method-get); }
        .method-POST { background: var(--method-post); }
        .method-PUT { background: var(--method-put); }
        .method-DELETE { background: var(--method-delete); }
        .method-PATCH { background: var(--method-patch); }
        .req-path { font-size: 20px; font-weight: 700; color: var(--text-main); }
        
        .url-box { background: #f1f5f9; padding: 12px 16px; border-radius: 8px; font-family: 'JetBrains Mono', monospace; font-size: 13px; color: var(--text-main); margin-bottom: 24px; border: 1px solid var(--border); display: flex; align-items: center; gap: 12px; }
        
        h3 { font-size: 14px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-secondary); margin: 32px 0 16px 0; }
        
        .table { width: 100%; border-collapse: collapse; margin-bottom: 24px; }
        .table th { text-align: left; padding: 12px; border-bottom: 1px solid var(--border); font-size: 12px; color: var(--text-secondary); }
        .table td { padding: 12px; border-bottom: 1px solid var(--border); font-size: 13px; }
        
        pre { background: var(--code-bg); color: var(--code-text); padding: 20px; border-radius: 8px; font-family: 'JetBrains Mono', monospace; font-size: 13px; overflow-x: auto; line-height: 1.6; }
        
        .tabs { display: flex; gap: 24px; border-bottom: 1px solid var(--border); margin-bottom: 16px; }
        .tab { padding: 12px 0; font-size: 13px; font-weight: 600; color: var(--text-secondary); cursor: pointer; border-bottom: 2px solid transparent; }
        .tab.active { color: var(--accent); border-bottom-color: var(--accent); }
    </style>
</head>
<body>
    <div id="sidebar">
        <div id="sidebar-header">
            <h1>API Docs</h1>
        </div>
`)

	for _, req := range s.FlatList {
		id := strings.ReplaceAll(req.Path, " ", "-")
		html.WriteString(fmt.Sprintf("<a href='#%s' class='sidebar-item'>%s</a>", id, req.Path))
	}
	html.WriteString("</div><div id='main'><div class='container'>")

	for _, req := range s.FlatList {
		id := strings.ReplaceAll(req.Path, " ", "-")
		html.WriteString(fmt.Sprintf("<div class='req-card' id='%s'>", id))
		html.WriteString("<div class='req-header'>")
		html.WriteString(fmt.Sprintf("<span class='method-badge method-%s'>%s</span>", req.Request.Method, req.Request.Method))
		html.WriteString(fmt.Sprintf("<span class='req-path'>%s</span>", req.Path))
		html.WriteString("</div>")

		html.WriteString(fmt.Sprintf("<div class='url-box'>%s</div>", req.Request.URL.Raw))

		if len(req.Request.Header) > 0 {
			html.WriteString("<h3>Headers</h3>")
			html.WriteString("<table class='table'><thead><tr><th>Key</th><th>Value</th></tr></thead><tbody>")
			for _, h := range req.Request.Header {
				html.WriteString(fmt.Sprintf("<tr><td><code>%s</code></td><td>%s</td></tr>", h.Key, h.Value))
			}
			html.WriteString("</tbody></table>")
		}

		if req.Request.Body != nil {
			if req.Request.Body.Mode == "raw" && req.Request.Body.Raw != "" {
				html.WriteString("<h3>Request Body (Raw)</h3>")
				html.WriteString("<pre>" + req.Request.Body.Raw + "</pre>")
			} else if req.Request.Body.Mode == "urlencoded" && len(req.Request.Body.UrlEncoded) > 0 {
				html.WriteString("<h3>Request Body (x-www-form-urlencoded)</h3>")
				html.WriteString("<table class='table'><thead><tr><th>Key</th><th>Value</th></tr></thead><tbody>")
				for _, f := range req.Request.Body.UrlEncoded {
					html.WriteString(fmt.Sprintf("<tr><td><code>%s</code></td><td>%s</td></tr>", f.Key, f.Value))
				}
				html.WriteString("</tbody></table>")
			}
		}

		// Add cURL Snippet
		html.WriteString("<h3>cURL Snippet</h3>")
		curl := fmt.Sprintf("curl -X %s \"%s\"", req.Request.Method, req.Request.URL.Raw)
		for _, h := range req.Request.Header {
			curl += fmt.Sprintf(" \\\n  -H \"%s: %s\"", h.Key, h.Value)
		}
		if req.Request.Body != nil && req.Request.Body.Raw != "" {
			curl += fmt.Sprintf(" \\\n  -d '%s'", req.Request.Body.Raw)
		}
		html.WriteString("<pre>" + curl + "</pre>")

		html.WriteString("</div>")
	}

	html.WriteString("</div></div></body></html>")

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html.String()))
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

	s.Storage.SetVaultPassword(input.Password)
	w.WriteHeader(http.StatusOK)
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

	ciphertext, err := s.Storage.Encrypt(input.Plaintext)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden) // Probably locked
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"ciphertext": ciphertext})
}

func (s *Server) handleVaultStatus(w http.ResponseWriter, r *http.Request) {
	unlocked := len(s.Storage.VaultKey) > 0
	json.NewEncoder(w).Encode(map[string]bool{"unlocked": unlocked})
}

func (s *Server) handleProxyStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct { Port int `json:"port"` }
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Port == 0 { input.Port = 8081 }

	if err := s.Proxy.Start(input.Port); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Proxy started on port %d", input.Port)
}

func (s *Server) handleProxyStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := s.Proxy.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Proxy stopped")
}

func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running": s.Proxy.Running,
		"port":    s.Proxy.Server.Addr, // This might be empty if stopped
	})
}

// Helper to match JS Date.now()
func javaTimeNow() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

func javaTimeFromMillis(ms int64) time.Time {
	return time.Unix(0, ms*int64(time.Millisecond))
}

func (s *Server) handleMockStats(w http.ResponseWriter, r *http.Request) {
	s.mockMu.Lock()
	defer s.mockMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.MockStats)
}

func (s *Server) handleWSConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct { URL string `json:"url"` }
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
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct { Message string `json:"message"` }
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

func (s *Server) handleExportHistory(w http.ResponseWriter, r *http.Request) {
	history := s.Storage.LoadHistory()
	data, err := processor.ExportToHAR(history)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=history.har")
	w.Write(data)
}

