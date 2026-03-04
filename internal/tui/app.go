package tui

import (
	"encoding/json"
	"fmt"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type RequestPanel struct {
	Main       *tview.Flex
	Response   *tview.TextView
	CurrentReq *models.RequestInfo
	Title      string
}

type TUIApp struct {
	App       *tview.Application
	Storage   *storage.Manager
	Processor *processor.ScriptProcessor
	Client    *api.Client

	// Data
	Collection models.Collection
	Cached     []models.RequestInfo

	// Layout Components
	Tree       *tview.TreeView
	LeftPanel  *RequestPanel
	RightPanel *RequestPanel
	ActivePanel *RequestPanel
	Status     *tview.TextView
}

func NewTUIApp(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client) *TUIApp {
	return &TUIApp{
		App:       tview.NewApplication(),
		Storage:   store,
		Processor: proc,
		Client:    client,
	}
}

func (t *TUIApp) newRequestPanel(title string) *RequestPanel {
	main := tview.NewFlex().SetDirection(tview.FlexRow)
	main.SetBorder(true).SetTitle(title + " Request")
	
	resp := tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true)
	resp.SetBorder(true).SetTitle(title + " Response")
	
	return &RequestPanel{
		Main:     main,
		Response: resp,
		Title:    title,
	}
}

func (t *TUIApp) getMainLayout() tview.Primitive {
	leftSide := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.LeftPanel.Main, 0, 1, false).
		AddItem(t.LeftPanel.Response, 0, 1, false)

	rightSide := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.RightPanel.Main, 0, 1, false).
		AddItem(t.RightPanel.Response, 0, 1, false)

	editors := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(leftSide, 0, 1, false).
		AddItem(rightSide, 0, 1, false)

	mainContent := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(editors, 0, 1, false).
		AddItem(t.Status, 1, 0, false)

	return tview.NewFlex().
		AddItem(t.Tree, 35, 1, true).
		AddItem(mainContent, 0, 3, false)
}

func (t *TUIApp) Run(collection models.Collection, cachedRequests []models.RequestInfo) error {
	t.Collection = collection
	t.Cached = cachedRequests

	// Sidebar: Tree View
	t.Tree = tview.NewTreeView()
	t.refreshTree()

	// Initialize Panels
	t.LeftPanel = t.newRequestPanel("Left")
	t.RightPanel = t.newRequestPanel("Right")
	t.ActivePanel = t.LeftPanel

	// Status/Info Area
	t.Status = tview.NewTextView().SetDynamicColors(true)
	t.Status.SetBorder(false)
	t.Status.SetText(" [yellow]Tab[white]: Cycle Focus | [yellow]Ctrl+P[white]: Command Palette | [yellow]Ctrl+R[white]: Send | [yellow]Ctrl+C[white]: Exit")

	flex := t.getMainLayout()

	// Selection Handler
	t.Tree.SetSelectedFunc(func(node *tview.TreeNode) {
		ref := node.GetReference()
		if req, ok := ref.(*models.RequestInfo); ok {
			t.showRequest(req)
		} else {
			node.SetExpanded(!node.IsExpanded())
		}
	})

	// Global Keybindings
	t.App.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlP {
			t.showCommandPalette()
			return nil
		}

		switch event.Key() {
		case tcell.KeyCtrlR:
			if t.ActivePanel.CurrentReq != nil {
				t.sendRequest()
			}
			return nil
		case tcell.KeyCtrlH:
			if t.ActivePanel.CurrentReq != nil {
				t.showHammer()
			}
			return nil
		case tcell.KeyCtrlS:
			if t.ActivePanel.CurrentReq != nil {
				t.showSQLEditor()
			}
			return nil
		case tcell.KeyCtrlN:
			t.showNewRequestForm()
			return nil
		case tcell.KeyCtrlD:
			if t.ActivePanel.CurrentReq != nil {
				t.duplicateRequest()
			}
			return nil
		case tcell.KeyCtrlM:
			if t.ActivePanel.CurrentReq != nil {
				t.showMoveRequestForm()
			}
			return nil
		case tcell.KeyUp:
			if event.Modifiers()&tcell.ModAlt != 0 && t.Tree.HasFocus() {
				t.moveRequest(true)
				return nil
			}
		case tcell.KeyDown:
			if event.Modifiers()&tcell.ModAlt != 0 && t.Tree.HasFocus() {
				t.moveRequest(false)
				return nil
			}
		case tcell.KeyTab:
			if t.Tree.HasFocus() {
				t.App.SetFocus(t.LeftPanel.Main)
				t.ActivePanel = t.LeftPanel
			} else if t.ActivePanel == t.LeftPanel {
				if t.LeftPanel.Main.HasFocus() {
					t.App.SetFocus(t.LeftPanel.Response)
				} else {
					t.App.SetFocus(t.RightPanel.Main)
					t.ActivePanel = t.RightPanel
				}
			} else if t.ActivePanel == t.RightPanel {
				if t.RightPanel.Main.HasFocus() {
					t.App.SetFocus(t.RightPanel.Response)
				} else {
					t.App.SetFocus(t.Tree)
				}
			}
			t.updatePanelBorders()
			return nil
		case tcell.KeyCtrlB:
			t.App.SetFocus(t.ActivePanel.Response)
			return nil
		}
		return event
	})

	t.updatePanelBorders()
	return t.App.SetRoot(flex, true).Run()
}

func (t *TUIApp) updatePanelBorders() {
	t.LeftPanel.Main.SetBorderColor(tcell.ColorWhite)
	t.LeftPanel.Response.SetBorderColor(tcell.ColorWhite)
	t.RightPanel.Main.SetBorderColor(tcell.ColorWhite)
	t.RightPanel.Response.SetBorderColor(tcell.ColorWhite)

	if t.ActivePanel == t.LeftPanel {
		t.LeftPanel.Main.SetBorderColor(tcell.ColorYellow)
		t.LeftPanel.Response.SetBorderColor(tcell.ColorYellow)
	} else {
		t.RightPanel.Main.SetBorderColor(tcell.ColorYellow)
		t.RightPanel.Response.SetBorderColor(tcell.ColorYellow)
	}
}

func (t *TUIApp) buildTree(parent *tview.TreeNode, items []models.Item, prefix string, cached []models.RequestInfo) {
	for _, item := range items {
		name := item.Name
		fullPath := name
		if prefix != "" {
			fullPath = prefix + " > " + name
		}

		node := tview.NewTreeNode(name)
		if item.Request != nil {
			node.SetColor(tcell.ColorGreen)
			
			// Inherit logic similar to CLI flattenItems
			reqInfo := models.RequestInfo{
				Path:    fullPath,
				Request: item.Request,
				Events:  item.Event,
				Order:   item.Order,
			}
			
			// Find cached version
			for _, c := range cached {
				if c.Path == fullPath {
					reqInfo = c
					break
				}
			}
			node.SetReference(&reqInfo)
		} else {
			node.SetColor(tcell.ColorBlue)
			t.buildTree(node, item.Item, fullPath, cached)
		}
		parent.AddChild(node)
	}
}

func (t *TUIApp) showRequest(req *models.RequestInfo) {
	t.ActivePanel.CurrentReq = req
	t.ActivePanel.Main.Clear()
	t.ActivePanel.Main.SetTitle(fmt.Sprintf(" %s (%s) ", req.Path, t.ActivePanel.Title))

	view := tview.NewTextView().SetDynamicColors(true)
	
	fmt.Fprintf(view, "[yellow]METHOD:[white] %s\n", req.Request.Method)
	fmt.Fprintf(view, "[yellow]URL:   [white] %s\n\n", req.Request.URL.Raw)

	fmt.Fprintf(view, "[yellow]HEADERS:\n")
	if len(req.Request.Header) == 0 {
		fmt.Fprintf(view, "  [gray]None\n")
	}
	for _, h := range req.Request.Header {
		fmt.Fprintf(view, "  [blue]%s:[white] %s\n", h.Key, h.Value)
	}

	fmt.Fprintf(view, "\n[yellow]BODY:\n")
	if req.Request.Body == nil || (req.Request.Body.Raw == "" && len(req.Request.Body.UrlEncoded) == 0) {
		fmt.Fprintf(view, "  [gray]Empty\n")
	} else if req.Request.Body.Mode == "raw" {
		fmt.Fprintf(view, "[white]%s\n", req.Request.Body.Raw)
	} else {
		for _, f := range req.Request.Body.UrlEncoded {
			fmt.Fprintf(view, "  [blue]%s:[white] %s\n", f.Key, f.Value)
		}
	}

	t.ActivePanel.Main.AddItem(view, 0, 1, true)
	t.ActivePanel.Response.SetText(" [gray]Press Ctrl+R to Send Request")
}

func (t *TUIApp) sendRequest() {
	panel := t.ActivePanel
	req := panel.CurrentReq
	panel.Response.SetText(" [yellow]Sending request...")
	
	go func() {
		// 1. Run Pre-request scripts
		t.Processor.RunScripts(req.Events, "prerequest", nil, nil, req.Request.Header)
		t.Processor.RunScripts(req.Events, "test", nil, nil, req.Request.Header)

		// 2. Execute
		body, headers, statusCode, statusText := t.Client.ExecuteRequest(req.Request)
		
		t.App.QueueUpdateDraw(func() {
			if statusCode == 0 {
				panel.Response.SetText(" [red]Failed to send request or no response received.")
				return
			}

			// Prettify JSON if possible
			var prettyJSON interface{}
			display := body
			if err := json.Unmarshal([]byte(body), &prettyJSON); err == nil {
				formatted, _ := json.MarshalIndent(prettyJSON, "", "  ")
				display = string(formatted)
			}

			// Add status at the top
			statusColor := "green"
			if statusCode >= 400 {
				statusColor = "red"
			} else if statusCode >= 300 {
				statusColor = "yellow"
			}
			
			respContent := fmt.Sprintf(" [yellow]Status: [%s]%d %s[white]\n\n%s", statusColor, statusCode, statusText, display)
			
			// 3. Run post-request scripts
			t.Processor.RunScripts(req.Events, "test", []byte(body), headers, req.Request.Header)

			// 4. SQL Sidekick
			if req.SQLQuery != "" && req.DBPath != "" {
				cols, rows, err := t.Client.ExecuteSQL(req.DBPath, req.SQLQuery)
				if err != nil {
					respContent += fmt.Sprintf("\n\n [red]SQL Error: %v", err)
				} else {
					respContent += "\n\n [yellow]SQL Results:\n"
					// Simple table view in text
					for _, col := range cols {
						respContent += fmt.Sprintf(" [blue]%-15s", col)
					}
					respContent += "[white]\n" + strings.Repeat("-", len(cols)*16) + "\n"
					for _, row := range rows {
						for _, val := range row {
							respContent += fmt.Sprintf(" %-15s", val)
						}
						respContent += "\n"
					}
				}
			}
			
			panel.Response.SetText(respContent)
		})
	}()
}

func (t *TUIApp) refreshTree() {
	root := tview.NewTreeNode(t.Collection.Info.Name).SetColor(tcell.ColorYellow)
	t.buildTree(root, t.Collection.Item, "", t.Cached)
	
	visited := make(map[string]bool)
	t.markVisited(root, visited)

	customRoot := tview.NewTreeNode("Custom Requests").SetColor(tcell.ColorBlue)
	hasCustom := false
	for _, req := range t.Cached {
		if !visited[req.Path] {
			// Create a local copy to avoid closure issues
			r := req
			node := tview.NewTreeNode(req.Path).SetColor(tcell.ColorGreen).SetReference(&r)
			customRoot.AddChild(node)
			hasCustom = true
		}
	}
	if hasCustom {
		root.AddChild(customRoot)
	}

	t.Tree.SetRoot(root)
	if t.Tree.GetCurrentNode() == nil {
		t.Tree.SetCurrentNode(root)
	}
}

func (t *TUIApp) markVisited(node *tview.TreeNode, visited map[string]bool) {
	ref := node.GetReference()
	if req, ok := ref.(*models.RequestInfo); ok {
		visited[req.Path] = true
	}
	for _, child := range node.GetChildren() {
		t.markVisited(child, visited)
	}
}

func (t *TUIApp) showCommandPalette() {
	commands := []struct {
		Name   string
		Action func()
	}{
		{"> New Request", t.showNewRequestForm},
		{"> Import cURL", t.showImportCurlForm},
		{"> Run Hammer", t.showHammer},
		{"> Switch Environment", t.showEnvironmentPicker},
		{"> Clear History", t.clearHistory},
	}

	pages := tview.NewPages()
	pages.AddPage("main", t.getMainLayout(), true, true)

	list := tview.NewList().ShowSecondaryText(false)
	for _, cmd := range commands {
		c := cmd // closure
		list.AddItem(c.Name, "", 0, func() {
			pages.RemovePage("palette")
			c.Action()
		})
	}

	list.AddItem("Cancel", "", 'q', func() {
		pages.RemovePage("palette")
		t.App.SetFocus(t.Tree)
	})

	list.SetBorder(true).SetTitle(" Command Palette (Ctrl+P) ")

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(list, 15, 1, true).
			AddItem(nil, 0, 1, false), 50, 1, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("palette", modal, true, true)
	t.App.SetRoot(pages, true).SetFocus(list)
}

func (t *TUIApp) showImportCurlForm() {
	pages := tview.NewPages()
	pages.AddPage("main", t.getMainLayout(), true, true)

	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" Import cURL ").SetTitleAlign(tview.AlignLeft)

	var curlStr string
	form.AddTextArea("cURL Command", "", 60, 10, 0, func(text string) { curlStr = text })

	form.AddButton("Import", func() {
		if curlStr == "" {
			return
		}
		req := processor.ParseCurl(curlStr)
		newReq := models.RequestInfo{
			Path:    "Imported > " + time.Now().Format("15:04:05"),
			Request: req,
			Order:   len(t.Cached),
		}
		t.Cached = append(t.Cached, newReq)
		t.Storage.SaveSingleRequest(newReq)
		t.Collection.Item = models.ReconstructItems(t.Cached)
		t.refreshTree()
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})
	form.AddButton("Cancel", func() {
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 18, 1, true).
			AddItem(nil, 0, 1, false), 70, 1, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("form", modal, true, true)
	t.App.SetRoot(pages, true).SetFocus(form)
}

func (t *TUIApp) showEnvironmentPicker() {
	envs := t.Storage.LoadEnvironments()
	if len(envs) == 0 {
		t.ActivePanel.Response.SetText(" [red]No environments found.")
		return
	}

	pages := tview.NewPages()
	pages.AddPage("main", t.getMainLayout(), true, true)

	list := tview.NewList().ShowSecondaryText(false)
	for _, env := range envs {
		e := env
		list.AddItem(e.Name, "", 0, func() {
			t.Storage.SaveActiveEnv(e.ID)
			t.ActivePanel.Response.SetText(fmt.Sprintf(" [green]Switched to environment: %s", e.Name))
			pages.RemovePage("picker")
			t.App.SetFocus(t.Tree)
		})
	}
	list.AddItem("Cancel", "", 'q', func() {
		pages.RemovePage("picker")
		t.App.SetFocus(t.Tree)
	})

	list.SetBorder(true).SetTitle(" Switch Environment ")

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(list, 10, 1, true).
			AddItem(nil, 0, 1, false), 40, 1, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("picker", modal, true, true)
	t.App.SetRoot(pages, true).SetFocus(list)
}

func (t *TUIApp) clearHistory() {
	t.Storage.SaveHistory([]models.HistoryRecord{})
	t.ActivePanel.Response.SetText(" [green]History cleared.")
}

func (t *TUIApp) showNewRequestForm() {
	pages := tview.NewPages()
	pages.AddPage("main", t.getMainLayout(), true, true)

	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" New Request ").SetTitleAlign(tview.AlignLeft)

	var name, method, url string
	method = "GET"
	form.AddInputField("Path (e.g. Folder > Name)", "", 40, nil, func(text string) { name = text })
	form.AddDropDown("Method", []string{"GET", "POST", "PUT", "DELETE", "PATCH"}, 0, func(option string, optionIndex int) { method = option })
	form.AddInputField("URL", "https://", 40, nil, func(text string) { url = text })

	form.AddButton("Save", func() {
		if name == "" || url == "" {
			return
		}
		newReq := models.RequestInfo{
			Path: name,
			Request: &models.Request{
				Method: method,
				URL:    models.URL{Raw: url},
			},
			Order: len(t.Cached),
		}
		t.Cached = append(t.Cached, newReq)
		t.Storage.SaveSingleRequest(newReq)
		t.Collection.Item = models.ReconstructItems(t.Cached)
		t.refreshTree()
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})
	form.AddButton("Cancel", func() {
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 15, 1, true).
			AddItem(nil, 0, 1, false), 50, 1, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("form", modal, true, true)
	t.App.SetRoot(pages, true).SetFocus(form)
}

func (t *TUIApp) duplicateRequest() {
	req := t.ActivePanel.CurrentReq
	if req == nil {
		return
	}

	pages := tview.NewPages()
	pages.AddPage("main", t.getMainLayout(), true, true)

	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" Duplicate Request ").SetTitleAlign(tview.AlignLeft)

	var newPath string
	newPath = req.Path + " Copy"
	form.AddInputField("New Path", newPath, 60, nil, func(text string) { newPath = text })

	form.AddButton("Duplicate", func() {
		if newPath == "" {
			return
		}
		
		var eventsCopy []models.Event
		if req.Events != nil {
			eventsCopy = make([]models.Event, len(req.Events))
			for i, e := range req.Events {
				eventsCopy[i] = e
				if e.Script.Exec != nil {
					eventsCopy[i].Script.Exec = make([]string, len(e.Script.Exec))
					copy(eventsCopy[i].Script.Exec, e.Script.Exec)
				}
			}
		}

		newReq := models.RequestInfo{
			Path:    newPath,
			Request: req.Request.DeepCopy(),
			Events:  eventsCopy,
			Order:   req.Order + 1,
		}
		
		t.Cached = append(t.Cached, newReq)
		t.Storage.SaveSingleRequest(newReq)
		t.Collection.Item = models.ReconstructItems(t.Cached)
		t.refreshTree()
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})
	form.AddButton("Cancel", func() {
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 9, 1, true).
			AddItem(nil, 0, 1, false), 70, 1, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("form", modal, true, true)
	t.App.SetRoot(pages, true).SetFocus(form)
}

func (t *TUIApp) moveRequest(up bool) {
	node := t.Tree.GetCurrentNode()
	if node == nil {
		return
	}

	ref := node.GetReference()
	req, ok := ref.(*models.RequestInfo)
	if !ok {
		return
	}

	root := t.Tree.GetRoot()
	parent := t.findParent(root, node)
	if parent == nil {
		return
	}

	children := parent.GetChildren()
	var idx int
	for i, child := range children {
		if child == node {
			idx = i
			break
		}
	}

	targetIdx := idx + 1
	if up {
		targetIdx = idx - 1
	}

	if targetIdx < 0 || targetIdx >= len(children) {
		return
	}

	targetNode := children[targetIdx]
	targetRef := targetNode.GetReference()
	targetReq, ok := targetRef.(*models.RequestInfo)
	if !ok {
		// Moving past a folder - not supported for now to keep it simple
		return
	}

	// Swap orders
	req.Order, targetReq.Order = targetReq.Order, req.Order
	if req.Order == targetReq.Order {
		if up {
			req.Order--
		} else {
			req.Order++
		}
	}

	// Save both
	t.Storage.SaveSingleRequest(*req)
	t.Storage.SaveSingleRequest(*targetReq)

	// Rebuild and refresh
	t.Collection.Item = models.ReconstructItems(t.Cached)
	t.refreshTree()
	
	// Reselect the node
	t.selectByPath(t.Tree.GetRoot(), req.Path)
}

func (t *TUIApp) showMoveRequestForm() {
	req := t.ActivePanel.CurrentReq
	if req == nil {
		return
	}

	pages := tview.NewPages()
	pages.AddPage("main", t.getMainLayout(), true, true)

	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" Move Request ").SetTitleAlign(tview.AlignLeft)

	var newPath string
	newPath = req.Path
	form.AddInputField("New Path (e.g. Folder > Name)", newPath, 60, nil, func(text string) { newPath = text })

	form.AddButton("Move", func() {
		if newPath == "" || newPath == req.Path {
			return
		}
		
		oldPath := req.Path
		req.Path = newPath
		
		for i := range t.Cached {
			if t.Cached[i].Path == oldPath {
				t.Cached[i].Path = newPath
				break
			}
		}

		t.Storage.SaveSingleRequest(*req)
		
		t.Collection.Item = models.ReconstructItems(t.Cached)
		t.refreshTree()
		t.selectByPath(t.Tree.GetRoot(), newPath)
		
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})
	form.AddButton("Cancel", func() {
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 9, 1, true).
			AddItem(nil, 0, 1, false), 70, 1, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("form", modal, true, true)
	t.App.SetRoot(pages, true).SetFocus(form)
}

func (t *TUIApp) findParent(root, target *tview.TreeNode) *tview.TreeNode {
	for _, child := range root.GetChildren() {
		if child == target {
			return root
		}
		if p := t.findParent(child, target); p != nil {
			return p
		}
	}
	return nil
}

func (t *TUIApp) selectByPath(root *tview.TreeNode, path string) bool {
	ref := root.GetReference()
	if req, ok := ref.(*models.RequestInfo); ok && req.Path == path {
		t.Tree.SetCurrentNode(root)
		return true
	}
	
	for _, child := range root.GetChildren() {
		if t.selectByPath(child, path) {
			return true
		}
	}
	return false
}

func (t *TUIApp) showHammer() {
	req := t.ActivePanel.CurrentReq
	if req == nil {
		return
	}

	pages := tview.NewPages()
	pages.AddPage("main", t.getMainLayout(), true, true)

	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" Hammer (Load Testing) ").SetTitleAlign(tview.AlignLeft)

	var workers int = 10
	var duration int = 5 // seconds
	form.AddInputField("Workers", "10", 10, tview.InputFieldInteger, func(text string) {
		fmt.Sscanf(text, "%d", &workers)
	})
	form.AddInputField("Duration (sec)", "5", 10, tview.InputFieldInteger, func(text string) {
		fmt.Sscanf(text, "%d", &duration)
	})

	form.AddButton("Start Hammering", func() {
		t.ActivePanel.Response.SetText(" [yellow]Hammering in progress...")
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)

		go func() {
			results := t.Client.Hammer(req.Request, workers, time.Duration(duration)*time.Second)
			
			t.App.QueueUpdateDraw(func() {
				resText := fmt.Sprintf(" [yellow]Hammer Results for %s[white]\n\n", req.Path)
				resText += fmt.Sprintf(" [blue]Total Requests:[white] %d\n", results.TotalRequests)
				resText += fmt.Sprintf(" [green]Success Rate:  [white] %.2f%%\n", float64(results.SuccessCount)/float64(results.TotalRequests)*100)
				resText += fmt.Sprintf(" [yellow]RPS:           [white] %.2f\n", results.RPS)
				resText += fmt.Sprintf(" [blue]Avg Latency:   [white] %v\n\n", results.AverageLatency)
				
				resText += " [yellow]Status Codes:\n"
				for code, count := range results.StatusCodes {
					color := "green"
					if code >= 400 { color = "red" }
					resText += fmt.Sprintf("   [%s]%d:[white] %d\n", color, code, count)
				}
				
				t.ActivePanel.Response.SetText(resText)
			})
		}()
	})
	
	form.AddButton("Cancel", func() {
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 12, 1, true).
			AddItem(nil, 0, 1, false), 50, 1, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("form", modal, true, true)
	t.App.SetRoot(pages, true).SetFocus(form)
}

func (t *TUIApp) showSQLEditor() {
	req := t.ActivePanel.CurrentReq
	if req == nil {
		return
	}

	pages := tview.NewPages()
	pages.AddPage("main", t.getMainLayout(), true, true)

	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" SQL Sidekick ").SetTitleAlign(tview.AlignLeft)

	form.AddInputField("DB Path (SQLite)", req.DBPath, 60, nil, func(text string) {
		req.DBPath = text
	})
	form.AddInputField("SQL Query", req.SQLQuery, 60, nil, func(text string) {
		req.SQLQuery = text
	})

	form.AddButton("Save", func() {
		t.Storage.SaveSingleRequest(*req)
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
		t.showRequest(req) // Refresh view
	})
	
	form.AddButton("Cancel", func() {
		pages.RemovePage("form")
		t.App.SetFocus(t.Tree)
	})

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 12, 1, true).
			AddItem(nil, 0, 1, false), 70, 1, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage("form", modal, true, true)
	t.App.SetRoot(pages, true).SetFocus(form)
}
