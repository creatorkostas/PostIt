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
	http.HandleFunc("/api/send", s.handleSendRequest)
	http.Handle("/", http.FileServer(http.FS(staticAssets)))

	fmt.Printf("Web UI started at http://localhost:%d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
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
