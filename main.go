package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "EasyShare",
		Width:     1180,
		Height:    780,
		MinWidth:  860,
		MinHeight: 640,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 233, G: 237, B: 243, A: 1},
		OnStartup: func(ctx context.Context) {
			app.Startup(ctx)
			startTray(app)
		},
		OnShutdown: app.Shutdown,
		OnBeforeClose: func(ctx context.Context) bool {
			// If quitting was explicitly requested (from tray or ShutdownAll),
			// allow the window to close. Otherwise hide to tray.
			if app.isQuitting() {
				return false
			}
			runtime.WindowHide(ctx)
			return true
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
