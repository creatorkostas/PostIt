package ui

import (
	"fmt"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"

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

func (m *Menu) HandleRequestSelection(reqInfo *models.RequestInfo) {
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
			m.Processor.RunScripts(reqInfo.Events, "prerequest", nil, nil, reqInfo.Request.Header)
			m.Processor.RunScripts(reqInfo.Events, "test", nil, nil, reqInfo.Request.Header)
			
			respBody, respHeaders := m.Client.ExecuteRequest(reqInfo.Request)
			
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
			for _, f := range reqInfo.Request.Body.Urlencoded {
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
					reqInfo.Request.Body.Urlencoded = append(reqInfo.Request.Body.Urlencoded, models.Urlencoded{Key: key, Value: val})
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
