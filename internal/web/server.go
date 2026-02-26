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
)

//go:embed static/*
var staticAssets embed.FS

type Server struct {
	Storage    *storage.Manager
	Processor  *processor.ScriptProcessor
	Client     *api.Client
	Collection models.Collection
	FlatList   []models.RequestInfo
}

func NewServer(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client, col models.Collection, flat []models.RequestInfo) *Server {
	return &Server{
		Storage:    store,
		Processor:  proc,
		Client:     client,
		Collection: col,
		FlatList:   flat,
	}
}

func (s *Server) Start(port int) error {
	http.HandleFunc("/api/requests", s.handleGetRequests)
	http.HandleFunc("/api/requests/new", s.handleNewRequest)
	http.HandleFunc("/api/requests/duplicate", s.handleDuplicateRequest)
	http.HandleFunc("/api/requests/update", s.handleUpdateRequest)
	http.HandleFunc("/api/send", s.handleSendRequest)
	http.Handle("/", http.FileServer(http.FS(staticAssets)))

	fmt.Printf("Web UI started at http://localhost:%d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}

func (s *Server) handleUpdateRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		OldPath string          `json:"oldPath"`
		NewPath string          `json:"newPath"`
		Method  string          `json:"method"`
		URL     string          `json:"url"`
		Body    string          `json:"body"`
		Headers []models.Header `json:"headers"`
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
		s.FlatList[targetIdx].Request.Body = &models.Body{Mode: "raw"}
	}
	s.FlatList[targetIdx].Request.Body.Raw = input.Body
	s.FlatList[targetIdx].Request.Header = input.Headers

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
		Path   string `json:"path"`
		Method string `json:"method"`
		URL    string `json:"url"`
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
		},
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
	}

	s.Storage.SaveSingleRequest(newReq)
	s.FlatList = append(s.FlatList, newReq)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newReq)
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
		Path string `json:"path"`
		Body string `json:"body"`
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

	if target.Request.Body != nil && input.Body != "" {
		target.Request.Body.Raw = input.Body
	}

	s.Processor.RunScripts(target.Events, "prerequest", nil, nil, target.Request.Header)
	s.Processor.RunScripts(target.Events, "test", nil, nil, target.Request.Header)

	body, headers := s.Client.ExecuteRequest(target.Request)
	
	if body != "" {
		s.Processor.RunScripts(target.Events, "test", []byte(body), headers, target.Request.Header)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"body": body,
	})
}
