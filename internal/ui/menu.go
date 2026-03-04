package ui

import (
	"fmt"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
)

type Menu struct {
	Storage   *storage.Manager
	Processor *processor.ScriptProcessor
	Client    *api.Client
}

func NewMenu(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client) *Menu {
	return &Menu{Storage: store, Processor: proc, Client: client}
}

func (m *Menu) ManageGlobalHeaders() {
	for {
		headerOptions := []string{"Add New Global Header", "Finish"}
		for _, h := range m.Storage.GlobalHeaders {
			headerOptions = append(headerOptions, fmt.Sprintf("%s: %s", h.Key, h.Value))
		}

		selectedHeader := ""
		survey.AskOne(&survey.Select{Message: "Select global header:", Options: headerOptions}, &selectedHeader)

		if selectedHeader == "Finish" {
			break
		}

		if selectedHeader == "Add New Global Header" {
			key, val := "", ""
			survey.AskOne(&survey.Input{Message: "Header Key:"}, &key)
			survey.AskOne(&survey.Input{Message: "Header Value:"}, &val)
			if key != "" {
				m.Storage.GlobalHeaders = append(m.Storage.GlobalHeaders, models.Header{Key: key, Value: val})
				m.Storage.SaveGlobalHeaders()
			}
		} else {
			for i, h := range m.Storage.GlobalHeaders {
				if fmt.Sprintf("%s: %s", h.Key, h.Value) == selectedHeader {
					action := ""
					survey.AskOne(&survey.Select{Message: "Action:", Options: []string{"Edit", "Delete", "Cancel"}}, &action)
					if action == "Edit" {
						newVal := h.Value
						survey.AskOne(&survey.Input{Message: fmt.Sprintf("New value for %s:", h.Key), Default: h.Value}, &newVal)
						m.Storage.GlobalHeaders[i].Value = newVal
						m.Storage.SaveGlobalHeaders()
					} else if action == "Delete" {
						m.Storage.GlobalHeaders = append(m.Storage.GlobalHeaders[:i], m.Storage.GlobalHeaders[i+1:]...)
						m.Storage.SaveGlobalHeaders()
					}
					break
				}
			}
		}
	}
}

func (m *Menu) ViewVariables() {
	for {
		keys := []string{"Add New Variable", "Finish"}
		for k, v := range m.Storage.VariableMap {
			keys = append(keys, fmt.Sprintf("%s: %s", k, v))
		}

		selected := ""
		survey.AskOne(&survey.Select{Message: "Select variable:", Options: keys}, &selected)
		if selected == "Finish" {
			break
		}

		if selected == "Add New Variable" {
			key, val := "", ""
			survey.AskOne(&survey.Input{Message: "Variable Name:"}, &key)
			survey.AskOne(&survey.Input{Message: "Value:"}, &val)
			if key != "" {
				m.Storage.SetVariable(key, val)
			}
		} else {
			key := strings.Split(selected, ": ")[0]
			val := m.Storage.VariableMap[key]
			survey.AskOne(&survey.Input{Message: fmt.Sprintf("New value for %s:", key), Default: val}, &val)
			m.Storage.SetVariable(key, val)
		}
	}
}

func (m *Menu) CreateNewRequest() *models.RequestInfo {
	path := ""
	method := "GET"
	url := ""

	survey.AskOne(&survey.Input{Message: "Request Path (e.g. Folder > My Request):"}, &path)
	if path == "" {
		return nil
	}

	survey.AskOne(&survey.Select{
		Message: "Method:",
		Options: []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
		Default: "GET",
	}, &method)

	survey.AskOne(&survey.Input{Message: "URL:", Default: "https://"}, &url)

	newReq := models.RequestInfo{
		Path: path,
		Request: &models.Request{
			Method: method,
			URL:    models.URL{Raw: url},
		},
		Order: 999, // Will be sorted
	}

	m.Storage.SaveSingleRequest(newReq)
	fmt.Printf("Request created at: %s\n", path)
	return &newReq
}

func (m *Menu) DuplicateRequest(reqInfo *models.RequestInfo) *models.RequestInfo {
	newPath := ""
	survey.AskOne(&survey.Input{
		Message: "New Path:",
		Default: reqInfo.Path + " Copy",
	}, &newPath)

	if newPath == "" {
		return nil
	}

	// Clone events
	var eventsCopy []models.Event
	if reqInfo.Events != nil {
		eventsCopy = make([]models.Event, len(reqInfo.Events))
		for i, e := range reqInfo.Events {
			eventsCopy[i] = e
			if e.Script.Exec != nil {
				eventsCopy[i].Script.Exec = make([]string, len(e.Script.Exec))
				copy(eventsCopy[i].Script.Exec, e.Script.Exec)
			}
		}
	}

	newReq := models.RequestInfo{
		Path:    newPath,
		Request: reqInfo.Request.DeepCopy(),
		Events:  eventsCopy,
		Order:   reqInfo.Order + 1,
	}

	m.Storage.SaveSingleRequest(newReq)
	fmt.Printf("Request duplicated to: %s\n", newPath)
	return &newReq
}

func (m *Menu) MoveRequest(reqInfo *models.RequestInfo) {
	newPath := ""
	survey.AskOne(&survey.Input{
		Message: "New Path (e.g. Folder > Name):",
		Default: reqInfo.Path,
	}, &newPath)

	if newPath != "" && newPath != reqInfo.Path {
		reqInfo.Path = newPath
		m.Storage.SaveSingleRequest(*reqInfo)
		fmt.Printf("Request moved to: %s\n", newPath)
	}
}

func (m *Menu) ReorderRequests(allRequests *[]models.RequestInfo) {
	options := []string{"Finish"}
	for _, r := range *allRequests {
		options = append(options, r.Path)
	}

	for {
		selected := ""
		survey.AskOne(&survey.Select{
			Message: "Select request to move UP:",
			Options: options,
		}, &selected)

		if selected == "Finish" {
			break
		}

		idx := -1
		for i, r := range *allRequests {
			if r.Path == selected {
				idx = i
				break
			}
		}

		if idx > 0 {
			// Swap with previous
			(*allRequests)[idx].Order, (*allRequests)[idx-1].Order = (*allRequests)[idx-1].Order, (*allRequests)[idx].Order
			
			// If orders were same, force differentiation
			if (*allRequests)[idx].Order == (*allRequests)[idx-1].Order {
				(*allRequests)[idx-1].Order--
			}

			m.Storage.SaveSingleRequest((*allRequests)[idx])
			m.Storage.SaveSingleRequest((*allRequests)[idx-1])

			// Re-sort options for the next iteration
			options = []string{"Finish"}
			for _, r := range *allRequests {
				options = append(options, r.Path)
			}
			fmt.Println("Moved up!")
		} else if idx == 0 {
			fmt.Println("Already at the top!")
		}
	}
}

func (m *Menu) HandleRequestSelection(reqInfo *models.RequestInfo, allRequests *[]models.RequestInfo) {
	for {
		action := ""
		prompt := &survey.Select{
			Message: fmt.Sprintf("Action for [%s]:", reqInfo.Path),
			Options: []string{"Send", "Edit Body", "Edit Headers", "Move/Rename", "Duplicate", "Reorder All", "Back"},
		}
		survey.AskOne(prompt, &action)

		switch action {
		case "Send":
			fmt.Println("\n--- Executing Scripts BEFORE Request ---")
			m.Processor.RunScripts(reqInfo.Events, "prerequest", nil, nil, reqInfo.Request.Header)
			m.Processor.RunScripts(reqInfo.Events, "test", nil, nil, reqInfo.Request.Header)
			
			startTime := time.Now()
			respBody, respHeaders, statusCode, statusText := m.Client.ExecuteRequest(reqInfo.Request)
			duration := time.Since(startTime).Milliseconds()

			// Record History
			go func() {
				history := m.Storage.LoadHistory()
				record := models.HistoryRecord{
					Timestamp:       startTime,
					Path:            reqInfo.Path,
					Method:          reqInfo.Request.Method,
					URL:             m.Processor.ResolveVariables(reqInfo.Request.URL.Raw),
					StatusCode:      statusCode,
					StatusText:      statusText,
					Duration:        duration,
					ResponseBody:    respBody,
					ResponseHeaders: respHeaders,
				}
				history = append(history, record)
				m.Storage.SaveHistory(history)
			}()

			fmt.Printf("\nResponse Status: %d %s\n", statusCode, statusText)
			
			if respBody != "" || len(respHeaders) > 0 {
				fmt.Println("\n--- Executing Scripts AFTER Request ---")
				m.Processor.RunScripts(reqInfo.Events, "test", []byte(respBody), respHeaders, reqInfo.Request.Header)
			}
			return
		case "Edit Body":
			m.EditBody(reqInfo)
		case "Edit Headers":
			if m.EditRequestHeaders(reqInfo.Request) {
				m.Storage.SaveSingleRequest(*reqInfo)
			}
		case "Move/Rename":
			m.MoveRequest(reqInfo)
		case "Duplicate":
			if newReq := m.DuplicateRequest(reqInfo); newReq != nil {
				*allRequests = append(*allRequests, *newReq)
			}
			return
		case "Reorder All":
			m.ReorderRequests(allRequests)
		case "Back":
			return
		}
	}
}

func (m *Menu) EditBody(reqInfo *models.RequestInfo) {
	if reqInfo.Request.Body == nil {
		reqInfo.Request.Body = &models.Body{Mode: "raw"}
	}

	if reqInfo.Request.Body.Mode == "urlencoded" {
		for {
			options := []string{"Add New Field", "Finish Editing"}
			for _, f := range reqInfo.Request.Body.UrlEncoded {
				options = append(options, fmt.Sprintf("%s: %s", f.Key, f.Value))
			}

			selected := ""
			survey.AskOne(&survey.Select{Message: "Select field:", Options: options}, &selected)
			if selected == "Finish Editing" {
				break
			}

			if selected == "Add New Field" {
				key, val := "", ""
				survey.AskOne(&survey.Input{Message: "Key:"}, &key)
				survey.AskOne(&survey.Input{Message: "Value:"}, &val)
				if key != "" {
					reqInfo.Request.Body.UrlEncoded = append(reqInfo.Request.Body.UrlEncoded, models.UrlEncoded{Key: key, Value: val})
				}
			} else {
				for i, f := range reqInfo.Request.Body.UrlEncoded {
					if fmt.Sprintf("%s: %s", f.Key, f.Value) == selected {
						newVal := f.Value
						survey.AskOne(&survey.Input{Message: "New value:", Default: f.Value}, &newVal)
						reqInfo.Request.Body.UrlEncoded[i].Value = newVal
						break
					}
				}
			}
		}
	} else {
		body := reqInfo.Request.Body.Raw
		prompt := &survey.Editor{
			Message:       "Edit Body (Raw)",
			Default:       body,
			AppendDefault: true,
			FileName:      "*.json",
		}
		survey.AskOne(prompt, &body)
		reqInfo.Request.Body.Raw = body
	}
	m.Storage.SaveSingleRequest(*reqInfo)
}

func (m *Menu) EditRequestHeaders(req *models.Request) bool {
	changed := false
	for {
		headerOptions := []string{"Add New Header", "Finish Editing"}
		for _, h := range req.Header {
			headerOptions = append(headerOptions, fmt.Sprintf("%s: %s", h.Key, h.Value))
		}
		selectedHeader := ""
		survey.AskOne(&survey.Select{Message: "Select header:", Options: headerOptions}, &selectedHeader)
		if selectedHeader == "Finish Editing" { break }
		changed = true
		if selectedHeader == "Add New Header" {
			key, val := "", ""
			survey.AskOne(&survey.Input{Message: "Key:"}, &key)
			survey.AskOne(&survey.Input{Message: "Value:"}, &val)
			if key != "" { req.Header = append(req.Header, models.Header{Key: key, Value: val}) }
		} else {
			for i, h := range req.Header {
				if fmt.Sprintf("%s: %s", h.Key, h.Value) == selectedHeader {
					newVal := h.Value
					survey.AskOne(&survey.Input{Message: fmt.Sprintf("New value for %s:", h.Key), Default: h.Value}, &newVal)
					req.Header[i].Value = newVal
					break
				}
			}
		}
	}
	return changed
}
