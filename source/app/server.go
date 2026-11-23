package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/overlay.html
var webFS embed.FS

// 버전: OBS 화면은 이 값이 바뀌면 자동 새로고침, 위젯은 이 값과 같은지 비교해서 "예전 위젯" 안내
const (
	AppVersion = "2.1.4"
	ExtVersion = "2.1.4" // 크롬 확장 프로그램 최신 버전
)

// 오버레이/위젯이 마지막으로 접속한 시각과 버전
type ClientInfo struct {
	Version string  `json:"version"`
	AgoSec  float64 `json:"agoSec"`
	Seen    bool    `json:"seen"`
}

type clientSeen struct {
	version string
	at      time.Time
}

type App struct {
	clients  map[string]clientSeen
	mu       sync.Mutex
	settings Settings
	store    *Store
	accent   *AccentCache
	cmds     *CommandQueue
	lastCmd  CmdStatus
	srv      *http.Server
	running  bool
	lastErr  string
}

func NewApp(s Settings) *App {
	a := &App{settings: s, store: NewStore(), accent: NewAccentCache(), clients: map[string]clientSeen{}, cmds: NewCommandQueue()}
	return a
}

func (a *App) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

func (a *App) SetSettings(s Settings) error {
	s.normalize()
	a.mu.Lock()
	old := a.settings
	a.settings = s
	a.mu.Unlock()
	_ = old
	return SaveSettings(s)
}

func (a *App) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

func (a *App) LastError() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastErr
}

func (a *App) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return nil
	}
	addr := "127.0.0.1:" + strconv.Itoa(a.settings.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		a.lastErr = fmt.Sprintf("포트 %d을(를) 열 수 없어요. 다른 프로그램이 쓰고 있는지 확인해 주세요. (%v)", a.settings.Port, err)
		return errors.New(a.lastErr)
	}
	a.srv = &http.Server{Handler: localOnly(a.routes()), ReadHeaderTimeout: 5 * time.Second}
	a.running = true
	a.lastErr = ""
	a.store.Clear()
	go func(srv *http.Server) { _ = srv.Serve(ln) }(a.srv)
	return nil
}

func (a *App) Stop() {
	a.mu.Lock()
	srv := a.srv
	a.srv = nil
	a.running = false
	a.clients = map[string]clientSeen{}
	a.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	a.store.Clear()
}

func (a *App) NowFor(target string) NowPlaying {
	s := a.Settings()
	np := a.store.Now(s.TabRule)
	np.Running = a.Running()
	np.ShowLink = s.ShowLink
	np.Build = AppVersion
	np.SpinSeconds = s.SpinSeconds()
	if np.HasTrack {
		np.Accent = AdjustAccent(a.accent.Get(np.Artwork), s.AccentStrength)
	}
	np.BgAlpha = 1 - s.BgTransparency
	np.Transparent = s.BgTransparency >= 0.5
	np.Glow = s.AccentStrength
	switch target {
	case "obs":
		np.Enabled = s.OBS
	case "overlay":
		np.Enabled = s.Overlay
	default: // preview = OBS 화면 미리보기
		np.Enabled = true
	}
	return np
}

func (a *App) Client(target string) ClientInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.clients[target]
	if !ok || !a.running {
		return ClientInfo{}
	}
	return ClientInfo{Version: c.version, AgoSec: time.Since(c.at).Seconds(), Seen: true}
}

// 허용: 확장 프로그램, 이 프로그램이 띄운 페이지(OBS/미리보기), 브라우저가 아닌 요청
// 거부: 일반 웹사이트가 몰래 127.0.0.1로 보내는 요청 (재생 조작·재생 정보 엿보기 방지)
func allowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	if strings.HasPrefix(origin, "chrome-extension://") || strings.HasPrefix(origin, "moz-extension://") {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")
}

func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" { // DNS 리바인딩 방지
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !allowedOrigin(r.Header.Get("Origin")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
}

func corsFor(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if o := r.Header.Get("Origin"); o != "" && allowedOrigin(o) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", o)
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Allow-Private-Network", "true")
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/tabs", func(w http.ResponseWriter, r *http.Request) {
		corsFor(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var p TabsPayload
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		a.store.Update(p)
		writeJSON(w, map[string]any{"ok": true, "rule": a.Settings().TabRule})
	})

	mux.HandleFunc("/api/now", func(w http.ResponseWriter, r *http.Request) {
		corsFor(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		q := r.URL.Query()
		target := q.Get("target")
		if target == "obs" {
			a.mu.Lock()
			a.clients[target] = clientSeen{version: q.Get("v"), at: time.Now()}
			a.mu.Unlock()
		}
		writeJSON(w, a.NowFor(target))
	})

	// 조작 명령: 확장 프로그램이 기다렸다가 가져감 (long-poll)
	mux.HandleFunc("/api/commands", func(w http.ResponseWriter, r *http.Request) {
		corsFor(w, r)
		wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
		writeJSON(w, map[string]any{"commands": a.cmds.Take(r.Context(), time.Duration(clampi(wait, 0, 25))*time.Second)})
	})
	// 마지막 조작 상태 (문제 확인용)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		corsFor(w, r)
		writeJSON(w, map[string]any{"lastCmd": a.LastCommand(), "version": AppVersion})
	})
	// 조작 명령 보내기 (테스트/외부 연동용, 내 PC 안에서만)
	mux.HandleFunc("/api/command", func(w http.ResponseWriter, r *http.Request) {
		corsFor(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var c struct {
			Type  string  `json:"type"`
			Value float64 `json:"value"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&c)
		writeJSON(w, map[string]any{"ok": a.Command(c.Type, c.Value)})
	})

	mux.HandleFunc("/overlay", func(w http.ResponseWriter, r *http.Request) {
		corsFor(w, r)
		b, _ := webFS.ReadFile("web/overlay.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(bytes.ReplaceAll(b, []byte("__LP_BUILD__"), []byte(AppVersion)))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/overlay?target=preview", http.StatusFound)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
