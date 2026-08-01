package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/wojustme/otto/protocol"
)

// 将生产前端嵌入二进制，使桌面应用打包后能够独立运行，
// 不依赖本地 HTTP 服务。
//
//go:embed all:frontend/dist
var assets embed.FS

// Wails 会在 macOS 运行时应用 Options.Icon。这里嵌入与打包相同的图标源，
// 避免 Dock 图标缓存回退到 Wails 默认图标。
//
//go:embed build/appicon.png
var appIcon []byte

// runtimeEventName 是 Go Runtime 事件流在 Wails 前后端之间使用的统一事件名。
const runtimeEventName = "otto:runtime-event"

// init 在应用创建前注册事件载荷类型，使 Wails 能生成并校验绑定。
func init() {
	// 类型化注册保证 Go 事件载荷与生成的 TypeScript 绑定在 Wails 边界处保持一致。
	application.RegisterEvent[protocol.Event](runtimeEventName)
}

// main 组装 Wails 应用、Runtime 服务和主窗口，并启动桌面事件循环。
func main() {
	runtimeService := NewRuntimeService()
	// RuntimeService 负责 sidecar 生命周期，也是 React 层唯一需要访问的后端 API；
	// Agent 引擎本身仍运行在 GUI 进程之外。
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

	// 事件循环异常退出时记录错误并终止进程，避免静默失败。
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
