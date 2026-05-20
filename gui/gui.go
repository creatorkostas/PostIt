package main

import (
	"io/fs"
	"postit/internal/assets"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// runGUI starts the native Wails desktop window and blocks until it closes.
func runGUI(deps *AppDeps) {
	// Create an instance of the app structure
	app := NewApp(deps.Store, deps.Processor, deps.Client, deps.Collection, deps.FlatList, true)

	// Create a native menu
	appMenu := menu.NewMenu()
	fileMenu := appMenu.AddSubmenu("File")
	fileMenu.AddText("Import Postman", keys.CmdOrCtrl("i"), func(_ *menu.CallbackData) {
		runtime.EventsEmit(app.ctx, "show-import", "postman")
	})
	fileMenu.AddText("Import OpenAPI", keys.CmdOrCtrl("o"), func(_ *menu.CallbackData) {
		runtime.EventsEmit(app.ctx, "show-import", "openapi")
	})
	fileMenu.AddSeparator()
	fileMenu.AddText("Quit", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		runtime.Quit(app.ctx)
	})

	editMenu := appMenu.AddSubmenu("Edit")
	editMenu.AddText("Settings", keys.CmdOrCtrl(","), func(_ *menu.CallbackData) {
		runtime.EventsEmit(app.ctx, "show-settings", "")
	})

	// Prepare shared frontend assets (strip the "frontend/" prefix)
	frontendFS, err := fs.Sub(assets.FS, "frontend")
	if err != nil {
		println("Error setting up frontend assets:", err.Error())
		return
	}

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "PostIt GUI",
		Width:  1280,
		Height: 800,
		Menu:   appMenu,
		AssetServer: &assetserver.Options{
			Assets: frontendFS,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
