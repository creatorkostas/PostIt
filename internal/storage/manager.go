package storage

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"postit/internal/models"
	"regexp"
	"strings"
)

type Manager struct {
	OutputDir     string
	VarsPath      string
	HeadersPath   string
	HistoryPath   string
	WorkflowsPath string
	VariableMap   map[string]string
	GlobalHeaders []models.Header
}

func NewManager(outputDir string) *Manager {
	return &Manager{
		OutputDir:     outputDir,
		VarsPath:      filepath.Join(outputDir, "variables.json"),
		HeadersPath:   filepath.Join(outputDir, "global_headers.json"),
		HistoryPath:   filepath.Join(outputDir, "history.json"),
		WorkflowsPath: filepath.Join(outputDir, "workflows.json"),
		VariableMap:   make(map[string]string),
		GlobalHeaders: []models.Header{},
	}
}

func (m *Manager) LoadWorkflows() []models.Workflow {
	var workflows []models.Workflow
	if data, err := ioutil.ReadFile(m.WorkflowsPath); err == nil {
		json.Unmarshal(data, &workflows)
	}
	return workflows
}

func (m *Manager) SaveWorkflows(workflows []models.Workflow) {
	data, _ := json.MarshalIndent(workflows, "", "  ")
	ioutil.WriteFile(m.WorkflowsPath, data, 0644)
}

func (m *Manager) Init() error {
	if _, err := os.Stat(m.OutputDir); os.IsNotExist(err) {
		os.Mkdir(m.OutputDir, 0755)
	}
	m.LoadVariables()
	m.LoadGlobalHeaders()
	return nil
}

func (m *Manager) LoadHistory() []models.HistoryRecord {
	var history []models.HistoryRecord
	if data, err := ioutil.ReadFile(m.HistoryPath); err == nil {
		json.Unmarshal(data, &history)
	}
	return history
}

func (m *Manager) SaveHistory(history []models.HistoryRecord) {
	// Keep last 50
	if len(history) > 50 {
		history = history[len(history)-50:]
	}
	data, _ := json.MarshalIndent(history, "", "  ")
	ioutil.WriteFile(m.HistoryPath, data, 0644)
}

func (m *Manager) LoadVariables() {
	if data, err := ioutil.ReadFile(m.VarsPath); err == nil {
		json.Unmarshal(data, &m.VariableMap)
	}
}

func (m *Manager) SaveVariables() {
	data, _ := json.MarshalIndent(m.VariableMap, "", "  ")
	ioutil.WriteFile(m.VarsPath, data, 0644)
}

func (m *Manager) SetVariable(key, value string) {
	m.VariableMap[key] = value
	m.SaveVariables()
}

func (m *Manager) LoadGlobalHeaders() {
	if data, err := ioutil.ReadFile(m.HeadersPath); err == nil {
		json.Unmarshal(data, &m.GlobalHeaders)
	}
}

func (m *Manager) SaveGlobalHeaders() {
	data, _ := json.MarshalIndent(m.GlobalHeaders, "", "  ")
	ioutil.WriteFile(m.HeadersPath, data, 0644)
}

func (m *Manager) LoadCache() map[string]models.RequestInfo {
	cache := make(map[string]models.RequestInfo)
	files, _ := ioutil.ReadDir(m.OutputDir)
	for _, file := range files {
		name := file.Name()
		if !file.IsDir() && strings.HasSuffix(name, ".json") && 
		   name != "variables.json" && name != "global_headers.json" {
			data, _ := ioutil.ReadFile(filepath.Join(m.OutputDir, name))
			var req models.RequestInfo
			if err := json.Unmarshal(data, &req); err == nil {
				cache[req.Path] = req
			}
		}
	}
	return cache
}

func (m *Manager) SaveSingleRequest(req models.RequestInfo) {
	filename := getSafeFilename(req.Path)
	data, _ := json.MarshalIndent(req, "", "    ")
	ioutil.WriteFile(filepath.Join(m.OutputDir, filename), data, 0644)
}

func (m *Manager) DeleteRequestFile(path string) {
	filename := getSafeFilename(path)
	os.Remove(filepath.Join(m.OutputDir, filename))
}

func getSafeFilename(path string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	safe := reg.ReplaceAllString(path, "_")
	regMulti := regexp.MustCompile(`_+`)
	safe = regMulti.ReplaceAllString(safe, "_")
	return strings.Trim(safe, "_") + ".json"
}
