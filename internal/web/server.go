package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"time"
)

//go:embed static/*
var staticAssets embed.FS

type Server struct {
	Storage    *storage.Manager
	Processor  *processor.ScriptProcessor
	Client     *api.Client
	Collection models.Collection
	FlatList   []models.RequestInfo
	EnableMock bool
}

func NewServer(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client, col models.Collection, flat []models.RequestInfo, enableMock bool) *Server {
	return &Server{
		Storage:    store,
		Processor:  proc,
		Client:     client,
		Collection: col,
		FlatList:   flat,
		EnableMock: enableMock,
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
	http.HandleFunc("/api/send", s.handleSendRequest)
	http.HandleFunc("/api/hammer", s.handleHammerRequest)
	http.HandleFunc("/api/sql", s.handleSQLRequest)
	http.HandleFunc("/api/mock/save", s.handleSaveMockResponse)
	http.HandleFunc("/api/workflows", s.handleWorkflows)
	http.HandleFunc("/api/workflows/run", s.handleRunWorkflow)
	
	if s.EnableMock {
		http.HandleFunc("/mock/", s.handleMockRequest)
		fmt.Printf("Mock server enabled at http://localhost:%d/mock/\n", port)
	} else {
		http.HandleFunc("/mock/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "Mock server is disabled. Start with -mock flag to enable it.")
		})
	}

	http.Handle("/", http.FileServer(http.FS(staticAssets)))

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

	fmt.Printf("Mock request received: %s %s\n", r.Method, mockPath)

	for _, reqInfo := range s.FlatList {
		// Basic path matching: resolve variables in saved URL and compare paths
		resolvedURL := s.Processor.ResolveVariables(reqInfo.Request.URL.Raw)
		
		// Remove protocol and host to compare just the path
		savedPath := resolvedURL
		if idx := strings.Index(savedPath, "://"); idx != -1 {
			savedPath = savedPath[idx+3:]
		}
		if idx := strings.Index(savedPath, "/"); idx != -1 {
			savedPath = savedPath[idx:]
		} else {
			savedPath = "/"
		}

		// Simple matching (ignoring query params for now)
		savedPathOnly := strings.Split(savedPath, "?")[0]
		incomingPathOnly := strings.Split(mockPath, "?")[0]

		if savedPathOnly == incomingPathOnly && strings.EqualFold(reqInfo.Request.Method, r.Method) {
			if len(reqInfo.Responses) > 0 {
				// For now, return the first mock response
				mock := reqInfo.Responses[0]
				
				for _, h := range mock.Header {
					w.Header().Add(h.Key, s.Processor.ResolveVariables(h.Value))
				}
				
				w.WriteHeader(mock.Code)
				w.Write([]byte(s.Processor.ResolveVariables(mock.Body)))
				return
			}
		}
	}

	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "No mock response found for %s %s", r.Method, mockPath)
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
		Urlencoded       []models.Urlencoded `json:"urlencoded"`
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
	s.FlatList[targetIdx].Request.Body.Urlencoded = input.Urlencoded
	
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
		Urlencoded       []models.Urlencoded `json:"urlencoded"`
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
				Urlencoded: input.Urlencoded,
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
		Urlencoded []models.Urlencoded `json:"urlencoded"`
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
	target.Request.Body.Urlencoded = input.Urlencoded

	s.Processor.RunScripts(target.Events, "prerequest", nil, nil, target.Request.Header)


	s.Processor.RunScripts(target.Events, "test", nil, nil, target.Request.Header)

	startTime := javaTimeNow()
	body, headers, statusCode, statusText := s.Client.ExecuteRequest(target.Request)
	duration := javaTimeNow() - startTime
	
	// Record History
	go func() {
		history := s.Storage.LoadHistory()
		record := models.HistoryRecord{
			Timestamp:  javaTimeFromMillis(startTime),
			Path:       target.Path,
			Method:     target.Request.Method,
			URL:        s.Processor.ResolveVariables(target.Request.URL.Raw),
			StatusCode: statusCode,
			StatusText: statusText,
			Duration:   duration,
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleSQLRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		Path   string `json:"path"`
		DBPath string `json:"db_path"`
		Query  string `json:"query"`
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

	logs, err := s.Client.RunWorkflow(&workflow, s.FlatList)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

// Helper to match JS Date.now()
func javaTimeNow() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

func javaTimeFromMillis(ms int64) time.Time {
	return time.Unix(0, ms*int64(time.Millisecond))
}

