package main

import (
	"context"
	"embed"
	"net/http"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"iptv-client/internal/logging"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	// Logging starts first, so a failure to open the database is logged.
	// If the folder is unusable it falls back to stderr and the app runs.
	dir, _ := logging.Dir()
	logs, _ := logging.Setup(dir, logging.Meta{Version: version, Run: logging.NewRun()})
	logging.RecoverCrashes(context.Background(), logs.Logger, dir)
	app := NewApp(logs.Logger, logs.Level, dir)

	err := wails.Run(&options.App{
		Title:     "IPTV Client",
		Width:     1200,
		Height:    780,
		MinWidth:  760,
		MinHeight: 480,
		AssetServer: &assetserver.Options{
			Assets: assets,
			// Called for paths not in assets.
			Handler: http.HandlerFunc(app.serveLogo),
		},
		BackgroundColour:   &options.RGBA{R: 24, G: 26, B: 31, A: 255},
		Menu:               app.appMenu(),
		OnStartup:          app.startup,
		OnDomReady:         finishAppMenu,
		OnShutdown:         app.shutdown,
		Logger:             logging.Wails(logs.Logger),
		LogLevel:           logging.WailsLevel,
		LogLevelProduction: logging.WailsLevel,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		logs.Logger.Error("the app failed to start", "err", err)
	}
	logs.Close()
	if err != nil {
		os.Exit(1)
	}
}
