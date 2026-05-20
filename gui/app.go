package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx        context.Context
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

// NewApp creates a new App application struct
func NewApp(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client, col models.Collection, flat []models.RequestInfo, enableMock bool) *App {
	proc.EnablePrompts = false
	return &App{
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

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// --- API Methods ---

func (a *App) GetRequests() map[string]interface{} {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return map[string]interface{}{
		"collection": a.Collection,
		"flat":       a.FlatList,
	}
}

func (a *App) NewRequest(input struct {
	Path             string              `json:"path"`
	Method           string              `json:"method"`
	URL              string              `json:"url"`
	BodyMode         string              `json:"bodyMode"`
	BodyRaw          string              `json:"bodyRaw"`
	UrlEncoded       []models.UrlEncoded `json:"urlencoded"`
	Headers          []models.Header     `json:"headers"`
	PreRequestScript string              `json:"preRequestScript"`
	TestScript       string              `json:"testScript"`
	SQLQuery         string              `json:"sqlQuery"`
	DBPath           string              `json:"dbPath"`
	SQLDriver        string              `json:"sqlDriver"`
	SQLTargetVar     string              `json:"sqlTargetVar"`
	SQLTargetCol     string              `json:"sqlTargetCol"`
	Schema           string              `json:"schema"`
}) (models.RequestInfo, error) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

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
		Order:        len(a.FlatList),
		SQLQuery:     input.SQLQuery,
		DBPath:       input.DBPath,
		SQLDriver:    input.SQLDriver,
		SQLTargetVar: input.SQLTargetVar,
		SQLTargetCol: input.SQLTargetCol,
		Schema:       input.Schema,
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

	if err := a.Storage.SaveSingleRequest(newReq); err != nil {
		return models.RequestInfo{}, err
	}
	a.FlatList = append(a.FlatList, newReq)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return models.RequestInfo{}, err
	}
	
	return newReq, nil
}

func (a *App) UpdateRequest(input struct {
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
	SQLQuery         string              `json:"sqlQuery"`
	DBPath           string              `json:"dbPath"`
	SQLDriver        string              `json:"sqlDriver"`
	SQLTargetVar     string              `json:"sqlTargetVar"`
	SQLTargetCol     string              `json:"sqlTargetCol"`
	Schema           string              `json:"schema"`
}) (models.RequestInfo, error) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	idx := -1
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.OldPath {
			idx = i
			break
		}
	}

	if idx == -1 {
		return models.RequestInfo{}, fmt.Errorf("request not found")
	}

	// If path changed, delete old file
	if input.OldPath != input.NewPath {
		a.Storage.DeleteRequestFile(input.OldPath)
	}

	a.FlatList[idx].Path = input.NewPath
	a.FlatList[idx].Request.Method = input.Method
	a.FlatList[idx].Request.URL.Raw = input.URL
	a.FlatList[idx].Request.Header = input.Headers
	if a.FlatList[idx].Request.Body == nil { a.FlatList[idx].Request.Body = &models.Body{} }
	a.FlatList[idx].Request.Body.Mode = input.BodyMode
	a.FlatList[idx].Request.Body.Raw = input.BodyRaw
	a.FlatList[idx].Request.Body.UrlEncoded = input.UrlEncoded
	
	a.FlatList[idx].SQLQuery = input.SQLQuery
	a.FlatList[idx].DBPath = input.DBPath
	a.FlatList[idx].SQLDriver = input.SQLDriver
	a.FlatList[idx].SQLTargetVar = input.SQLTargetVar
	a.FlatList[idx].SQLTargetCol = input.SQLTargetCol
	a.FlatList[idx].Schema = input.Schema

	a.FlatList[idx].Events = []models.Event{}
	if input.PreRequestScript != "" {
		a.FlatList[idx].Events = append(a.FlatList[idx].Events, models.Event{Listen: "prerequest", Script: models.Script{Type: "text/javascript", Exec: strings.Split(input.PreRequestScript, "\n")}})
	}
	if input.TestScript != "" {
		a.FlatList[idx].Events = append(a.FlatList[idx].Events, models.Event{Listen: "test", Script: models.Script{Type: "text/javascript", Exec: strings.Split(input.TestScript, "\n")}})
	}

	if err := a.Storage.SaveSingleRequest(a.FlatList[idx]); err != nil {
		return models.RequestInfo{}, err
	}
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return models.RequestInfo{}, err
	}
	
	return a.FlatList[idx], nil
}

func (a *App) SendRequest(input struct{ Path string `json:"path"` }) (map[string]interface{}, error) {
	a.stateMu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.stateMu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("request not found")
	}

	a.Processor.RunScripts(target.Events, "prerequest", nil, nil, target.Request.Header)
	body, headers, code, status := a.Client.ExecuteRequest(a.ctx, target.Request)
	a.Processor.RunScripts(target.Events, "test", []byte(body), headers, target.Request.Header)

	resp := map[string]interface{}{
		"body":       body,
		"headers":    headers,
		"statusCode": code,
		"statusText": status,
	}
	return resp, nil
}

func (a *App) GetVariables() map[string]string {
	return a.Storage.GetVariableMapCopy()
}

func (a *App) SaveVariables(vars map[string]string) error {
	for k, v := range vars {
		if err := a.Storage.SetVariable(k, v); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) GetHistory() []models.HistoryRecord {
	return a.Storage.LoadHistory()
}

func (a *App) ClearHistory() error {
	return a.Storage.SaveHistory([]models.HistoryRecord{})
}

func (a *App) DeleteHistory(ts time.Time) error {
	history := a.Storage.LoadHistory()
	newHistory := []models.HistoryRecord{}
	for _, h := range history {
		if !h.Timestamp.Equal(ts) {
			newHistory = append(newHistory, h)
		}
	}
	return a.Storage.SaveHistory(newHistory)
}

func (a *App) ImportCurl(curl string) (models.RequestInfo, error) {
	req := processor.ParseCurl(curl)
	if req == nil {
		return models.RequestInfo{}, fmt.Errorf("invalid cURL command")
	}

	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	reqInfo := models.RequestInfo{
		Path:    "Imported > " + time.Now().Format("15:04:05"),
		Request: req,
		Order:   len(a.FlatList),
	}

	if err := a.Storage.SaveSingleRequest(reqInfo); err != nil {
		return models.RequestInfo{}, err
	}
	a.FlatList = append(a.FlatList, reqInfo)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return models.RequestInfo{}, err
	}

	return reqInfo, nil
}

func (a *App) DuplicateRequest(input struct {
	Path    string `json:"path"`
	NewPath string `json:"newPath"`
}) (models.RequestInfo, error) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}

	if target == nil {
		return models.RequestInfo{}, fmt.Errorf("request not found")
	}

	maxOrder := -1
	for _, r := range a.FlatList {
		if r.Order > maxOrder {
			maxOrder = r.Order
		}
	}

	newReq := models.RequestInfo{
		Path:    input.NewPath,
		Request: target.Request.DeepCopy(),
		Events:  append([]models.Event{}, target.Events...),
		Responses: append([]models.MockResponse{}, target.Responses...),
		Order:   maxOrder + 1,
		SQLQuery:     target.SQLQuery,
		DBPath:       target.DBPath,
		SQLDriver:    target.SQLDriver,
		SQLTargetVar: target.SQLTargetVar,
		SQLTargetCol: target.SQLTargetCol,
		Schema:       target.Schema,
	}

	if err := a.Storage.SaveSingleRequest(newReq); err != nil {
		return models.RequestInfo{}, err
	}
	a.FlatList = append(a.FlatList, newReq)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return models.RequestInfo{}, err
	}

	return newReq, nil
}

func (a *App) DuplicateFolder(input struct {
	Path    string `json:"path"`
	NewPath string `json:"newPath"`
}) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	affected := false
	oldPrefix := input.Path + " > "
	
	maxOrder := -1
	for _, r := range a.FlatList {
		if r.Order > maxOrder {
			maxOrder = r.Order
		}
	}

	newRequests := []models.RequestInfo{}
	for _, req := range a.FlatList {
		if req.Path == input.Path || strings.HasPrefix(req.Path, oldPrefix) {
			newReq := models.RequestInfo{
				Path:      input.NewPath + strings.TrimPrefix(req.Path, input.Path),
				Request:   req.Request.DeepCopy(),
				Responses: append([]models.MockResponse{}, req.Responses...),
				Events:    append([]models.Event{}, req.Events...),
				Order:     maxOrder + 1,
				SQLQuery:     req.SQLQuery,
				DBPath:       req.DBPath,
				SQLDriver:    req.SQLDriver,
				SQLTargetVar: req.SQLTargetVar,
				SQLTargetCol: req.SQLTargetCol,
				Schema:       req.Schema,
			}
			maxOrder++
			newRequests = append(newRequests, newReq)
			affected = true
		}
	}

	if !affected {
		return fmt.Errorf("no items found in folder %s", input.Path)
	}

	for _, req := range newRequests {
		if err := a.Storage.SaveSingleRequest(req); err != nil {
			fmt.Printf("Error saving duplicated request %s: %v\n", req.Path, err)
		}
		a.FlatList = append(a.FlatList, req)
	}

	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return err
	}

	return nil
}

func (a *App) DeleteRequest(input struct{ Path string `json:"path"` }) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	found := false
	oldPrefix := input.Path + " > "
	
	newFlatList := []models.RequestInfo{}
	for _, req := range a.FlatList {
		if req.Path == input.Path || strings.HasPrefix(req.Path, oldPrefix) {
			a.Storage.DeleteRequestFile(req.Path)
			found = true
		} else {
			newFlatList = append(newFlatList, req)
		}
	}

	if !found {
		return fmt.Errorf("not found")
	}

	a.FlatList = newFlatList
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return err
	}
	return nil
}

func (a *App) RenameFolder(input struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	affected := false
	oldPrefix := input.OldPath + " > "
	
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.OldPath || strings.HasPrefix(a.FlatList[i].Path, oldPrefix) {
			oldReqPath := a.FlatList[i].Path
			newReqPath := ""
			if oldReqPath == input.OldPath {
				newReqPath = input.NewPath
			} else {
				newReqPath = input.NewPath + strings.TrimPrefix(oldReqPath, input.OldPath)
			}

			// Delete old file
			a.Storage.DeleteRequestFile(oldReqPath)

			// Update path and save new file
			a.FlatList[i].Path = newReqPath
			if err := a.Storage.SaveSingleRequest(a.FlatList[i]); err != nil {
				fmt.Printf("Error saving renamed request %s: %v\n", newReqPath, err)
			}
			affected = true
		}
	}

	if !affected {
		return fmt.Errorf("no items found in folder %s", input.OldPath)
	}

	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return err
	}

	return nil
}

func (a *App) ReorderRequests(input struct {
	Paths []string `json:"paths"`
}) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	pathMap := make(map[string]int)
	for i, path := range input.Paths {
		pathMap[path] = i
	}

	for i := range a.FlatList {
		if order, ok := pathMap[a.FlatList[i].Path]; ok {
			a.FlatList[i].Order = order
		}
	}

	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return err
	}
	return nil
}

func (a *App) GetWorkflows() []models.Workflow {
	return a.Storage.GetWorkflows()
}

func (a *App) SaveWorkflows(wfs []models.Workflow) error {
	return a.Storage.SaveWorkflows(wfs)
}

func (a *App) RunWorkflow(wf models.Workflow) ([]api.WorkflowLog, error) {
	return a.Client.RunWorkflow(a.ctx, &wf, a.FlatList, "")
}

func (a *App) GetEnvironments() []models.Environment {
	return a.Storage.GetEnvironments()
}

func (a *App) SaveEnvironments(envs []models.Environment) error {
	return a.Storage.SaveEnvironments(envs)
}

func (a *App) GetActiveEnv() map[string]string {
	return map[string]string{
		"activeEnvId": a.Storage.GetActiveEnvID(),
	}
}

func (a *App) SetActiveEnv(input struct{ ID string `json:"id"` }) error {
	return a.Storage.SetActiveEnvID(input.ID)
}

func (a *App) UnlockVault(input struct{ Password string `json:"password"` }) {
	a.Storage.SetVaultKey(input.Password)
}

func (a *App) GetVaultStatus() map[string]bool {
	return map[string]bool{"unlocked": a.Storage.IsVaultUnlocked()}
}

func (a *App) VaultEncrypt(input struct{ Plaintext string `json:"plaintext"` }) (struct{ Ciphertext string `json:"ciphertext"` }, error) {
	ct, err := a.Storage.Encrypt(input.Plaintext)
	return struct{ Ciphertext string `json:"ciphertext"` }{Ciphertext: ct}, err
}

func (a *App) ImportOpenAPI(input struct{ JSON string `json:"json"` }) (struct{ Count int `json:"count"` }, error) {
	requests, err := processor.ParseOpenAPI([]byte(input.JSON))
	if err != nil {
		return struct{ Count int `json:"count"` }{}, err
	}

	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	orderCounter := len(a.FlatList)
	for _, fresh := range requests {
		fresh.Order = orderCounter
		orderCounter++
		a.FlatList = append(a.FlatList, fresh)
		a.Storage.SaveSingleRequest(fresh)
	}

	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return struct{ Count int `json:"count"` }{}, err
	}

	return struct{ Count int `json:"count"` }{Count: len(requests)}, nil
}

func (a *App) ImportPostman(input struct{ JSON string `json:"json"` }) (struct{ Count int `json:"count"` }, error) {
	_, requests, err := processor.ParsePostmanCollection([]byte(input.JSON))
	if err != nil {
		return struct{ Count int `json:"count"` }{}, err
	}

	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	orderCounter := len(a.FlatList)
	for _, fresh := range requests {
		fresh.Order = orderCounter
		orderCounter++
		a.FlatList = append(a.FlatList, fresh)
		a.Storage.SaveSingleRequest(fresh)
	}

	a.Collection.Item = models.ReconstructItems(a.FlatList)
	if err := a.Storage.SaveCollection(a.Collection); err != nil {
		return struct{ Count int `json:"count"` }{}, err
	}

	return struct{ Count int `json:"count"` }{Count: len(requests)}, nil
}

func (a *App) HammerRequest(input struct {
	Path    string `json:"path"`
	Workers int    `json:"workers"`
	Seconds int    `json:"seconds"`
}) (*api.HammerResults, error) {
	a.stateMu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.stateMu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("not found")
	}

	return a.Client.Hammer(target.Request, input.Workers, time.Duration(input.Seconds)*time.Second), nil
}

func (a *App) SQLRequest(input struct {
	Driver  string `json:"driver"`
	ConnStr string `json:"connStr"`
	Query   string `json:"query"`
}) (map[string]interface{}, error) {
	cols, rows, err := a.Client.ExecuteSQL(a.ctx, input.Driver, input.ConnStr, input.Query)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"columns": cols, "rows": rows}, nil
}

func (a *App) StartProxy(input struct{ Port int `json:"port"` }) error {
	return a.Proxy.Start(input.Port)
}

func (a *App) StopProxy() {
	a.Proxy.Stop()
}

func (a *App) GetProxyStatus() struct{ Running bool `json:"running"` } {
	return struct{ Running bool `json:"running"` }{Running: a.Proxy.IsRunning()}
}

func (a *App) FuzzRequest(input struct{ Path string `json:"path"` }) ([]api.FuzzResult, error) {
	a.stateMu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.stateMu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("not found")
	}

	return a.fuzzer.Run(a.ctx, *target, a.Storage.GetVariableMapCopy())
}

func (a *App) RunRunner(input struct {
	Path string              `json:"path"`
	Data []map[string]string `json:"data"`
}) []api.RunnerResult {
	a.stateMu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.stateMu.RUnlock()

	if target == nil {
		return nil
	}

	if input.Data == nil {
		input.Data = []map[string]string{{}}
	}

	return a.runner.RunIteration(a.ctx, *target, input.Data)
}

func (a *App) WSConnect(input struct{ URL string `json:"url"` }) error {
	return a.WSClient.Connect(input.URL)
}

func (a *App) WSSend(input struct{ Message string `json:"message"` }) error {
	return a.WSClient.Send(input.Message)
}

func (a *App) WSGetMessages() []api.WSMessage {
	return a.WSClient.GetMessages()
}

func (a *App) WSClose() {
	a.WSClient.Close()
}

func (a *App) SaveMockResponse(input struct {
	Path      string               `json:"path"`
	Name      string               `json:"name"`
	Code      int                  `json:"code"`
	Status    string               `json:"status"`
	Body      string               `json:"body"`
	Headers   []models.Header      `json:"headers"`
	Condition string               `json:"condition"`
	Delay     int                  `json:"delay"`
}) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	idx := -1
	for i, req := range a.FlatList {
		if req.Path == input.Path {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("request not found")
	}

	mock := models.MockResponse{
		Name:      input.Name,
		Code:      input.Code,
		Status:    input.Status,
		Body:      input.Body,
		Header:    input.Headers,
		Condition: input.Condition,
		Delay:     input.Delay,
	}

	// Update existing or add new
	found := false
	for i, existing := range a.FlatList[idx].Responses {
		if existing.Name == input.Name {
			a.FlatList[idx].Responses[i] = mock
			found = true
			break
		}
	}
	if !found {
		a.FlatList[idx].Responses = append(a.FlatList[idx].Responses, mock)
	}

	return a.Storage.SaveSingleRequest(a.FlatList[idx])
}

func (a *App) DeleteMock(input struct {
	Path     string `json:"path"`
	MockName string `json:"mockName"`
}) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	idx := -1
	for i, req := range a.FlatList {
		if req.Path == input.Path {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("request not found")
	}

	for i, m := range a.FlatList[idx].Responses {
		if m.Name == input.MockName {
			a.FlatList[idx].Responses = append(a.FlatList[idx].Responses[:i], a.FlatList[idx].Responses[i+1:]...)
			return a.Storage.SaveSingleRequest(a.FlatList[idx])
		}
	}
	return fmt.Errorf("mock not found")
}

func (a *App) GetMockStats() map[string]*models.MockStat {
	a.mockMu.RLock()
	defer a.mockMu.RUnlock()
	stats := make(map[string]*models.MockStat)
	for k, v := range a.mockStats {
		stats[k] = v
	}
	return stats
}

func (a *App) SaveSchema(input struct {
	Path   string `json:"path"`
	Schema string `json:"schema"`
}) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			a.FlatList[i].Schema = input.Schema
			return a.Storage.SaveSingleRequest(a.FlatList[i])
		}
	}
	return fmt.Errorf("request not found")
}

func (a *App) GraphQLIntrospection(input struct{ URL string `json:"url"` }) (map[string]interface{}, error) {
	return nil, nil // Stub
}

func (a *App) ExportPostman(path string) error {
	a.stateMu.RLock()
	requests := make([]models.RequestInfo, len(a.FlatList))
	copy(requests, a.FlatList)
	a.stateMu.RUnlock()

	collection := api.ExportPostman(requests, path)
	data, err := json.MarshalIndent(collection, "", "  ")
	if err != nil {
		return err
	}

	filename := "collection.json"
	if path != "" {
		filename = strings.ReplaceAll(path, " > ", "_") + ".postman_collection.json"
	}

	selectedFile, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: filename,
		Filters: []runtime.FileFilter{
			{DisplayName: "Postman Collection (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return err
	}
	if selectedFile == "" {
		return nil // User cancelled
	}

	return os.WriteFile(selectedFile, data, 0644)
}

