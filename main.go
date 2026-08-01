package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/wojustme/otto/protocol"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

const runtimeEventName = "otto:runtime-event"

func init() {
	application.RegisterEvent[protocol.Event](runtimeEventName)
}

func main() {
	runtimeService := NewRuntimeService()
	app := application.New(application.Options{
		Name:        "Otto",
		Description: "A desktop agent execution node",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(runtimeService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Otto",
		Width:            1240,
		Height:           780,
		MinWidth:         960,
		MinHeight:        620,
		InitialPosition:  application.WindowCentered,
		BackgroundColour: application.NewRGB(243, 240, 231),
		URL:              "/",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 46,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
