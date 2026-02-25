package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/go-resty/resty/v2"
	"github.com/tidwall/gjson"
)

// PostmanCollection models
type Collection struct {
	Info Info   `json:"info"`
	Item []Item `json:"item"`
}

type Info struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type Item struct {
	Name    string   `json:"name"`
	Event   []Event  `json:"event,omitempty"`
	Item    []Item   `json:"item,omitempty"`
	Request *Request `json:"request,omitempty"`
}

type Event struct {
	Listen string `json:"listen"`
	Script Script `json:"script"`
}

type Script struct {
	Exec []string `json:"exec"`
	Type string   `json:"type"`
}

type Request struct {
	Method string   `json:"method"`
	Header []Header `json:"header"`
	Body   *Body    `json:"body,omitempty"`
	URL    URL      `json:"url"`
	Auth   *Auth    `json:"auth,omitempty"`
}

type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Body struct {
	Mode       string       `json:"mode"`
	Raw        string       `json:"raw,omitempty"`
	Urlencoded []Urlencoded `json:"urlencoded,omitempty"`
	Options    *BodyOptions `json:"options,omitempty"`
}

type BodyOptions struct {
	Raw *RawOptions `json:"raw,omitempty"`
}

type RawOptions struct {
	Language string `json:"language,omitempty"`
}

type Urlencoded struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

type URL struct {
	Raw   string   `json:"raw"`
	Host  []string `json:"host"`
	Path  []string `json:"path"`
	Query []Query  `json:"query"`
}

type Query struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Auth struct {
	Type   string   `json:"type"`
	Bearer []Bearer `json:"bearer,omitempty"`
}

type Bearer struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

// Internal structures
type RequestInfo struct {
	Path    string   `json:"path"`
	Request *Request `json:"request"`
	Events  []Event  `json:"events,omitempty"`
}

var (
	variableMap   = make(map[string]string)
	globalHeaders = []Header{}
	reVar         = regexp.MustCompile(`\{\{([^}]+)\}\}`)
	outputDir     = "output"
	varsPath      = filepath.Join(outputDir, "variables.json")
	headersPath   = filepath.Join(outputDir, "global_headers.json")

	// Script Regexes
	reSetVar    = regexp.MustCompile(`pm\.(collectionVariables|environment|globals)\.set\(['"](.+?)['"]\s*,\s*(.+?)\)`)
	reGetVar    = regexp.MustCompile(`pm\.(collectionVariables|environment|globals)\.get\(['"](.+?)['"]\)`)
	reJsonPath  = regexp.MustCompile(`pm\.response\.json\(\)\?*\.(.+?)(?:;|\n|$)`)
	reHeaderGet = regexp.MustCompile(`pm\.(response|request)\.headers\.get\(['"](.+?)['"]\)`)
	reAssign    = regexp.MustCompile(`(?:const|let|var|)\s*(\w+)\s*=\s*(.+?)(?:;|\n|$)`)
	reIf        = regexp.MustCompile(`if\s*\((.+?)\)`)
)

func main() {
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		os.Mkdir(outputDir, 0755)
	}

	loadVariables()
	loadGlobalHeaders()
	cache := loadCache()

	filePath := "dx4b.postman_collection.json"
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	var collection Collection
	if err := json.Unmarshal(data, &collection); err != nil {
		log.Fatalf("Error parsing JSON: %v", err)
	}

	freshRequests := []RequestInfo{}
	flattenItems(collection.Item, "", []Event{}, &freshRequests)

	finalRequests := []RequestInfo{}
	for _, fresh := range freshRequests {
		if cached, ok := cache[fresh.Path]; ok {
			cached.Events = fresh.Events
			finalRequests = append(finalRequests, cached)
		} else {
			finalRequests = append(finalRequests, fresh)
			saveSingleRequest(fresh)
		}
	}

	if len(finalRequests) == 0 {
		fmt.Println("No requests found.")
		return
	}

	for {
		options := []string{"[Manage Global Headers]", "[Manage Global Variables]"}
		for _, r := range finalRequests {
			options = append(options, r.Path)
		}

		var selected string
		prompt := &survey.Select{
			Message:  "Select a request or action:",
			Options:  options,
			PageSize: 15,
		}

		if err := survey.AskOne(prompt, &selected); err != nil {
			break
		}

		switch selected {
		case "[Manage Global Headers]":
			manageGlobalHeaders()
		case "[Manage Global Variables]":
			viewVariables()
		default:
			var selectedIdx int
			for i, r := range finalRequests {
				if r.Path == selected {
					selectedIdx = i
					break
				}
			}
			handleRequestSelection(&finalRequests[selectedIdx])
		}
		fmt.Println("\n--------------------------------------------------")
	}
}

func loadVariables() {
	if _, err := os.Stat(varsPath); err == nil {
		data, err := ioutil.ReadFile(varsPath)
		if err == nil {
			json.Unmarshal(data, &variableMap)
		}
	}
}

func saveVariables() {
	data, err := json.MarshalIndent(variableMap, "", "  ")
	if err == nil {
		ioutil.WriteFile(varsPath, data, 0644)
	}
}

func loadGlobalHeaders() {
	if _, err := os.Stat(headersPath); err == nil {
		data, err := ioutil.ReadFile(headersPath)
		if err == nil {
			json.Unmarshal(data, &globalHeaders)
		}
	}
}

func saveGlobalHeaders() {
	data, err := json.MarshalIndent(globalHeaders, "", "  ")
	if err == nil {
		ioutil.WriteFile(headersPath, data, 0644)
	}
}

func setVariable(key, value string) {
	variableMap[key] = value
	saveVariables()
}

func manageGlobalHeaders() {
	for {
		headerOptions := []string{"Add New Global Header", "Finish"}
		for _, h := range globalHeaders {
			headerOptions = append(headerOptions, fmt.Sprintf("%s: %s", h.Key, h.Value))
		}

		selectedHeader := ""
		survey.AskOne(&survey.Select{Message: "Select global header to edit:", Options: headerOptions}, &selectedHeader)

		if selectedHeader == "Finish" {
			break
		}

		if selectedHeader == "Add New Global Header" {
			key, val := "", ""
			survey.AskOne(&survey.Input{Message: "Header Key:"}, &key)
			survey.AskOne(&survey.Input{Message: "Header Value:"}, &val)
			if key != "" {
				globalHeaders = append(globalHeaders, Header{Key: key, Value: val})
				saveGlobalHeaders()
			}
		} else {
			for i, h := range globalHeaders {
				if fmt.Sprintf("%s: %s", h.Key, h.Value) == selectedHeader {
					action := ""
					survey.AskOne(&survey.Select{Message: "Action:", Options: []string{"Edit", "Delete", "Cancel"}}, &action)
					if action == "Edit" {
						newVal := h.Value
						survey.AskOne(&survey.Input{Message: fmt.Sprintf("New value for %s:", h.Key), Default: h.Value}, &newVal)
						globalHeaders[i].Value = newVal
						saveGlobalHeaders()
					} else if action == "Delete" {
						globalHeaders = append(globalHeaders[:i], globalHeaders[i+1:]...)
						saveGlobalHeaders()
					}
					break
				}
			}
		}
	}
}

func handleRequestSelection(reqInfo *RequestInfo) {
	for {
		action := ""
		prompt := &survey.Select{
			Message: fmt.Sprintf("Action for [%s]:", reqInfo.Path),
			Options: []string{"Send", "Edit Body", "Edit Headers", "Back"},
		}
		survey.AskOne(prompt, &action)

		switch action {
		case "Send":
			fmt.Println("\n--- Executing Scripts BEFORE Request ---")
			runScripts(reqInfo.Events, "prerequest", nil, nil, reqInfo.Request.Header)
			runScripts(reqInfo.Events, "test", nil, nil, reqInfo.Request.Header)
			
			respBody, respHeaders := executeRequest(reqInfo.Request)
			
			if respBody != "" || len(respHeaders) > 0 {
				fmt.Println("\n--- Executing Scripts AFTER Request ---")
				runScripts(reqInfo.Events, "test", []byte(respBody), respHeaders, reqInfo.Request.Header)
			}
			return
		case "Edit Body":
			editBody(reqInfo)
		case "Edit Headers":
			if editHeaders(reqInfo.Request) {
				saveSingleRequest(*reqInfo)
			}
		case "Back":
			return
		}
	}
}

func viewVariables() {
	for {
		keys := []string{"Add New Variable", "Finish"}
		for k, v := range variableMap {
			keys = append(keys, fmt.Sprintf("%s: %s", k, v))
		}

		selected := ""
		survey.AskOne(&survey.Select{Message: "Select variable to edit:", Options: keys}, &selected)
		if selected == "Finish" {
			break
		}

		if selected == "Add New Variable" {
			key, val := "", ""
			survey.AskOne(&survey.Input{Message: "Variable Name:"}, &key)
			survey.AskOne(&survey.Input{Message: "Value:"}, &val)
			if key != "" {
				setVariable(key, val)
			}
		} else {
			key := strings.Split(selected, ": ")[0]
			val := variableMap[key]
			survey.AskOne(&survey.Input{Message: fmt.Sprintf("New value for %s:", key), Default: val}, &val)
			setVariable(key, val)
		}
	}
}

func editBody(reqInfo *RequestInfo) {
	if reqInfo.Request.Body == nil {
		reqInfo.Request.Body = &Body{Mode: "raw"}
	}

	if reqInfo.Request.Body.Mode == "urlencoded" {
		for {
			options := []string{"Add New Field", "Finish Editing"}
			for _, f := range reqInfo.Request.Body.Urlencoded {
				options = append(options, fmt.Sprintf("%s: %s", f.Key, f.Value))
			}

			selected := ""
			survey.AskOne(&survey.Select{Message: "Select field to edit:", Options: options}, &selected)
			if selected == "Finish Editing" {
				break
			}

			if selected == "Add New Field" {
				key, val := "", ""
				survey.AskOne(&survey.Input{Message: "Key:"}, &key)
				survey.AskOne(&survey.Input{Message: "Value:"}, &val)
				if key != "" {
					reqInfo.Request.Body.Urlencoded = append(reqInfo.Request.Body.Urlencoded, Urlencoded{Key: key, Value: val})
				}
			} else {
				for i, f := range reqInfo.Request.Body.Urlencoded {
					if fmt.Sprintf("%s: %s", f.Key, f.Value) == selected {
						newVal := f.Value
						survey.AskOne(&survey.Input{Message: "New value:", Default: f.Value}, &newVal)
						reqInfo.Request.Body.Urlencoded[i].Value = newVal
						break
					}
				}
			}
		}
	} else {
		body := reqInfo.Request.Body.Raw
		prompt := &survey.Editor{
			Message:       "Edit Request Body (Raw)",
			Default:       body,
			AppendDefault: true,
			FileName:      "*.json",
		}
		survey.AskOne(prompt, &body)
		reqInfo.Request.Body.Raw = body
	}
	saveSingleRequest(*reqInfo)
}

func runScripts(events []Event, typeFilter string, responseBody []byte, respHeaders map[string][]string, reqHeaders []Header) {
	localVars := make(map[string]string)
	for _, event := range events {
		if event.Listen != typeFilter {
			continue
		}
		processLines(event.Script.Exec, responseBody, respHeaders, reqHeaders, localVars)
	}
}

func processLines(lines []string, responseBody []byte, respHeaders map[string][]string, reqHeaders []Header, localVars map[string]string) {
	var bodyStr *string
	if responseBody != nil {
		s := string(responseBody)
		bodyStr = &s
	}

	skipping := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Handle Logic Blocks
		if m := reIf.FindStringSubmatch(line); len(m) > 1 {
			cond := m[1]
			result := evaluateCondition(cond, localVars)
			skipping = !result
			continue
		}
		if strings.Contains(line, "}else{") || strings.Contains(line, "else {") {
			skipping = !skipping // Switch branch
			continue
		}
		if line == "}" {
			skipping = false
			continue
		}

		if skipping {
			continue
		}

		// Assignments
		if m := reAssign.FindStringSubmatch(line); len(m) > 2 {
			varName := m[1]
			valRaw := strings.TrimSpace(m[2])
			val := resolveValue(valRaw, bodyStr, respHeaders, reqHeaders, localVars)
			if val != "" {
				localVars[varName] = val
			}
		}

		// Sets
		if m := reSetVar.FindStringSubmatch(line); len(m) > 3 {
			key := strings.Trim(m[2], "'\"")
			valRaw := strings.TrimSpace(m[3])
			val := resolveValue(valRaw, bodyStr, respHeaders, reqHeaders, localVars)
			if val != "" {
				setVariable(key, val)
				if responseBody != nil {
					fmt.Printf(" [Script] Variable Saved: %s = %s\n", key, val)
				}
			}
		}
	}
}

func evaluateCondition(cond string, localVars map[string]string) bool {
	// Very simple evaluator for patterns like: useRemote === "true"
	cond = strings.ReplaceAll(cond, "===", "==")
	parts := strings.Split(cond, "==")
	if len(parts) != 2 {
		return true // Default to true if too complex
	}

	left := strings.TrimSpace(parts[0])
	right := strings.Trim(strings.TrimSpace(parts[1]), "'\"")

	leftVal := left
	if val, ok := localVars[left]; ok {
		leftVal = val
	} else if val, ok := variableMap[left]; ok {
		leftVal = val
	}

	return leftVal == right
}

func resolveValue(raw string, body *string, respHeaders map[string][]string, reqHeaders []Header, localVars map[string]string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Handle Concatenation
	if strings.Contains(raw, "+") {
		parts := strings.Split(raw, "+")
		result := ""
		for _, p := range parts {
			result += resolveValue(p, body, respHeaders, reqHeaders, localVars)
		}
		return result
	}

	// String literal
	if strings.HasPrefix(raw, "'") || strings.HasPrefix(raw, "\"") {
		return strings.Trim(raw, "'\"")
	}

	// pm.get
	if m := reGetVar.FindStringSubmatch(raw); len(m) > 2 {
		key := strings.Trim(m[2], "'\"")
		val, ok := variableMap[key]
		if !ok || val == "" {
			return getOrPrompt(key)
		}
		return val
	}

	// pm.headers.get
	if m := reHeaderGet.FindStringSubmatch(raw); len(m) > 2 {
		source := m[1]
		hName := strings.Trim(m[2], "'\"")
		if source == "response" && respHeaders != nil {
			if vals, ok := respHeaders[httpHeaderKey(hName, respHeaders)]; ok && len(vals) > 0 {
				return vals[0]
			}
		} else if source == "request" && reqHeaders != nil {
			for _, h := range reqHeaders {
				if strings.EqualFold(h.Key, hName) {
					return h.Value
				}
			}
		}
	}

	// pm.response.json()
	if body != nil && strings.Contains(raw, "pm.response.json()") {
		if m := reJsonPath.FindStringSubmatch(raw); len(m) > 1 {
			jsPath := strings.ReplaceAll(m[1], "?.", ".")
			return gjson.Get(*body, jsPath).String()
		}
	}

	// Local JS Variable lookup
	if val, ok := localVars[raw]; ok {
		return val
	}

	return ""
}

func httpHeaderKey(key string, headers map[string][]string) string {
	for k := range headers {
		if strings.EqualFold(k, key) {
			return k
		}
	}
	return key
}

func getOrPrompt(name string) string {
	if val, ok := variableMap[name]; ok && val != "" {
		return val
	}
	if val := os.Getenv(name); val != "" {
		setVariable(name, val)
		return val
	}
	var val string
	prompt := &survey.Input{Message: fmt.Sprintf("Variable '%s' required. Value:", name)}
	survey.AskOne(prompt, &val)
	setVariable(name, val)
	return val
}

func flattenItems(items []Item, prefix string, parentEvents []Event, result *[]RequestInfo) {
	for _, item := range items {
		name := item.Name
		if prefix != "" {
			name = prefix + " > " + name
		}
		currentEvents := append([]Event{}, parentEvents...)
		currentEvents = append(currentEvents, item.Event...)
		if item.Request != nil {
			*result = append(*result, RequestInfo{Path: name, Request: item.Request, Events: currentEvents})
		}
		if len(item.Item) > 0 {
			flattenItems(item.Item, name, currentEvents, result)
		}
	}
}

func getSafeFilename(path string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	safe := reg.ReplaceAllString(path, "_")
	regMulti := regexp.MustCompile(`_+`)
	safe = regMulti.ReplaceAllString(safe, "_")
	return strings.Trim(safe, "_") + ".json"
}

func loadCache() map[string]RequestInfo {
	cache := make(map[string]RequestInfo)
	files, _ := ioutil.ReadDir(outputDir)
	for _, file := range files {
		name := file.Name()
		if !file.IsDir() && strings.HasSuffix(name, ".json") && name != "variables.json" && name != "global_headers.json" {
			data, _ := ioutil.ReadFile(filepath.Join(outputDir, name))
			var req RequestInfo
			if err := json.Unmarshal(data, &req); err == nil {
				cache[req.Path] = req
			}
		}
	}
	return cache
}

func saveSingleRequest(req RequestInfo) {
	filename := getSafeFilename(req.Path)
	data, _ := json.MarshalIndent(req, "", "  ")
	ioutil.WriteFile(filepath.Join(outputDir, filename), data, 0644)
}

func resolveVariables(text string) string {
	return reVar.ReplaceAllStringFunc(text, func(m string) string {
		varName := m[2 : len(m)-2]
		if val, ok := variableMap[varName]; ok && val != "" {
			return val
		}
		if val := os.Getenv(varName); val != "" {
			setVariable(varName, val)
			return val
		}
		var val string
		prompt := &survey.Input{Message: fmt.Sprintf("Enter value for variable '%s':", varName)}
		survey.AskOne(prompt, &val)
		setVariable(varName, val)
		return val
	})
}

func executeRequest(req *Request) (string, map[string][]string) {
	client := resty.New()
	url := resolveVariables(req.URL.Raw)
	method := strings.ToUpper(req.Method)
	r := client.R()
	contentType := ""
	if req.Body != nil {
		if req.Body.Mode == "urlencoded" {
			contentType = "application/x-www-form-urlencoded"
		} else if req.Body.Mode == "raw" && req.Body.Options != nil && req.Body.Options.Raw != nil {
			if req.Body.Options.Raw.Language == "json" { contentType = "application/json" }
		}
	}
	for _, h := range globalHeaders { r.SetHeader(h.Key, resolveVariables(h.Value)) }
	for _, h := range req.Header { r.SetHeader(h.Key, resolveVariables(h.Value)) }
	if contentType != "" && r.Header.Get("Content-Type") == "" { r.SetHeader("Content-Type", contentType) }
	if req.Body != nil {
		if req.Body.Mode == "raw" { r.SetBody(resolveVariables(req.Body.Raw)) } else if req.Body.Mode == "urlencoded" {
			formData := make(map[string]string)
			for _, f := range req.Body.Urlencoded { formData[resolveVariables(f.Key)] = resolveVariables(f.Value) }
			r.SetFormData(formData)
		}
	}
	if req.Auth != nil && req.Auth.Type == "bearer" {
		for _, b := range req.Auth.Bearer { if b.Key == "token" { r.SetAuthToken(resolveVariables(b.Value)) } }
	}
	fmt.Printf("\nSending %s %s...\n", method, url)
	var resp *resty.Response
	var err error
	switch method {
	case "GET": resp, err = r.Get(url)
	case "POST": resp, err = r.Post(url)
	case "PUT": resp, err = r.Put(url)
	case "DELETE": resp, err = r.Delete(url)
	case "PATCH": resp, err = r.Patch(url)
	default: return "", nil
	}
	if err != nil { return "", nil }
	fmt.Printf("Status: %s (%v)\n", resp.Status(), resp.Time())
	body := string(resp.Body())
	if body != "" {
		var prettyJSON interface{}
		if err := json.Unmarshal(resp.Body(), &prettyJSON); err == nil {
			formatted, _ := json.MarshalIndent(prettyJSON, "", "  ")
			fmt.Println(string(formatted))
		} else { fmt.Println(body) }
	}
	return body, resp.Header()
}

func editHeaders(req *Request) bool {
	changed := false
	for {
		headerOptions := []string{"Add New Header", "Finish Editing"}
		for _, h := range req.Header { headerOptions = append(headerOptions, fmt.Sprintf("%s: %s", h.Key, h.Value)) }
		selectedHeader := ""
		survey.AskOne(&survey.Select{Message: "Select header:", Options: headerOptions}, &selectedHeader)
		if selectedHeader == "Finish Editing" { break }
		changed = true
		if selectedHeader == "Add New Header" {
			key, val := "", ""
			survey.AskOne(&survey.Input{Message: "Key:"}, &key)
			survey.AskOne(&survey.Input{Message: "Value:"}, &val)
			if key != "" { req.Header = append(req.Header, Header{Key: key, Value: val}) }
		} else {
			for i, h := range req.Header {
				if fmt.Sprintf("%s: %s", h.Key, h.Value) == selectedHeader {
					newVal := h.Value
					survey.AskOne(&survey.Input{Message: "New value:", Default: h.Value}, &newVal)
					req.Header[i].Value = newVal
					break
				}
			}
		}
	}
	return changed
}
