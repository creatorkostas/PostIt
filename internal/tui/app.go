package tui

import (
	"encoding/json"
	"fmt"
	"postit/internal/api"
	"postit/internal/models"
	"postit/internal/processor"
	"postit/internal/storage"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type TUIApp struct {
	App       *tview.Application
	Storage   *storage.Manager
	Processor *processor.ScriptProcessor
	Client    *api.Client

	// Layout Components
	Tree     *tview.TreeView
	Response *tview.TextView
	Main     *tview.Flex
	Status   *tview.TextView

	CurrentReq *models.RequestInfo
}

func NewTUIApp(store *storage.Manager, proc *processor.ScriptProcessor, client *api.Client) *TUIApp {
	return &TUIApp{
		App:       tview.NewApplication(),
		Storage:   store,
		Processor: proc,
		Client:    client,
	}
}

func (t *TUIApp) Run(collection models.Collection, cachedRequests []models.RequestInfo) error {
	// Sidebar: Tree View
	root := tview.NewTreeNode(collection.Info.Name).SetColor(tcell.ColorYellow)
	t.Tree = tview.NewTreeView().SetRoot(root).SetCurrentNode(root)

	// Build Tree with proper paths
	t.buildTree(root, collection.Item, "", cachedRequests)

	// Response Area
	t.Response = tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true)
	t.Response.SetBorder(true).SetTitle("Response Body (Ctrl+B to focus)")

	// Status/Info Area
	t.Status = tview.NewTextView().SetDynamicColors(true)
	t.Status.SetBorder(false)
	t.Status.SetText(" [yellow]Tab[white]: Switch Panels | [yellow]Ctrl+R[white]: Send | [yellow]Ctrl+C[white]: Exit")

	// Main Request Area
	t.Main = tview.NewFlex().SetDirection(tview.FlexRow)
	t.Main.SetBorder(true).SetTitle("Request Details")

	// Layout
	rightSide := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.Main, 0, 1, false).
		AddItem(t.Response, 0, 1, false).
		AddItem(t.Status, 1, 0, false)

	flex := tview.NewFlex().
		AddItem(t.Tree, 35, 1, true).
		AddItem(rightSide, 0, 3, false)

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
		switch event.Key() {
		case tcell.KeyCtrlR:
			if t.CurrentReq != nil {
				t.sendRequest()
			}
			return nil
		case tcell.KeyTab:
			if t.Tree.HasFocus() {
				t.App.SetFocus(t.Main)
			} else if t.Main.HasFocus() {
				t.App.SetFocus(t.Response)
			} else {
				t.App.SetFocus(t.Tree)
			}
			return nil
		case tcell.KeyCtrlB:
			t.App.SetFocus(t.Response)
			return nil
		}
		return event
	})

	return t.App.SetRoot(flex, true).Run()
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
				Events:  item.Event, // Note: For TUI, we might need parent inheritance if not already flattened
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
	t.CurrentReq = req
	t.Main.Clear()
	t.Main.SetTitle(fmt.Sprintf(" %s ", req.Path))

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
	if req.Request.Body == nil || (req.Request.Body.Raw == "" && len(req.Request.Body.Urlencoded) == 0) {
		fmt.Fprintf(view, "  [gray]Empty\n")
	} else if req.Request.Body.Mode == "raw" {
		fmt.Fprintf(view, "[white]%s\n", req.Request.Body.Raw)
	} else {
		for _, f := range req.Request.Body.Urlencoded {
			fmt.Fprintf(view, "  [blue]%s:[white] %s\n", f.Key, f.Value)
		}
	}

	t.Main.AddItem(view, 0, 1, true)
	t.Response.SetText(" [gray]Press Ctrl+R to Send Request")
}

func (t *TUIApp) sendRequest() {
	t.Response.SetText(" [yellow]Sending request...")
	go func() {
		// 1. Run Pre-request scripts
		t.Processor.RunScripts(t.CurrentReq.Events, "prerequest", nil, nil, t.CurrentReq.Request.Header)
		t.Processor.RunScripts(t.CurrentReq.Events, "test", nil, nil, t.CurrentReq.Request.Header)

		// 2. Execute
		body, headers := t.Client.ExecuteRequest(t.CurrentReq.Request)
		
		t.App.QueueUpdateDraw(func() {
			if body == "" {
				t.Response.SetText(" [red]No response body or error occurred.")
				return
			}

			// Prettify JSON if possible
			var prettyJSON interface{}
			display := body
			if err := json.Unmarshal([]byte(body), &prettyJSON); err == nil {
				formatted, _ := json.MarshalIndent(prettyJSON, "", "  ")
				display = string(formatted)
			}

			t.Response.SetText(display)
			
			// 3. Run post-request scripts
			t.Processor.RunScripts(t.CurrentReq.Events, "test", []byte(body), headers, t.CurrentReq.Request.Header)
		})
	}()
}
