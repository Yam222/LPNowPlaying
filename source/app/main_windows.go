//go:build windows

package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

//go:embed web/control.html
var controlHTML string

//go:embed extension
var extensionFS embed.FS

const appTitle = "LP Now Playing 2"

var (
	app     *App
	view    webview2.WebView
	overlay *Overlay
)

func main() {
	runtime.LockOSThread()

	// 중복 실행 방지: 이미 켜져 있으면 기존 창을 앞으로
	mname, _ := windows.UTF16PtrFromString("Local\\LPNowPlaying2.SingleInstance")
	if _, err := windows.CreateMutex(nil, false, mname); err == windows.ERROR_ALREADY_EXISTS {
		focusExisting()
		return
	}

	app = NewApp(LoadSettings())

	scale := float64(systemDPI()) / 96
	winH := 820 * scale
	if sh := float64(screenHeight()); sh > 0 && winH > sh*0.92 {
		winH = sh * 0.92
	}
	local, _ := os.UserCacheDir() // %LOCALAPPDATA%
	view = webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(local, "LPNowPlaying2", "WebView2"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  appTitle,
			Width:  uint(500 * scale),
			Height: uint(winH),
			IconId: 1,
			Center: true,
		},
	})
	if view == nil {
		messageBox("Microsoft Edge WebView2 런타임이 필요해요.\n\n확인을 누르면 설치 페이지를 열어요. 설치 후 다시 실행해 주세요.")
		openURL("https://developer.microsoft.com/microsoft-edge/webview2/")
		return
	}
	defer view.Destroy()
	view.SetSize(int(420*scale), int(480*scale), webview2.HintMin)

	// 화면 오버레이 창 (같은 UI 스레드에서 메시지 처리)
	overlay = NewOverlay(app)

	bind()
	view.SetHtml(controlHTML)

	if app.Settings().AutoStart {
		_ = app.Start()
	}
	view.Run()
	app.Stop()
}

func bind() {
	must := func(err error) {
		if err != nil {
			messageBox(err.Error())
		}
	}
	must(view.Bind("lpGetState", func() map[string]any {
		s := app.Settings()
		np := app.NowFor("preview")
		ov := overlay.Status()
		ov.Monitors = listMonitors()
		return map[string]any{
			"running":    app.Running(),
			"connected":  np.Connected,
			"lastError":  app.LastError(),
			"settings":   s,
			"obsUrl":     s.OBSURL(),
			"now":        np,
			"overlay":    ov,
			"obsClient":  app.Client("obs"),
			"lastCmd":    app.LastCommand(),
			"extVersion": app.store.ExtVersion(),
			"extLatest":  ExtVersion,
			"appVersion": AppVersion,
		}
	}))
	must(view.Bind("lpStart", func() string {
		if err := app.Start(); err != nil {
			return err.Error()
		}
		return ""
	}))
	must(view.Bind("lpStop", func() { app.Stop() }))
	must(view.Bind("lpSaveSettings", func(s Settings) string {
		old := app.Settings()
		if err := app.SetSettings(s); err != nil {
			return "설정을 저장하지 못했어요: " + err.Error()
		}
		if old.Port != app.Settings().Port && app.Running() {
			app.Stop()
			if err := app.Start(); err != nil {
				return err.Error()
			}
		}
		return ""
	}))
	must(view.Bind("lpToggleOverlay", func() { overlay.Toggle() }))
	must(view.Bind("lpCopy", func(text string) { _ = setClipboard(text) }))
	must(view.Bind("lpOpenUrl", func(u string) {
		if strings.HasPrefix(u, "http://127.0.0.1:") || strings.HasPrefix(u, "https://") {
			openURL(u)
		}
	}))
	must(view.Bind("lpExportExtension", func() string {
		dir, err := exportExtension()
		if err != nil {
			messageBox("확장 프로그램 폴더를 만들지 못했어요: " + err.Error())
			return ""
		}
		_ = setClipboard(dir)
		openURL(dir)
		return dir
	}))
}

// ── 크롬 확장 프로그램 꺼내기 ─────────────────────────────

func exportExtension() (string, error) {
	local, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(local, "LPNowPlaying2", "chrome-extension")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	err = fs.WalkDir(extensionFS, "extension", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := extensionFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, filepath.Base(p)), b, 0o644)
	})
	return dir, err
}
