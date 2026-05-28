package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"postit/internal/api"
	"postit/internal/assets"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// ─── Wails App Struct ──────────────────────────────────────────────────────────

// App is the main Wails-bound struct. Its exported methods become available
// as window.go.main.App.* in the frontend. All methods delegate to the same
// internal packages used by the web server handlers.
type App struct {
	ctx        context.Context
	Storage    *storage.Manager
	Processor  *processor.ScriptProcessor
	Client     *api.Client
	Proxy      *api.ProxyServer
	Collection models.Collection
	FlatList   []models.RequestInfo
	EnableMock bool

	mockStats map[string]*models.MockStat
	mockMu    sync.RWMutex
	fuzzer    *api.Fuzzer
	runner    *api.Runner
	WSClient  *api.WSClient
	Kafka     *api.KafkaProducer

	mu       sync.RWMutex // protects Collection & FlatList
	httpPort int
	tmpDir   string
}

// NewApp creates a Wails App pre-wired with all backend dependencies.
func NewApp(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client,
	col models.Collection, flat []models.RequestInfo, enableMock bool) *App {
	proc.EnablePrompts = false // disable interactive prompts in GUI mode
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
		Kafka:      api.NewKafkaProducer(),
		fuzzer:     api.NewFuzzer(),
		runner:     api.NewRunner(client, store, proc),
	}
}

// ─── Lifecycle ─────────────────────────────────────────────────────────────────

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Find available TCP port for the internal HTTP proxy server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	a.httpPort = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Build an HTTP mux with the same routes as the web server so the frontend's
	// fetch() based calls work in Wails mode too.
	mux := http.NewServeMux()

	// Helper: register a route wrapped with recovery + CSRF middleware
	route := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, recoveryMiddleware(csrfMiddleware(handler)))
	}
	bodyLimitRoute := func(pattern string, handler http.HandlerFunc, limit int64) {
		mux.HandleFunc(pattern, recoveryMiddleware(csrfMiddleware(withBodyLimit(handler, limit))))
	}

	// ── Request CRUD ──────────────────────────────────────────────────────────
	route("/api/requests", a.handleGetRequests)
	bodyLimitRoute("/api/requests/new", a.handleNewRequest, 10<<20)
	route("/api/requests/duplicate", a.handleDuplicateRequest)
	bodyLimitRoute("/api/requests/update", a.handleUpdateRequest, 10<<20)
	route("/api/requests/delete", a.handleDeleteRequest)
	route("/api/requests/reorder", a.handleReorderRequest)

	// ── Data ──────────────────────────────────────────────────────────────────
	route("/api/variables", a.handleVariables)
	route("/api/history", a.handleGetHistory)
	route("/api/history/clear", a.handleClearHistory)
	route("/api/history/delete", a.handleDeleteHistory)
	route("/api/send", a.handleSendRequest)
	route("/api/hammer", a.handleHammerRequest)
	route("/api/sql", a.handleSQLRequest)

	// ── Schema ────────────────────────────────────────────────────────────────
	route("/api/schema/generate", a.handleSchemaGenerate)
	route("/api/schema/validate", a.handleSchemaValidate)
	route("/api/schema/save", a.handleSchemaSave)

	// ── Mock ──────────────────────────────────────────────────────────────────
	route("/api/mock/save", a.handleSaveMockResponse)
	route("/api/mock/delete", a.handleDeleteMock)
	route("/api/mock/stats", a.handleMockStats)

	// ── Fuzzer / Runner / GraphQL ─────────────────────────────────────────────
	route("/api/fuzz", a.handleFuzzRequest)
	route("/api/runner/run", a.handleRunnerRun)
	route("/api/graphql/introspection", a.handleGraphQLIntrospection)

	// ── Imports ───────────────────────────────────────────────────────────────
	bodyLimitRoute("/api/import/curl", a.handleImportCurl, 10<<20)
	bodyLimitRoute("/api/import/openapi", a.handleImportOpenAPI, 50<<20)
	bodyLimitRoute("/api/import/postman", a.handleImportPostman, 50<<20)

	// ── Workflows ─────────────────────────────────────────────────────────────
	route("/api/workflows", a.handleWorkflows)
	bodyLimitRoute("/api/workflows/run", a.handleRunWorkflow, 10<<20)

	// ── Environments ──────────────────────────────────────────────────────────
	route("/api/environments", a.handleEnvironments)
	route("/api/environments/active", a.handleActiveEnv)

	// ── Vault ─────────────────────────────────────────────────────────────────
	route("/api/vault/unlock", a.handleUnlockVault)
	route("/api/vault/encrypt", a.handleVaultEncrypt)
	route("/api/vault/status", a.handleVaultStatus)

	// ── WebSocket ─────────────────────────────────────────────────────────────
	route("/api/ws/connect", a.handleWSConnect)
	route("/api/ws/send", a.handleWSSend)
	route("/api/ws/messages", a.handleWSMessages)
	route("/api/ws/close", a.handleWSClose)

	// ── Proxy ─────────────────────────────────────────────────────────────────
	route("/api/proxy/start", a.handleProxyStart)
	route("/api/proxy/stop", a.handleProxyStop)
	route("/api/proxy/status", a.handleProxyStatus)

	// ── Kafka ─────────────────────────────────────────────────────────────────
	route("/api/kafka/connect", a.handleKafkaConnect)
	route("/api/kafka/send", a.handleKafkaSend)
	route("/api/kafka/topics", a.handleKafkaTopics)
	route("/api/kafka/topics/", a.handleKafkaTopicMeta)
	route("/api/kafka/status", a.handleKafkaStatus)
	route("/api/kafka/disconnect", a.handleKafkaDisconnect)
	route("/api/kafka/configs", a.handleKafkaConfigs)

	// ── Export ────────────────────────────────────────────────────────────────
	route("/api/export", a.handleExportCollection)
	route("/api/history/export", a.handleExportHistory)

	// ── Docs ──────────────────────────────────────────────────────────────────
	route("/api/docs/generate", a.handleGenerateDocs)

	// ── Mock server (optional) ────────────────────────────────────────────────
	if a.EnableMock {
		mux.HandleFunc("/mock/", recoveryMiddleware(csrfMiddleware(a.handleMockRequest)))
	}

	// ── Static frontend files ─────────────────────────────────────────────────
	mux.Handle("/", a.frontendFileServer())

	// Start the proxy HTTP server in the background
	httpSrv := &http.Server{
		Addr:        fmt.Sprintf("127.0.0.1:%d", a.httpPort),
		Handler:     mux,
		ReadTimeout: 15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
	go func() {
		httpSrv.ListenAndServe()
	}()
}

func (a *App) shutdown(ctx context.Context) {
	if a.tmpDir != "" {
		os.RemoveAll(a.tmpDir)
	}
	if a.Client != nil {
		a.Client.Close()
	}
}

func (a *App) frontendFileServer() http.Handler {
	subFS, err := fs.Sub(assets.FS, "frontend")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(subFS))
}

// ─── WailsRun ──────────────────────────────────────────────────────────────────

// WailsRun starts the native Wails desktop GUI. It starts an internal HTTP
// server for the frontend's fetch() calls and opens the Wails webview window.
func WailsRun(store *storage.Manager, proc *processor.ScriptProcessor,
	client *api.Client, col models.Collection, flat []models.RequestInfo,
	enableMock bool) error {

	app := NewApp(store, proc, client, col, flat, enableMock)

	return wails.Run(&options.App{
		Title:     "PostIt",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Handler: app.httpProxyHandler(),
		},
		BackgroundColour: &options.RGBA{R: 30, G: 30, B: 30, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
}

// httpProxyHandler returns an http.Handler that proxies all requests to the
// internal HTTP server running on a random localhost port. This ensures the
// frontend's fetch() calls (for /api/…) work identically in both Wails and
// web-browser mode without code changes.
func (a *App) httpProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetURL := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("127.0.0.1:%d", a.httpPort),
		}
		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		proxy.ServeHTTP(w, r)
	})
}

// ─── Middleware (inlined from server.go) ────────────────────────────────────────

func recoveryMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				http.Error(w, fmt.Sprintf("panic: %v", rec), http.StatusInternalServerError)
			}
		}()
		next(w, r)
	}
}

func csrfMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// In Wails mode requests originate from localhost – allow all origins.
		if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		if origin != "" && !strings.HasPrefix(origin, "http://127.0.0.1") &&
			!strings.HasPrefix(origin, "http://localhost") &&
			!strings.HasPrefix(origin, "wails://") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func withBodyLimit(next http.HandlerFunc, maxBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next(w, r)
	}
}

// ─── JSON helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ─── Wails-Bound Methods ───────────────────────────────────────────────────────
//
// These exported methods are bound to window.go.main.App.* and called by the
// frontend's wailsCall() dispatch. Each method duplicates the handler logic
// from internal/web/server.go by calling the same internal packages.

// -- No-arg methods ------------------------------------------------------------

func (a *App) GetRequests() (interface{}, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return map[string]interface{}{
		"collection": a.Collection,
		"flat":       a.FlatList,
	}, nil
}

func (a *App) GetVariables() (interface{}, error) {
	return a.Storage.GetVariableMapCopy(), nil
}

func (a *App) GetHistory() (interface{}, error) {
	return a.Storage.LoadHistory(), nil
}

func (a *App) ClearHistory() (interface{}, error) {
	err := a.Storage.SaveHistory([]models.HistoryRecord{})
	return nil, err
}

func (a *App) GetMockStats() (interface{}, error) {
	a.mockMu.RLock()
	defer a.mockMu.RUnlock()
	return a.mockStats, nil
}

func (a *App) GetProxyStatus() (interface{}, error) {
	return map[string]bool{"running": a.Proxy.IsRunning()}, nil
}

func (a *App) GetVaultStatus() (interface{}, error) {
	return map[string]bool{"unlocked": a.Storage.IsVaultUnlocked()}, nil
}

func (a *App) GetWorkflows() (interface{}, error) {
	return a.Storage.GetWorkflows(), nil
}

func (a *App) WSClose() (interface{}, error) {
	a.WSClient.Close()
	return nil, nil
}

func (a *App) WSGetMessages() (interface{}, error) {
	return a.WSClient.GetMessages(), nil
}

func (a *App) StopProxy() (interface{}, error) {
	err := a.Proxy.Stop()
	return map[string]bool{"running": false}, err
}

// ── Kafka (Wails-bound) ────────────────────────────────────────────────────────

func (a *App) KafkaConnect(data map[string]interface{}) (interface{}, error) {
	cfg := parseKafkaConfig(data)
	if cfg.ClientID == "" {
		cfg.ClientID = "postit"
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 10
	}
	err := a.Kafka.Connect(cfg)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"connected": true,
		"brokers":   cfg.Brokers,
	}, nil
}

func (a *App) KafkaSend(data map[string]interface{}) (interface{}, error) {
	msg := models.KafkaMessage{
		Topic: toString(data["topic"]),
		Key:   toString(data["key"]),
		Value: toString(data["value"]),
	}

	if p, ok := data["partition"]; ok {
		msg.Partition = toInt(p, -1)
	} else {
		msg.Partition = -1
	}

	// Parse headers
	if rawHeaders, ok := data["headers"].(map[string]interface{}); ok {
		msg.Headers = make(map[string]string, len(rawHeaders))
		for k, v := range rawHeaders {
			msg.Headers[k] = toString(v)
		}
	}

	if msg.Topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	result, err := a.Kafka.SendMessage(context.Background(), msg)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *App) KafkaGetTopics(data map[string]interface{}) (interface{}, error) {
	brokers := parseStringSlice(data["brokers"])
	if len(brokers) == 0 {
		// Accept comma-separated string as well
		b, _ := data["brokers"].(string)
		if b != "" {
			brokers = strings.Split(b, ",")
		}
	}
	if len(brokers) == 0 {
		return nil, fmt.Errorf("brokers are required")
	}

	cfg := api.KafkaConfigDefaults()
	cfg.Brokers = brokers
	topics, err := a.Kafka.GetTopics(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"topics": topics}, nil
}

func (a *App) KafkaGetTopicMeta(data map[string]interface{}) (interface{}, error) {
	topic, _ := data["topic"].(string)
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	brokers := parseStringSlice(data["brokers"])
	if len(brokers) == 0 {
		b, _ := data["brokers"].(string)
		if b != "" {
			brokers = strings.Split(b, ",")
		}
	}
	if len(brokers) == 0 {
		return nil, fmt.Errorf("brokers are required")
	}

	cfg := api.KafkaConfigDefaults()
	cfg.Brokers = brokers
	partitions, err := a.Kafka.GetTopicMetadata(context.Background(), cfg, topic)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"topic":      topic,
		"partitions": partitions,
	}, nil
}

func (a *App) KafkaStatus() (interface{}, error) {
	return map[string]bool{"connected": a.Kafka.IsConnected()}, nil
}

func (a *App) KafkaDisconnect() (interface{}, error) {
	a.Kafka.Close()
	return map[string]bool{"connected": false}, nil
}

func (a *App) KafkaGetConfigs() (interface{}, error) {
	return a.Storage.GetKafkaConnections(), nil
}

func (a *App) KafkaSaveConfig(data map[string]interface{}) (interface{}, error) {
	conn := parseKafkaConnection(data)
	if conn.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return nil, a.Storage.AddKafkaConnection(conn)
}

func (a *App) KafkaDeleteConfig(data map[string]interface{}) (interface{}, error) {
	id, _ := data["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	return nil, a.Storage.DeleteKafkaConnection(id)
}

func (a *App) KafkaTestConnection(data map[string]interface{}) (interface{}, error) {
	cfg := parseKafkaConfig(data)
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 10
	}
	err := a.Kafka.TestConnection(context.Background(), cfg)
	if err != nil {
		return map[string]interface{}{"success": false, "error": err.Error()}, nil
	}
	return map[string]interface{}{"success": true}, nil
}

func (a *App) GetEnvironments() (interface{}, error) {
	return a.Storage.GetEnvironments(), nil
}

func (a *App) GetActiveEnv() (interface{}, error) {
	return map[string]string{"activeEnvId": a.Storage.GetActiveEnvID()}, nil
}

// -- Single-arg methods --------------------------------------------------------

func (a *App) NewRequest(data map[string]interface{}) (interface{}, error) {
	path, _ := data["path"].(string)
	method, _ := data["method"].(string)
	urlStr, _ := data["url"].(string)
	bodyMode, _ := data["bodyMode"].(string)
	bodyRaw, _ := data["bodyRaw"].(string)
	preReqScript, _ := data["preRequestScript"].(string)
	testScript, _ := data["testScript"].(string)
	note, _ := data["note"].(string)
	schema, _ := data["schema"].(string)

	urlencoded := parseURLEncoded(data["urlencoded"])
	headers := parseHeaders(data["headers"])

	events := []models.Event{}
	if preReqScript != "" {
		events = append(events, models.Event{
			Listen: "prerequest",
			Script: models.Script{Type: "text/javascript", Exec: []string{preReqScript}},
		})
	}
	if testScript != "" {
		events = append(events, models.Event{
			Listen: "test",
			Script: models.Script{Type: "text/javascript", Exec: []string{testScript}},
		})
	}

	body := &models.Body{Mode: bodyMode, Raw: bodyRaw, UrlEncoded: urlencoded}

	newReq := models.RequestInfo{
		Path: path,
		Request: &models.Request{
			Method: method,
			URL:    models.URL{Raw: urlStr},
			Header: headers,
			Body:   body,
		},
		Events: events,
		Note:   note,
		Schema: schema,
		Order:  len(a.FlatList),
	}

	if err := a.Storage.SaveSingleRequest(newReq); err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.FlatList = append(a.FlatList, newReq)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	return newReq, nil
}

func (a *App) UpdateRequest(data map[string]interface{}) (interface{}, error) {
	oldPath, _ := data["oldPath"].(string)
	newPath, _ := data["newPath"].(string)

	a.mu.Lock()
	defer a.mu.Unlock()

	idx := -1
	for i, r := range a.FlatList {
		if r.Path == oldPath {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, fmt.Errorf("request not found: %s", oldPath)
	}

	req := &a.FlatList[idx]
	if newPath != "" {
		req.Path = newPath
	}
	if v, ok := data["method"]; ok {
		req.Request.Method = toString(v)
	}
	if v, ok := data["url"]; ok {
		req.Request.URL.Raw = toString(v)
	}
	if v, ok := data["bodyMode"]; ok {
		mode := toString(v)
		if req.Request.Body == nil {
			req.Request.Body = &models.Body{}
		}
		req.Request.Body.Mode = mode
	}
	if v, ok := data["bodyRaw"]; ok {
		if req.Request.Body == nil {
			req.Request.Body = &models.Body{}
		}
		req.Request.Body.Raw = toString(v)
	}
	if v, ok := data["urlencoded"]; ok {
		if req.Request.Body == nil {
			req.Request.Body = &models.Body{}
		}
		req.Request.Body.UrlEncoded = parseURLEncoded(v)
	}
	if v, ok := data["headers"]; ok {
		req.Request.Header = parseHeaders(v)
	}
	if v, ok := data["preRequestScript"]; ok {
		s := toString(v)
		updateEvent(req, "prerequest", s)
	}
	if v, ok := data["testScript"]; ok {
		s := toString(v)
		updateEvent(req, "test", s)
	}
	if v, ok := data["note"]; ok {
		req.Note = toString(v)
	}
	if v, ok := data["schema"]; ok {
		req.Schema = toString(v)
	}

	if err := a.Storage.SaveSingleRequest(*req); err != nil {
		return nil, err
	}

	a.Collection.Item = models.ReconstructItems(a.FlatList)
	_ = a.Storage.SaveCollection(a.Collection)
	return *req, nil
}

func (a *App) DeleteRequest(data map[string]interface{}) (interface{}, error) {
	path, _ := data["path"].(string)

	if err := a.Storage.DeleteRequestFile(path); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i, r := range a.FlatList {
		if r.Path == path {
			a.FlatList = append(a.FlatList[:i], a.FlatList[i+1:]...)
			break
		}
	}
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	_ = a.Storage.SaveCollection(a.Collection)
	return nil, nil
}

func (a *App) DuplicateRequest(data map[string]interface{}) (interface{}, error) {
	path, _ := data["path"].(string)
	newPath, _ := data["newPath"].(string)

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("request not found: %s", path)
	}

	newReq := *target
	newReq.Path = newPath
	newReq.Order = len(a.FlatList)
	newReq.Request = target.Request.DeepCopy()

	if err := a.Storage.SaveSingleRequest(newReq); err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.FlatList = append(a.FlatList, newReq)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	return newReq, nil
}

func (a *App) ReorderRequests(data map[string]interface{}) (interface{}, error) {
	rawPaths, _ := data["paths"].([]interface{})
	paths := make([]string, len(rawPaths))
	for i, p := range rawPaths {
		paths[i] = toString(p)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	orderMap := make(map[string]int, len(paths))
	for i, p := range paths {
		orderMap[p] = i
	}
	for i := range a.FlatList {
		if order, ok := orderMap[a.FlatList[i].Path]; ok {
			a.FlatList[i].Order = order
		}
	}
	sort.Slice(a.FlatList, func(i, j int) bool {
		return a.FlatList[i].Order < a.FlatList[j].Order
	})
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	_ = a.Storage.SaveCollection(a.Collection)
	return nil, nil
}

func (a *App) SaveVariables(data map[string]interface{}) (interface{}, error) {
	for k, v := range data {
		val := toString(v)
		if err := a.Storage.SetVariable(k, val); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (a *App) DeleteHistory(data map[string]interface{}) (interface{}, error) {
	ts, _ := data["timestamp"].(string)
	history := a.Storage.LoadHistory()
	updated := make([]models.HistoryRecord, 0, len(history))
	for _, h := range history {
		if h.Timestamp.Format(time.RFC3339Nano) != ts && h.Timestamp.Format(time.RFC3339) != ts {
			updated = append(updated, h)
		}
	}
	err := a.Storage.SaveHistory(updated)
	return nil, err
}

func (a *App) DeleteMock(data map[string]interface{}) (interface{}, error) {
	reqPath, _ := data["requestPath"].(string)
	respName, _ := data["responseName"].(string)

	if respName == "" {
		respName, _ = data["mockName"].(string)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.FlatList {
		if a.FlatList[i].Path == reqPath {
			responses := a.FlatList[i].Responses
			for j, m := range responses {
				if m.Name == respName {
					a.FlatList[i].Responses = append(responses[:j], responses[j+1:]...)
					break
				}
			}
			return nil, a.Storage.SaveSingleRequest(a.FlatList[i])
		}
	}
	return nil, fmt.Errorf("request not found: %s", reqPath)
}

func (a *App) SendRequest(data map[string]interface{}) (interface{}, error) {
	path, _ := data["path"].(string)
	if path == "" {
		path, _ = data["requestPath"].(string)
	}

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("request not found: %s", path)
	}

	now := time.Now()
	reqCopy := target.Request.DeepCopy()

	// Run pre-request scripts
	a.Processor.RunScripts(target.Events, "prerequest", nil, nil, reqCopy.Header)

	body, headers, code, status := a.Client.ExecuteRequest(a.ctx, reqCopy)
	duration := time.Since(now).Milliseconds()

	// Run test scripts
	a.Processor.RunScripts(target.Events, "test", []byte(body), headers, reqCopy.Header)

	// Save to history
	historyRecord := models.HistoryRecord{
		Timestamp:  now,
		Path:       target.Path,
		Method:     reqCopy.Method,
		URL:        reqCopy.URL.Raw,
		StatusCode: code,
		StatusText: status,
		Duration:   duration,
	}
	_ = a.Storage.AddHistoryRecord(historyRecord)

	return map[string]interface{}{
		"body":       body,
		"headers":    headers,
		"statusCode": code,
		"statusText": status,
	}, nil
}

func (a *App) HammerRequest(data map[string]interface{}) (interface{}, error) {
	path, _ := data["path"].(string)
	workers := toInt(data["workers"], 10)
	seconds := toInt(data["seconds"], 5)

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("request not found: %s", path)
	}

	results := a.Client.Hammer(target.Request.DeepCopy(), workers, time.Duration(seconds)*time.Second)
	return results, nil
}

func (a *App) SQLRequest(data map[string]interface{}) (interface{}, error) {
	driver, _ := data["driver"].(string)
	connStr, _ := data["connStr"].(string)
	query, _ := data["query"].(string)

	if driver == "" {
		driver, _ = data["driver"].(string)
	}
	if connStr == "" {
		connStr, _ = data["connStr"].(string)
	}
	if query == "" {
		query, _ = data["query"].(string)
	}

	columns, rows, err := a.Client.ExecuteSQL(a.ctx, driver, connStr, query)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"columns": columns,
		"rows":    rows,
	}, nil
}

func (a *App) SaveSchema(data map[string]interface{}) (interface{}, error) {
	reqPath, _ := data["requestPath"].(string)
	schema, _ := data["schema"].(string)

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.FlatList {
		if a.FlatList[i].Path == reqPath {
			a.FlatList[i].Schema = schema
			return map[string]bool{"success": true}, a.Storage.SaveSingleRequest(a.FlatList[i])
		}
	}
	return nil, fmt.Errorf("request not found: %s", reqPath)
}

func (a *App) SaveMockResponse(data map[string]interface{}) (interface{}, error) {
	reqPath, _ := data["requestPath"].(string)
	respData, ok := data["response"].(map[string]interface{})
	if !ok {
		// Try flat fields
		respData = data
		reqPath, _ = data["requestPath"].(string)
		if reqPath == "" {
			reqPath, _ = data["path"].(string)
		}
	}

	name, _ := respData["name"].(string)
	code := toInt(respData["code"], 200)
	body, _ := respData["body"].(string)

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.FlatList {
		if a.FlatList[i].Path == reqPath {
			mr := models.MockResponse{
				Name: name,
				Code: code,
				Body: body,
			}
			if h, ok := respData["header"]; ok {
				mr.Header = parseHeaders(h)
			}
			a.FlatList[i].Responses = append(a.FlatList[i].Responses, mr)
			return map[string]bool{"success": true}, a.Storage.SaveSingleRequest(a.FlatList[i])
		}
	}
	return nil, fmt.Errorf("request not found: %s", reqPath)
}

func (a *App) FuzzRequest(data map[string]interface{}) (interface{}, error) {
	path, _ := data["path"].(string)

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("request not found: %s", path)
	}

	results, err := a.fuzzer.Run(a.ctx, *target, a.Storage.GetVariableMapCopy())
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (a *App) RunRunner(data map[string]interface{}) (interface{}, error) {
	path, _ := data["path"].(string)
	rawData, _ := data["data"].([]interface{})

	iterData := make([]map[string]string, len(rawData))
	for i, row := range rawData {
		rowMap, ok := row.(map[string]interface{})
		if !ok {
			continue
		}
		iterData[i] = make(map[string]string)
		for k, v := range rowMap {
			iterData[i][k] = toString(v)
		}
	}

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("request not found: %s", path)
	}

	results := a.runner.RunIteration(a.ctx, *target, iterData)
	return results, nil
}

func (a *App) GraphQLIntrospection(data map[string]interface{}) (interface{}, error) {
	urlStr, _ := data["url"].(string)
	if urlStr == "" {
		return nil, fmt.Errorf("url is required")
	}

	introspectionQuery := `{"query":"query { __schema { types { name kind description fields { name description type { name kind ofType { name kind } } } } } }"}`
	resp, err := http.Post(urlStr, "application/json", strings.NewReader(introspectionQuery))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func (a *App) ImportCurl(data map[string]interface{}) (interface{}, error) {
	command, _ := data["command"].(string)
	if command == "" {
		command, _ = data["curl"].(string)
	}

	parsed := processor.ParseCurl(command)
	if parsed == nil {
		return nil, fmt.Errorf("failed to parse cURL command")
	}

	newReq := models.RequestInfo{
		Path:    "cURL Import",
		Request: parsed,
		Order:   len(a.FlatList),
	}

	if err := a.Storage.SaveSingleRequest(newReq); err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.FlatList = append(a.FlatList, newReq)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)

	return map[string]interface{}{
		"success": true,
		"path":    newReq.Path,
	}, nil
}

func (a *App) ImportOpenAPI(data map[string]interface{}) (interface{}, error) {
	jsonStr, _ := data["json"].(string)
	if jsonStr == "" {
		return nil, fmt.Errorf("OpenAPI spec JSON is required")
	}

	requests, err := processor.ParseOpenAPI([]byte(jsonStr))
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	for _, req := range requests {
		req.Order = len(a.FlatList)
		a.FlatList = append(a.FlatList, req)
		if err := a.Storage.SaveSingleRequest(req); err != nil {
			a.mu.Unlock()
			return nil, err
		}
	}
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	return map[string]interface{}{"success": true, "count": len(requests)}, nil
}

func (a *App) ImportPostman(data map[string]interface{}) (interface{}, error) {
	jsonStr, _ := data["json"].(string)
	if jsonStr == "" {
		return nil, fmt.Errorf("Postman collection JSON is required")
	}

	_, requests, err := processor.ParsePostmanCollection([]byte(jsonStr))
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	for _, req := range requests {
		req.Order = len(a.FlatList)
		a.FlatList = append(a.FlatList, req)
		if err := a.Storage.SaveSingleRequest(req); err != nil {
			a.mu.Unlock()
			return nil, err
		}
	}
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	return map[string]interface{}{"success": true, "count": len(requests)}, nil
}

func (a *App) SaveWorkflows(data interface{}) (interface{}, error) {
	workflows := a.Storage.GetWorkflows()

	switch v := data.(type) {
	case []interface{}:
		// Called with a direct array of workflows
		newWorkflows := make([]models.Workflow, len(v))
		for i, raw := range v {
			if m, ok := raw.(map[string]interface{}); ok {
				newWorkflows[i] = parseWorkflow(m)
			}
		}
		return nil, a.Storage.SaveWorkflows(newWorkflows)

	case map[string]interface{}:
		// Called with a single workflow object or {workflows: [...], id: "..."}
		if wfList, ok := v["workflows"].([]interface{}); ok {
			newWorkflows := make([]models.Workflow, len(wfList))
			for i, raw := range wfList {
				if m, ok := raw.(map[string]interface{}); ok {
					newWorkflows[i] = parseWorkflow(m)
				}
			}
			return nil, a.Storage.SaveWorkflows(newWorkflows)
		}

		// Single workflow with id
		if id, ok := v["id"].(string); ok && id != "" {
			wf := parseWorkflow(v)
			found := false
			for i, w := range workflows {
				if w.ID == id {
					workflows[i] = wf
					found = true
					break
				}
			}
			if !found {
				workflows = append(workflows, wf)
			}
			return nil, a.Storage.SaveWorkflows(workflows)
		}
	}

	return nil, a.Storage.SaveWorkflows(workflows)
}

func (a *App) RunWorkflow(data map[string]interface{}) (interface{}, error) {
	workflowID, _ := data["workflowId"].(string)
	if workflowID == "" {
		workflowID, _ = data["id"].(string)
	}
	startNode, _ := data["startNode"].(string)

	workflows := a.Storage.GetWorkflows()
	var workflow *models.Workflow
	for i, w := range workflows {
		if w.ID == workflowID {
			workflow = &workflows[i]
			break
		}
	}
	if workflow == nil {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}

	a.mu.RLock()
	requestsCopy := make([]models.RequestInfo, len(a.FlatList))
	copy(requestsCopy, a.FlatList)
	a.mu.RUnlock()

	logs, err := a.Client.RunWorkflow(a.ctx, workflow, requestsCopy, startNode)
	result := map[string]interface{}{
		"logs":  logs,
		"error": nil,
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result, nil
}

func (a *App) SaveEnvironments(data []interface{}) (interface{}, error) {
	envs := make([]models.Environment, len(data))
	for i, raw := range data {
		if envMap, ok := raw.(map[string]interface{}); ok {
			envs[i] = parseEnvironment(envMap)
		}
	}
	return nil, a.Storage.SaveEnvironments(envs)
}

// Handle both single env and array.
func (a *App) SaveEnvironment(data map[string]interface{}) (interface{}, error) {
	envs := a.Storage.GetEnvironments()
	env := parseEnvironment(data)

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
	return nil, a.Storage.SaveEnvironments(envs)
}

func (a *App) SetActiveEnv(data map[string]interface{}) (interface{}, error) {
	id, _ := data["id"].(string)
	if id == "" {
		id, _ = data["envId"].(string)
	}
	return nil, a.Storage.SetActiveEnvID(id)
}

func (a *App) UnlockVault(data map[string]interface{}) (interface{}, error) {
	password, _ := data["password"].(string)
	if err := a.Storage.SetVaultKey(password); err != nil {
		return map[string]bool{"unlocked": false}, err
	}
	return map[string]bool{"unlocked": true}, nil
}

func (a *App) VaultEncrypt(data map[string]interface{}) (interface{}, error) {
	plaintext, _ := data["plaintext"].(string)
	if plaintext == "" {
		plaintext, _ = data["plaintext"].(string)
	}
	encrypted, err := a.Storage.Encrypt(plaintext)
	if err != nil {
		return nil, err
	}
	return map[string]string{"encrypted": encrypted}, nil
}

func (a *App) WSConnect(data map[string]interface{}) (interface{}, error) {
	urlStr, _ := data["url"].(string)
	return nil, a.WSClient.Connect(urlStr)
}

func (a *App) WSSend(data map[string]interface{}) (interface{}, error) {
	message, _ := data["message"].(string)
	return nil, a.WSClient.Send(message)
}

func (a *App) StartProxy(data map[string]interface{}) (interface{}, error) {
	port := toInt(data["port"], 8888)
	err := a.Proxy.Start(port)
	return map[string]interface{}{
		"running": err == nil,
		"port":    port,
	}, err
}

func (a *App) ExportPostman(data map[string]interface{}) (interface{}, error) {
	folderPath, _ := data["path"].(string)
	if folderPath == "" {
		folderPath, _ = data["folderPath"].(string)
	}

	a.mu.RLock()
	collection := api.ExportPostman(a.FlatList, folderPath)
	a.mu.RUnlock()

	return collection, nil
}

func (a *App) DuplicateFolder(data map[string]interface{}) (interface{}, error) {
	path, _ := data["path"].(string)
	newParentPath, _ := data["newParentPath"].(string)
	_ = newParentPath // Not fully implemented in server.go either

	a.mu.Lock()
	defer a.mu.Unlock()

	// Duplicate all requests under path
	var newReqs []models.RequestInfo
	for _, r := range a.FlatList {
		if strings.HasPrefix(r.Path, path) {
			dup := r
			suffix := strings.TrimPrefix(r.Path, path)
			dup.Path = r.Path + " (copy)" + suffix
			dup.Order = len(a.FlatList) + len(newReqs)
			dup.Request = r.Request.DeepCopy()

			if err := a.Storage.SaveSingleRequest(dup); err != nil {
				return nil, err
			}
			newReqs = append(newReqs, dup)
		}
	}

	a.FlatList = append(a.FlatList, newReqs...)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	_ = a.Storage.SaveCollection(a.Collection)
	return nil, nil
}

func (a *App) RenameFolder(data map[string]interface{}) (interface{}, error) {
	oldPath, _ := data["oldPath"].(string)
	newPath, _ := data["newPath"].(string)

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.FlatList {
		if strings.HasPrefix(a.FlatList[i].Path, oldPath) {
			a.FlatList[i].Path = strings.Replace(a.FlatList[i].Path, oldPath, newPath, 1)
			if err := a.Storage.SaveSingleRequest(a.FlatList[i]); err != nil {
				return nil, err
			}
		}
	}

	a.Collection.Item = models.ReconstructItems(a.FlatList)
	_ = a.Storage.SaveCollection(a.Collection)
	return nil, nil
}

// ─── Internal handlers (for the HTTP proxy server) ─────────────────────────────

func (a *App) handleGetRequests(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	result := map[string]interface{}{
		"collection": a.Collection,
		"flat":       a.FlatList,
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, result)
}

func (a *App) handleNewRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path            string             `json:"path"`
		Method          string             `json:"method"`
		URL             string             `json:"url"`
		BodyMode        string             `json:"bodyMode"`
		BodyRaw         string             `json:"bodyRaw"`
		UrlEncoded      []models.UrlEncoded `json:"urlencoded"`
		Headers         []models.Header     `json:"headers"`
		PreRequestScript string             `json:"preRequestScript"`
		TestScript      string             `json:"testScript"`
		Note            string             `json:"note"`
		Schema          string             `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	events := []models.Event{}
	if input.PreRequestScript != "" {
		events = append(events, models.Event{
			Listen: "prerequest",
			Script: models.Script{Type: "text/javascript", Exec: []string{input.PreRequestScript}},
		})
	}
	if input.TestScript != "" {
		events = append(events, models.Event{
			Listen: "test",
			Script: models.Script{Type: "text/javascript", Exec: []string{input.TestScript}},
		})
	}

	newReq := models.RequestInfo{
		Path: input.Path,
		Request: &models.Request{
			Method: input.Method,
			URL:    models.URL{Raw: input.URL},
			Header: input.Headers,
			Body:   &models.Body{Mode: input.BodyMode, Raw: input.BodyRaw, UrlEncoded: input.UrlEncoded},
		},
		Events: events,
		Note:   input.Note,
		Schema: input.Schema,
		Order:  len(a.FlatList),
	}

	if err := a.Storage.SaveSingleRequest(newReq); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.mu.Lock()
	a.FlatList = append(a.FlatList, newReq)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	writeJSON(w, http.StatusCreated, newReq)
}

func (a *App) handleDuplicateRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path    string `json:"path"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()
	if target == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}

	newReq := *target
	newReq.Path = input.NewPath
	newReq.Order = len(a.FlatList)
	newReq.Request = target.Request.DeepCopy()

	if err := a.Storage.SaveSingleRequest(newReq); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.mu.Lock()
	a.FlatList = append(a.FlatList, newReq)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	writeJSON(w, http.StatusCreated, newReq)
}

func (a *App) handleUpdateRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OldPath         string             `json:"oldPath"`
		NewPath         string             `json:"newPath"`
		Method          string             `json:"method"`
		URL             string             `json:"url"`
		BodyMode        string             `json:"bodyMode"`
		BodyRaw         string             `json:"bodyRaw"`
		UrlEncoded      []models.UrlEncoded `json:"urlencoded"`
		Headers         []models.Header     `json:"headers"`
		PreRequestScript string             `json:"preRequestScript"`
		TestScript      string             `json:"testScript"`
		Note            string             `json:"note"`
		Schema          string             `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	idx := -1
	for i, r := range a.FlatList {
		if r.Path == input.OldPath {
			idx = i
			break
		}
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "request not found: "+input.OldPath)
		return
	}

	req := &a.FlatList[idx]
	if input.NewPath != "" {
		req.Path = input.NewPath
	}
	if input.Method != "" {
		req.Request.Method = input.Method
	}
	if input.URL != "" {
		req.Request.URL.Raw = input.URL
	}
	if input.BodyMode != "" {
		if req.Request.Body == nil {
			req.Request.Body = &models.Body{}
		}
		req.Request.Body.Mode = input.BodyMode
	}
	if input.BodyRaw != "" || input.BodyMode != "" {
		if req.Request.Body == nil {
			req.Request.Body = &models.Body{}
		}
		req.Request.Body.Raw = input.BodyRaw
		req.Request.Body.UrlEncoded = input.UrlEncoded
	}
	if len(input.Headers) > 0 {
		req.Request.Header = input.Headers
	}
	if input.PreRequestScript != "" {
		updateEvent(req, "prerequest", input.PreRequestScript)
	}
	if input.TestScript != "" {
		updateEvent(req, "test", input.TestScript)
	}
	if input.Note != "" {
		req.Note = input.Note
	}
	if input.Schema != "" {
		req.Schema = input.Schema
	}

	if err := a.Storage.SaveSingleRequest(*req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	_ = a.Storage.SaveCollection(a.Collection)
	writeJSON(w, http.StatusOK, *req)
}

func (a *App) handleDeleteRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := a.Storage.DeleteRequestFile(input.Path); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for i, r := range a.FlatList {
		if r.Path == input.Path {
			a.FlatList = append(a.FlatList[:i], a.FlatList[i+1:]...)
			break
		}
	}
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	_ = a.Storage.SaveCollection(a.Collection)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleReorderRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	orderMap := make(map[string]int, len(input.Paths))
	for i, p := range input.Paths {
		orderMap[p] = i
	}
	for i := range a.FlatList {
		if order, ok := orderMap[a.FlatList[i].Path]; ok {
			a.FlatList[i].Order = order
		}
	}
	sort.Slice(a.FlatList, func(i, j int) bool {
		return a.FlatList[i].Order < a.FlatList[j].Order
	})
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	_ = a.Storage.SaveCollection(a.Collection)
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleVariables(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.Storage.GetVariableMapCopy())
	case http.MethodPost:
		var vars map[string]string
		if err := json.NewDecoder(r.Body).Decode(&vars); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		for k, v := range vars {
			if err := a.Storage.SetVariable(k, v); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleSendRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()
	if target == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}

	now := time.Now()
	reqCopy := target.Request.DeepCopy()
	a.Processor.RunScripts(target.Events, "prerequest", nil, nil, reqCopy.Header)
	body, headers, code, status := a.Client.ExecuteRequest(r.Context(), reqCopy)
	duration := time.Since(now).Milliseconds()
	a.Processor.RunScripts(target.Events, "test", []byte(body), headers, reqCopy.Header)

	_ = a.Storage.AddHistoryRecord(models.HistoryRecord{
		Timestamp:  now,
		Path:       target.Path,
		Method:     reqCopy.Method,
		URL:        reqCopy.URL.Raw,
		StatusCode: code,
		StatusText: status,
		Duration:   duration,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"body":       body,
		"headers":    headers,
		"statusCode": code,
		"statusText": status,
	})
}

func (a *App) handleHammerRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path     string `json:"path"`
		Workers  int    `json:"workers"`
		Duration int    `json:"duration"`
		Seconds  int    `json:"seconds"`
		Stream   bool   `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.Workers <= 0 {
		input.Workers = 10
	}
	// Accept both "duration" and "seconds" field names
	if input.Duration <= 0 {
		input.Duration = input.Seconds
	}
	if input.Duration <= 0 {
		input.Duration = 5
	}

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()
	if target == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}

	duration := time.Duration(input.Duration) * time.Second

	if input.Stream {
		// Streaming mode: use chunked encoding and flush progress every 500ms
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)

		progressCh := make(chan *api.HammerProgress, 32)
		go a.Client.HammerStream(target.Request.DeepCopy(), input.Workers, duration, progressCh)

		for p := range progressCh {
			line, _ := json.Marshal(p)
			w.Write(append(line, '\n'))
			flusher.Flush()
		}
		return
	}

	results := a.Client.Hammer(target.Request.DeepCopy(), input.Workers, duration)
	writeJSON(w, http.StatusOK, results)
}

func (a *App) handleSQLRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Driver  string `json:"driver"`
		ConnStr string `json:"connStr"`
		Query   string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.Driver == "" || input.Query == "" {
		writeError(w, http.StatusBadRequest, "driver and query are required")
		return
	}

	columns, rows, err := a.Client.ExecuteSQL(r.Context(), input.Driver, input.ConnStr, input.Query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"columns": columns,
		"rows":    rows,
	})
}

func (a *App) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.Storage.LoadHistory())
}

func (a *App) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	err := a.Storage.SaveHistory([]models.HistoryRecord{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleDeleteHistory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	history := a.Storage.LoadHistory()
	updated := make([]models.HistoryRecord, 0, len(history))
	for _, h := range history {
		ts := h.Timestamp.Format(time.RFC3339Nano)
		if ts != input.Timestamp && h.Timestamp.Format(time.RFC3339) != input.Timestamp {
			updated = append(updated, h)
		}
	}
	if err := a.Storage.SaveHistory(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleSaveMockResponse(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RequestPath string               `json:"requestPath"`
		Response    models.MockResponse  `json:"response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.FlatList {
		if a.FlatList[i].Path == input.RequestPath {
			a.FlatList[i].Responses = append(a.FlatList[i].Responses, input.Response)
			if err := a.Storage.SaveSingleRequest(a.FlatList[i]); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
	}
	writeError(w, http.StatusNotFound, "request not found")
}

func (a *App) handleDeleteMock(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RequestPath  string `json:"requestPath"`
		ResponseName string `json:"responseName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.FlatList {
		if a.FlatList[i].Path == input.RequestPath {
			responses := a.FlatList[i].Responses
			for j, m := range responses {
				if m.Name == input.ResponseName {
					a.FlatList[i].Responses = append(responses[:j], responses[j+1:]...)
					if err := a.Storage.SaveSingleRequest(a.FlatList[i]); err != nil {
						writeError(w, http.StatusInternalServerError, err.Error())
						return
					}
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			writeError(w, http.StatusNotFound, "mock response not found")
			return
		}
	}
	writeError(w, http.StatusNotFound, "request not found")
}

func (a *App) handleMockStats(w http.ResponseWriter, r *http.Request) {
	a.mockMu.RLock()
	defer a.mockMu.RUnlock()
	writeJSON(w, http.StatusOK, a.mockStats)
}

func (a *App) handleSchemaGenerate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	schema := generateJSONSchema(input.Body)
	writeJSON(w, http.StatusOK, schema)
}

func (a *App) handleSchemaValidate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body   string      `json:"body"`
		Schema interface{} `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	valid, errors := validateAgainstSchema(input.Body, input.Schema)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":  valid,
		"errors": errors,
	})
}

func (a *App) handleSchemaSave(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RequestPath string `json:"requestPath"`
		Schema      string `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.FlatList {
		if a.FlatList[i].Path == input.RequestPath {
			a.FlatList[i].Schema = input.Schema
			if err := a.Storage.SaveSingleRequest(a.FlatList[i]); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
	}
	writeError(w, http.StatusNotFound, "request not found")
}

func (a *App) handleFuzzRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()
	if target == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}

	results, err := a.fuzzer.Run(r.Context(), *target, a.Storage.GetVariableMapCopy())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (a *App) handleRunnerRun(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string              `json:"path"`
		Data []map[string]string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.RLock()
	var target *models.RequestInfo
	for i := range a.FlatList {
		if a.FlatList[i].Path == input.Path {
			target = &a.FlatList[i]
			break
		}
	}
	a.mu.RUnlock()
	if target == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}

	if input.Data == nil {
		input.Data = []map[string]string{{}}
	}

	results := a.runner.RunIteration(r.Context(), *target, input.Data)
	writeJSON(w, http.StatusOK, results)
}

func (a *App) handleGraphQLIntrospection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	const introspectionQuery = `{"query":"query { __schema { types { name kind description fields { name description type { name kind ofType { name kind } } } } } }"}`
	req, err := http.NewRequestWithContext(r.Context(), "POST", input.URL, strings.NewReader(introspectionQuery))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range input.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer resp.Body.Close()

	var result interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	writeJSON(w, http.StatusOK, result)
}

func (a *App) handleImportCurl(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.Command == "" {
		var alt struct{ Curl string `json:"curl"` }
		if err := json.NewDecoder(r.Body).Decode(&alt); err == nil && alt.Curl != "" {
			input.Command = alt.Curl
		}
	}
	if input.Command == "" {
		writeError(w, http.StatusBadRequest, "cURL command is required")
		return
	}

	parsed := processor.ParseCurl(input.Command)
	if parsed == nil {
		writeError(w, http.StatusBadRequest, "failed to parse cURL command")
		return
	}

	newReq := models.RequestInfo{
		Path:    "cURL Import",
		Request: parsed,
		Order:   len(a.FlatList),
	}
	if err := a.Storage.SaveSingleRequest(newReq); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.mu.Lock()
	a.FlatList = append(a.FlatList, newReq)
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"path":    newReq.Path,
	})
}

func (a *App) handleImportOpenAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		JSON string `json:"json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.JSON == "" {
		writeError(w, http.StatusBadRequest, "OpenAPI spec JSON is required")
		return
	}

	requests, err := processor.ParseOpenAPI([]byte(input.JSON))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse OpenAPI spec: "+err.Error())
		return
	}

	a.mu.Lock()
	for _, req := range requests {
		req.Order = len(a.FlatList)
		a.FlatList = append(a.FlatList, req)
		if saveErr := a.Storage.SaveSingleRequest(req); saveErr != nil {
			a.mu.Unlock()
			writeError(w, http.StatusInternalServerError, saveErr.Error())
			return
		}
	}
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"count":   len(requests),
	})
}

func (a *App) handleImportPostman(w http.ResponseWriter, r *http.Request) {
	var input struct {
		JSON string `json:"json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.JSON == "" {
		writeError(w, http.StatusBadRequest, "Postman collection JSON is required")
		return
	}

	_, requests, err := processor.ParsePostmanCollection([]byte(input.JSON))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse Postman collection: "+err.Error())
		return
	}

	a.mu.Lock()
	for _, req := range requests {
		req.Order = len(a.FlatList)
		a.FlatList = append(a.FlatList, req)
		if saveErr := a.Storage.SaveSingleRequest(req); saveErr != nil {
			a.mu.Unlock()
			writeError(w, http.StatusInternalServerError, saveErr.Error())
			return
		}
	}
	a.Collection.Item = models.ReconstructItems(a.FlatList)
	a.mu.Unlock()

	_ = a.Storage.SaveCollection(a.Collection)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"count":   len(requests),
	})
}

func (a *App) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.Storage.GetWorkflows())
	case http.MethodPost:
		var input models.Workflow
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		workflows := a.Storage.GetWorkflows()
		found := false
		for i, w := range workflows {
			if w.ID == input.ID {
				workflows[i] = input
				found = true
				break
			}
		}
		if !found {
			workflows = append(workflows, input)
		}

		if err := a.Storage.SaveWorkflows(workflows); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, input)
	case http.MethodDelete:
		var input struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := a.Storage.DeleteWorkflow(input.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleRunWorkflow(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkflowID string `json:"workflowId"`
		StartNode  string `json:"startNode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	workflows := a.Storage.GetWorkflows()
	var workflow *models.Workflow
	for i, w := range workflows {
		if w.ID == input.WorkflowID {
			workflow = &workflows[i]
			break
		}
	}
	if workflow == nil {
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}

	a.mu.RLock()
	requestsCopy := make([]models.RequestInfo, len(a.FlatList))
	copy(requestsCopy, a.FlatList)
	a.mu.RUnlock()

	logs, err := a.Client.RunWorkflow(r.Context(), workflow, requestsCopy, input.StartNode)
	result := map[string]interface{}{"logs": logs}
	if err != nil {
		result["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *App) handleEnvironments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.Storage.GetEnvironments())
	case http.MethodPost:
		var input models.Environment
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		envs := a.Storage.GetEnvironments()
		found := false
		for i, e := range envs {
			if e.ID == input.ID {
				envs[i] = input
				found = true
				break
			}
		}
		if !found {
			envs = append(envs, input)
		}

		if err := a.Storage.SaveEnvironments(envs); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, input)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleActiveEnv(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]string{"activeEnvId": a.Storage.GetActiveEnvID()})
	case http.MethodPost:
		var input struct {
			EnvID string `json:"envId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := a.Storage.SetActiveEnvID(input.EnvID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleUnlockVault(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := a.Storage.SetVaultKey(input.Password); err != nil {
		writeJSON(w, http.StatusOK, map[string]bool{"unlocked": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"unlocked": true})
}

func (a *App) handleVaultEncrypt(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	encrypted, err := a.Storage.Encrypt(input.Plaintext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"encrypted": encrypted})
}

func (a *App) handleVaultStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"unlocked": a.Storage.IsVaultUnlocked()})
}

func (a *App) handleWSConnect(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := a.WSClient.Connect(input.URL); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleWSSend(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := a.WSClient.Send(input.Message); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleWSMessages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.WSClient.GetMessages())
}

func (a *App) handleWSClose(w http.ResponseWriter, r *http.Request) {
	a.WSClient.Close()
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleProxyStart(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.Port <= 0 {
		input.Port = 8888
	}
	err := a.Proxy.Start(input.Port)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"running": err == nil,
		"port":    input.Port,
	})
}

func (a *App) handleProxyStop(w http.ResponseWriter, r *http.Request) {
	err := a.Proxy.Stop()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"running": err != nil,
	})
}

func (a *App) handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"running": a.Proxy.IsRunning()})
}

// ── Kafka HTTP handlers ────────────────────────────────────────────────────

func (a *App) handleKafkaConnect(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Config models.KafkaConfig `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if input.Config.ClientID == "" {
		input.Config.ClientID = "postit"
	}
	if input.Config.TimeoutSec <= 0 {
		input.Config.TimeoutSec = 10
	}
	if err := a.Kafka.Connect(input.Config); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"connected": true,
		"brokers":   input.Config.Brokers,
	})
}

func (a *App) handleKafkaSend(w http.ResponseWriter, r *http.Request) {
	var msg models.KafkaMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if msg.Topic == "" {
		writeError(w, http.StatusBadRequest, "topic is required")
		return
	}
	result, err := a.Kafka.SendMessage(r.Context(), msg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *App) handleKafkaTopics(w http.ResponseWriter, r *http.Request) {
	brokersParam := r.URL.Query().Get("brokers")
	if brokersParam == "" {
		writeError(w, http.StatusBadRequest, "brokers query parameter is required (comma-separated)")
		return
	}
	brokers := strings.Split(brokersParam, ",")
	cfg := api.KafkaConfigDefaults()
	cfg.Brokers = brokers
	topics, err := a.Kafka.GetTopics(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"topics": topics})
}

func (a *App) handleKafkaTopicMeta(w http.ResponseWriter, r *http.Request) {
	topic := strings.TrimPrefix(r.URL.Path, "/api/kafka/topics/")
	if topic == "" {
		writeError(w, http.StatusBadRequest, "topic is required in path")
		return
	}
	brokersParam := r.URL.Query().Get("brokers")
	if brokersParam == "" {
		writeError(w, http.StatusBadRequest, "brokers query parameter is required (comma-separated)")
		return
	}
	brokers := strings.Split(brokersParam, ",")
	cfg := api.KafkaConfigDefaults()
	cfg.Brokers = brokers
	partitions, err := a.Kafka.GetTopicMetadata(r.Context(), cfg, topic)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"topic":      topic,
		"partitions": partitions,
	})
}

func (a *App) handleKafkaStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"connected": a.Kafka.IsConnected()})
}

func (a *App) handleKafkaDisconnect(w http.ResponseWriter, r *http.Request) {
	a.Kafka.Close()
	writeJSON(w, http.StatusOK, map[string]bool{"connected": false})
}

func (a *App) handleKafkaConfigs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.Storage.GetKafkaConnections())
	case http.MethodPost:
		var conn models.KafkaConnection
		if err := json.NewDecoder(r.Body).Decode(&conn); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if conn.ID == "" {
			writeError(w, http.StatusBadRequest, "id is required")
			return
		}
		if err := a.Storage.AddKafkaConnection(conn); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, conn)
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "id query parameter is required")
			return
		}
		if err := a.Storage.DeleteKafkaConnection(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleExportCollection(w http.ResponseWriter, r *http.Request) {
	folderPath := r.URL.Query().Get("path")
	if folderPath == "" {
		folderPath = r.URL.Query().Get("folderPath")
	}

	a.mu.RLock()
	collection := api.ExportPostman(a.FlatList, folderPath)
	a.mu.RUnlock()

	writeJSON(w, http.StatusOK, collection)
}

func (a *App) handleExportHistory(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	history := a.Storage.LoadHistory()

	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=history.csv")
		fmt.Fprintln(w, "Timestamp,Path,Method,URL,StatusCode,StatusText,Duration")
		for _, h := range history {
			fmt.Fprintf(w, "%s,%s,%s,%s,%d,%s,%d\n",
				h.Timestamp.Format(time.RFC3339), h.Path, h.Method, h.URL,
				h.StatusCode, h.StatusText, h.Duration)
		}
		return
	}

	writeJSON(w, http.StatusOK, history)
}

func (a *App) handleGenerateDocs(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	actions := []map[string]string{}
	for _, req := range a.FlatList {
		actions = append(actions, map[string]string{
			"method": req.Request.Method,
			"url":    req.Request.URL.Raw,
			"path":   req.Path,
		})
	}

	switch format {
	case "openapi":
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"openapi": "3.0.0",
			"info":    map[string]string{"title": "PostIt API", "version": "1.0.0"},
			"paths":   actions,
		})
	case "html":
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, "<html><body><h1>API Documentation</h1><ul>")
		for _, a := range actions {
			fmt.Fprintf(w, "<li><strong>%s</strong> %s <em>%s</em></li>", a["method"], a["url"], a["path"])
		}
		fmt.Fprintln(w, "</ul></body></html>")
	default:
		w.Header().Set("Content-Type", "text/markdown")
		fmt.Fprintln(w, "# API Documentation")
		for _, a := range actions {
			fmt.Fprintf(w, "- **%s** %s (%s)\n", a["method"], a["url"], a["path"])
		}
	}
}

func (a *App) handleMockRequest(w http.ResponseWriter, r *http.Request) {
	// Simple mock handler - finds matching request and returns configured mock response
	path := strings.TrimPrefix(r.URL.Path, "/mock/")
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, req := range a.FlatList {
		if strings.EqualFold(req.Path, path) || strings.EqualFold(req.Path, "/"+path) {
			if len(req.Responses) > 0 {
				mock := req.Responses[0]
				for _, h := range mock.Header {
					w.Header().Set(h.Key, h.Value)
				}
				w.WriteHeader(mock.Code)
				fmt.Fprint(w, mock.Body)

				a.mockMu.Lock()
				stat := a.mockStats[req.Path]
				if stat == nil {
					stat = &models.MockStat{Hits: 0, LastAccess: time.Now()}
					a.mockStats[req.Path] = stat
				}
				stat.Hits++
				stat.LastAccess = time.Now()
				a.mockMu.Unlock()
				return
			}
			writeError(w, http.StatusNotFound, "no mock response configured")
			return
		}
	}
	writeError(w, http.StatusNotFound, "no matching request for mock")
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toFloat64(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

func toInt(v interface{}, defaultVal int) int {
	if v == nil {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		fmt.Sscanf(n, "%d", &defaultVal)
		return defaultVal
	}
	return defaultVal
}

func parseURLEncoded(data interface{}) []models.UrlEncoded {
	if data == nil {
		return nil
	}
	raw, ok := data.([]interface{})
	if !ok {
		return nil
	}
	result := make([]models.UrlEncoded, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		result[i] = models.UrlEncoded{
			Key:   toString(m["key"]),
			Value: toString(m["value"]),
		}
	}
	return result
}

func parseHeaders(data interface{}) []models.Header {
	if data == nil {
		return nil
	}
	raw, ok := data.([]interface{})
	if !ok {
		return nil
	}
	result := make([]models.Header, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		result[i] = models.Header{
			Key:   toString(m["key"]),
			Value: toString(m["value"]),
		}
	}
	return result
}

func parseEnvironment(data map[string]interface{}) models.Environment {
	env := models.Environment{
		ID:   toString(data["id"]),
		Name: toString(data["name"]),
	}
	if vars, ok := data["variables"].(map[string]interface{}); ok {
		env.Variables = make(map[string]string)
		for k, v := range vars {
			env.Variables[k] = toString(v)
		}
	}
	if svars, ok := data["secretVars"].(map[string]interface{}); ok {
		env.SecretVars = make(map[string]string)
		for k, v := range svars {
			env.SecretVars[k] = toString(v)
		}
	}
	return env
}

func parseWorkflowNodes(data []interface{}) []models.WorkflowNode {
	nodes := make([]models.WorkflowNode, len(data))
	for i, raw := range data {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		nodes[i].ID = toString(m["id"])
		nodes[i].Type = toString(m["type"])
		nodes[i].RequestPath = toString(m["requestPath"])
		nodes[i].WaitTime = toInt(m["waitTime"], 0)
		nodes[i].Condition = toString(m["condition"])
		nodes[i].LoopPath = toString(m["loopPath"])
		nodes[i].MaxIterations = toInt(m["maxIterations"], 10)
		nodes[i].Script = toString(m["script"])
		nodes[i].VariableName = toString(m["variableName"])
		nodes[i].X = toFloat64(m["x"])
		nodes[i].Y = toFloat64(m["y"])
	}
	return nodes
}

func parseWorkflow(data map[string]interface{}) models.Workflow {
	wf := models.Workflow{
		ID:   toString(data["id"]),
		Name: toString(data["name"]),
	}
	if nodes, ok := data["nodes"].([]interface{}); ok {
		wf.Nodes = parseWorkflowNodes(nodes)
	}
	if edges, ok := data["edges"].([]interface{}); ok {
		wf.Edges = parseWorkflowEdges(edges)
	}
	return wf
}

func parseWorkflowEdges(data []interface{}) []models.WorkflowEdge {
	edges := make([]models.WorkflowEdge, len(data))
	for i, raw := range data {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		edges[i].FromNode = toString(m["fromNode"])
		edges[i].ToNode = toString(m["toNode"])
		edges[i].Type = toString(m["type"])
	}
	return edges
}

func updateEvent(req *models.RequestInfo, listen, script string) {
	for i, e := range req.Events {
		if e.Listen == listen {
			req.Events[i].Script.Exec = []string{script}
			return
		}
	}
	req.Events = append(req.Events, models.Event{
		Listen: listen,
		Script: models.Script{Type: "text/javascript", Exec: []string{script}},
	})
}

// ── Kafka helpers ──────────────────────────────────────────────────────────

func parseKafkaConfig(data map[string]interface{}) models.KafkaConfig {
	cfg := api.KafkaConfigDefaults()

	if brokers, ok := data["brokers"].([]interface{}); ok {
		cfg.Brokers = make([]string, len(brokers))
		for i, b := range brokers {
			cfg.Brokers[i] = toString(b)
		}
	} else if b, ok := data["brokers"].(string); ok {
		cfg.Brokers = strings.Split(b, ",")
	}

	if cid, ok := data["clientId"].(string); ok && cid != "" {
		cfg.ClientID = cid
	}
	if t, ok := data["compression"].(string); ok && t != "" {
		cfg.Compression = t
	}
	if a, ok := data["requiredAcks"].(string); ok && a != "" {
		cfg.RequiredAcks = a
	}
	if bs, ok := data["batchSize"].(float64); ok && bs > 0 {
		cfg.BatchSize = int(bs)
	}
	if bt, ok := data["batchTimeoutMs"].(float64); ok && bt > 0 {
		cfg.BatchTimeoutMs = int(bt)
	}
	if ts, ok := data["timeoutSec"].(float64); ok && ts > 0 {
		cfg.TimeoutSec = int(ts)
	}

	// TLS
	if tls, ok := data["tls"].(map[string]interface{}); ok {
		if enabled, ok := tls["enabled"].(bool); ok {
			cfg.TLS.Enabled = enabled
		}
		if skip, ok := tls["insecureSkipVerify"].(bool); ok {
			cfg.TLS.InsecureSkipVerify = skip
		}
	} else if enabled, ok := data["tls"].(bool); ok {
		cfg.TLS.Enabled = enabled
	}

	// SASL
	if sasl, ok := data["sasl"].(map[string]interface{}); ok {
		cfg.SASL = &models.SASLConfig{
			Mechanism: toString(sasl["mechanism"]),
			Username:  toString(sasl["username"]),
			Password:  toString(sasl["password"]),
		}
	}

	return cfg
}

func parseKafkaConnection(data map[string]interface{}) models.KafkaConnection {
	return models.KafkaConnection{
		ID:     toString(data["id"]),
		Name:   toString(data["name"]),
		Config: parseKafkaConfig(data),
	}
}

func parseStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []interface{}:
		result := make([]string, len(val))
		for i, item := range val {
			result[i] = toString(item)
		}
		return result
	case []string:
		return val
	case string:
		if val == "" {
			return nil
		}
		return strings.Split(val, ",")
	}
	return nil
}

func generateJSONSchema(body string) interface{} {
	var data interface{}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return map[string]interface{}{"type": "string"}
	}
	// Build a minimal JSON schema from the sample
	return inferSchema(data)
}

func inferSchema(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
		props := schema["properties"].(map[string]interface{})
		for key, val := range v {
			props[key] = inferSchema(val)
		}
		return schema
	case []interface{}:
		schema := map[string]interface{}{
			"type":  "array",
			"items": map[string]interface{}{},
		}
		if len(v) > 0 {
			schema["items"] = inferSchema(v[0])
		}
		return schema
	case float64:
		if v == float64(int64(v)) {
			return map[string]interface{}{"type": "integer"}
		}
		return map[string]interface{}{"type": "number"}
	case bool:
		return map[string]interface{}{"type": "boolean"}
	case string:
		return map[string]interface{}{"type": "string"}
	default:
		return map[string]interface{}{"type": "string"}
	}
}

func validateAgainstSchema(body string, schema interface{}) (bool, []string) {
	var data interface{}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return false, []string{"invalid JSON body: " + err.Error()}
	}
	errs := validateValue(data, schema, "$")
	return len(errs) == 0, errs
}

func validateValue(data interface{}, schema interface{}, path string) []string {
	schemaMap, ok := schema.(map[string]interface{})
	if !ok {
		return nil
	}

	schemaType, _ := schemaMap["type"].(string)
	if schemaType == "" {
		return nil
	}

	var errs []string

	switch schemaType {
	case "object":
		if data == nil {
			errs = append(errs, path+": expected object, got null")
			return errs
		}
		dataMap, ok := data.(map[string]interface{})
		if !ok {
			errs = append(errs, path+": expected object")
			return errs
		}
		if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
			for key, propSchema := range props {
				val, exists := dataMap[key]
				if !exists {
					if required, ok := schemaMap["required"].([]interface{}); ok {
						for _, r := range required {
							if r.(string) == key {
								errs = append(errs, path+"."+key+": missing required field")
							}
						}
					}
					continue
				}
				errs = append(errs, validateValue(val, propSchema, path+"."+key)...)
			}
		}
	case "array":
		dataArr, ok := data.([]interface{})
		if !ok {
			errs = append(errs, path+": expected array")
			return errs
		}
		if items, ok := schemaMap["items"]; ok {
			for i, item := range dataArr {
				errs = append(errs, validateValue(item, items, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case "string":
		if _, ok := data.(string); !ok && data != nil {
			errs = append(errs, path+": expected string")
		}
	case "number":
		if _, ok := data.(float64); !ok && data != nil {
			errs = append(errs, path+": expected number")
		}
	case "integer":
		f, ok := data.(float64)
		if !ok && data != nil {
			errs = append(errs, path+": expected integer")
		} else if ok && f != float64(int64(f)) {
			errs = append(errs, path+": expected integer")
		}
	case "boolean":
		if _, ok := data.(bool); !ok && data != nil {
			errs = append(errs, path+": expected boolean")
		}
	}

	return errs
}


