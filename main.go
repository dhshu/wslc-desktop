// Command wslc-desktop is the Wails desktop shell for the wslc CLI.
//
// The shell owns no business logic: main.go only mounts the frontend assets,
// wires the process-wide context and hands the bound App to Wails. Everything
// the UI can do lives in internal/service and is forwarded by app.go.
//
// The frontend is pure static HTML/CSS/JS and is compiled into the binary with
// go:embed, so the build has no npm/node step at all: `wails build -s` (skip
// frontend build) produces a self-contained executable.
package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// frontendAssets carries the whole frontend tree (index.html, styles.css,
// app.js, wailsjs/…). The embed root is the repository, so the sub-FS handed to
// the asset server is "frontend".
//
//go:embed all:frontend
var frontendAssets embed.FS

func main() {
	assets, err := fs.Sub(frontendAssets, "frontend")
	if err != nil {
		log.Fatalf("wslc Desktop: 无法挂载内嵌前端资源: %v", err)
	}

	app := NewApp(newRunner())

	err = wails.Run(&options.App{
		Title:            "wslc Desktop",
		Width:            850,    // 默认可见区域约为主屏 2/3，减少初次打开的视觉压迫感
		Height:           540,
		MinWidth:         640,    // 保持与默认尺寸同比例，仍允许拉大到全屏
		MinHeight:        430,
		BackgroundColour: options.NewRGBA(15, 17, 21, 255),
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnDomReady: app.domReady,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatalf("wslc Desktop: wails.Run 失败: %v", err)
	}
}

// newRunner resolves wslc.exe and returns a runner bound to it.
//
// A resolution failure is not fatal: the application must still open so that
// the environment tab can explain what is missing and how to fix it. In that
// case the runner is built without an executable, and both wslc.ExecRunner.Available
// and service.EnvCheck report ErrExecutableNotFound (with the attempted lookup
// paths logged below) instead of the window failing to appear.
func newRunner() wslc.Runner {
	exe, err := wslc.NewResolver().Resolve()
	if err != nil {
		log.Printf("wslc Desktop: 未找到 wslc.exe（界面仍会启动，由“环境自检”报告）: %v", err)
	}
	return wslc.NewExecRunner(exe)
}
