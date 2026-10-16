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
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"github.com/wslc-desktop/wslc-desktop/internal/service"
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
		log.Fatalf("Wslc Desktop: 无法挂载内嵌前端资源: %v", err)
	}

	app := NewApp(newRunner())

	err = wails.Run(&options.App{
		Title:            "Wslc Desktop",
		Width:            940, // 默认窗口较原 850×540 整体放大 10%
		Height:           600,
		MinWidth:         640, // 保持与默认尺寸同比例，仍允许拉大到全屏
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
		Windows: windowsOptions(),
	})
	if err != nil {
		log.Fatalf("Wslc Desktop: wails.Run 失败: %v", err)
	}
}

// windowsOptions pins WebView2's user data folder — localStorage (the theme
// preference wslc.theme lives there), cookies, IndexedDB, cache — next to the
// executable in portable mode, mirroring where the settings JSON goes (see
// internal/service/portable.go).
//
// In a non-portable install the field stays empty on purpose: Wails forwards
// it verbatim to CreateCoreWebView2EnvironmentWithOptions, and an unwritable
// path makes environment creation fail fatally (message box + non-zero exit),
// whereas the empty default (%APPDATA%\<exe name>) always works.
func windowsOptions() *windows.Options {
	opts := &windows.Options{}
	if dir, ok := service.PortableDataDir(); ok {
		opts.WebviewUserDataPath = filepath.Join(dir, service.WebView2UserDataDirName)
	}
	return opts
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
		log.Printf("Wslc Desktop: 未找到 wslc.exe（界面仍会启动，由“环境自检”报告）: %v", err)
	}
	return wslc.NewExecRunner(exe)
}
