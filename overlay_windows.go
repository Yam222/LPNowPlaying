//go:build windows

package main

// 화면 오버레이 창: 다른 모든 창 위에 강제로 표시되는 투명·클릭 통과 창
//  - WS_EX_TOPMOST + 주기적으로 다시 맨 위로 올리기 + 활성 창이 바뀔 때마다 즉시 다시 올리기
//  - WS_EX_LAYERED + UpdateLayeredWindow: 픽셀 단위 투명(배경 없는 LP 카드)
//  - WS_EX_TRANSPARENT + WS_EX_NOACTIVATE: 마우스 클릭은 아래 프로그램으로 통과, 포커스를 뺏지 않음
//  - Ctrl + Alt + L: 숨기기/보이기

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	gdi32                        = windows.NewLazySystemDLL("gdi32.dll")
	shcore                       = windows.NewLazySystemDLL("shcore.dll")
	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procUpdateLayeredWindow      = user32.NewProc("UpdateLayeredWindow")
	procSetTimer                 = user32.NewProc("SetTimer")
	procKillTimer                = user32.NewProc("KillTimer")
	procGetDC                    = user32.NewProc("GetDC")
	procRegisterHotKey           = user32.NewProc("RegisterHotKey")
	procSetWinEventHook          = user32.NewProc("SetWinEventHook")
	procEnumDisplayMonitors      = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	procMonitorFromPoint         = user32.NewProc("MonitorFromPoint")
	procSetWindowDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procCreateCompatibleDC       = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection         = gdi32.NewProc("CreateDIBSection")
	procSelectObject             = gdi32.NewProc("SelectObject")
	procDeleteObject             = gdi32.NewProc("DeleteObject")
	procGetDpiForMonitor         = shcore.NewProc("GetDpiForMonitor")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procSetCapture               = user32.NewProc("SetCapture")
	procReleaseCapture           = user32.NewProc("ReleaseCapture")
	procSetCursor                = user32.NewProc("SetCursor")
	procLoadCursorW              = user32.NewProc("LoadCursorW")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
)

const (
	wmSetCursor      = 0x0020
	wmMouseActivate  = 0x0021
	wmMouseMove      = 0x0200
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmMouseWheel     = 0x020A
	wmCaptureChanged = 0x0215
	maNoActivate     = 3
	htClient         = 1
	idcHand          = 32649
	gwlExStyle       = ^uintptr(19) // -20
	timerHover       = 3
	vkLeft           = 0x25
	vkRight          = 0x27
)

// 재생 조작 단축키 (프로그램이 [시작] 상태일 때만 등록)
var mediaHotkeys = []struct {
	id   uintptr
	vk   uintptr
	name string
}{
	{10, vkLeft, "Ctrl+Alt+←"}, {11, vkRight, "Ctrl+Alt+→"},
}

const (
	wsPopup           = 0x80000000
	wsExTopmost       = 0x00000008
	wsExTransparent   = 0x00000020
	wsExToolWindow    = 0x00000080
	wsExLayered       = 0x00080000
	wsExNoActivate    = 0x08000000
	swpNoSize         = 0x0001
	swpNoMove         = 0x0002
	swpNoActivate     = 0x0010
	swpShowWindow     = 0x0040
	swpHideWindow     = 0x0080
	swpNoOwnerZOrder  = 0x0200
	wmDestroy         = 0x0002
	wmTimer           = 0x0113
	wmHotkey          = 0x0312
	wmNcHitTest       = 0x0084
	wmDisplayChange   = 0x007E
	wmDpiChanged      = 0x02E0
	htTransparent     = ^uintptr(0) // -1
	ulwAlpha          = 0x02
	timerFrame        = 1
	timerTopmost      = 2
	hotkeyToggle      = 1
	modAlt            = 0x0001
	modControl        = 0x0002
	modNoRepeat       = 0x4000
	eventForeground   = 0x0003
	winEventOutOfCtx  = 0x0000
	wdaNone           = 0x00
	wdaExcludeCapture = 0x11
	monitorPrimary    = 1
)

var hwndTopmost = ^uintptr(0) // HWND_TOPMOST = -1

type point struct{ X, Y int32 }
type size struct{ CX, CY int32 }
type rect struct{ L, T, R, B int32 }

type monitorInfoEx struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
	Device  [32]uint16
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

// 모니터 목록 (설정 화면용)
type MonitorDesc struct {
	Index   int    `json:"index"` // 1부터
	Name    string `json:"name"`
	Primary bool   `json:"primary"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	work    rect
	dpi     uint32
}

// 콜백은 한 번만 만들어야 함 (Go는 만들 수 있는 콜백 개수에 제한이 있음)
var (
	enumMu    sync.Mutex
	enumHs    []uintptr
	enumCB    = windows.NewCallback(func(h, hdc, lprc, data uintptr) uintptr { enumHs = append(enumHs, h); return 1 })
	monCache  []MonitorDesc
	monCached time.Time
)

// 모니터 목록 (2초 캐시, 화면 설정이 바뀌면 바로 갱신)
func listMonitors() []MonitorDesc {
	enumMu.Lock()
	defer enumMu.Unlock()
	if monCache != nil && time.Since(monCached) < 2*time.Second {
		return monCache
	}
	enumHs = enumHs[:0]
	procEnumDisplayMonitors.Call(0, 0, enumCB, 0)
	var out []MonitorDesc
	for i, h := range enumHs {
		var mi monitorInfoEx
		mi.Size = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); r == 0 {
			continue
		}
		var dx, dy uint32 = 96, 96
		if procGetDpiForMonitor.Find() == nil {
			procGetDpiForMonitor.Call(h, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy)))
		}
		d := MonitorDesc{
			Index: i + 1, Primary: mi.Flags&monitorPrimary != 0,
			W: int(mi.Monitor.R - mi.Monitor.L), H: int(mi.Monitor.B - mi.Monitor.T), work: mi.Work, dpi: dx,
		}
		d.Name = "모니터 " + strconv.Itoa(d.Index)
		if d.Primary {
			d.Name += " (주 모니터)"
		}
		d.Name += " · " + strconv.Itoa(d.W) + "×" + strconv.Itoa(d.H)
		out = append(out, d)
	}
	monCache, monCached = out, time.Now()
	return out
}

func invalidateMonitors() {
	enumMu.Lock()
	monCache = nil
	enumMu.Unlock()
}

type Overlay struct {
	app *App
	r   *Renderer

	mu         sync.Mutex
	hwnd       uintptr
	screenDC   uintptr
	memDC      uintptr
	dib        uintptr
	bits       []byte
	bw, bh     int
	pos        point
	shown      bool
	userHidden bool // Ctrl+Alt+L
	hotkeyOK   bool
	affinity   int

	alpha     float64 // 현재 페이드 값 0~1
	curKey    string
	lastProg  float64
	angle     float64
	last      time.Time
	start     time.Time
	fast      bool
	fullDirty bool

	// 마우스 조작
	np          NowPlaying // 마지막으로 그린 곡 정보
	interactive bool       // 지금 클릭을 받는 중 (LP판/버튼 위)
	hover       string
	pressed     string
	down        bool    // 판 위에서 마우스를 누르고 있음
	moved       bool    // 클릭이 아니라 끌기
	dial        bool    // 다이얼(돌리기) 모드
	downX       float64 // 누른 위치
	downY       float64
	lastAng     float64
	angValid    bool
	accDeg      float64 // 누적 회전 각도 (시계방향 +)
	startPos    float64 // 누를 때 재생 위치
	baseAngle   float64
	optPos      float64 // 명령을 보낸 직후 화면에 먼저 보여 줄 값
	optPosUntil time.Time
	optPlay     bool
	optPlayTill time.Time
	flash       string
	flashUntil  time.Time
	info        string // 휠/단축키 이동 시 잠깐 보여 줄 시간
	infoSub     string
	infoUntil   time.Time

	releasing    bool    // ReleaseCapture 중 (WM_CAPTURECHANGED를 취소로 보지 않음)
	dispPos      float64 // 화면에 그린 재생 위치 (흔들림 방지)
	seekAt       time.Time
	dispKey      string
	hotkeysOn    bool
	hotkeyFailed []string
}

var theOverlay *Overlay

func NewOverlay(app *App) *Overlay {
	o := &Overlay{app: app, r: NewRenderer(LoadFonts()), start: time.Now(), last: time.Now(), affinity: -1}
	theOverlay = o
	o.create()
	return o
}

func (o *Overlay) create() {
	inst, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	cls, _ := windows.UTF16PtrFromString("LPNowPlaying2Overlay")
	wc := wndClassEx{
		WndProc:   windows.NewCallback(overlayWndProc),
		Instance:  inst,
		ClassName: cls,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	title, _ := windows.UTF16PtrFromString("LP Now Playing 2 Overlay")
	h, _, _ := procCreateWindowExW.Call(
		wsExLayered|wsExTransparent|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)), wsPopup,
		0, 0, 10, 10, 0, 0, inst, 0)
	o.hwnd = h
	o.screenDC, _, _ = procGetDC.Call(0)
	o.memDC, _, _ = procCreateCompatibleDC.Call(o.screenDC)

	r, _, _ := procRegisterHotKey.Call(h, hotkeyToggle, modControl|modAlt|modNoRepeat, 'L')
	o.hotkeyOK = r != 0

	procSetTimer.Call(h, timerFrame, 250, 0)
	procSetTimer.Call(h, timerTopmost, 400, 0)
	procSetTimer.Call(h, timerHover, 40, 0) // 마우스가 LP판/버튼 위에 있는지 확인
	// 다른 창이 활성화될 때마다 바로 다시 맨 위로
	procSetWinEventHook.Call(eventForeground, eventForeground, 0, windows.NewCallback(func(hook, event, hwnd, idObj, idChild, thread, t uintptr) uintptr {
		if theOverlay != nil {
			theOverlay.raise()
		}
		return 0
	}), 0, 0, winEventOutOfCtx)
}

func overlayWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	o := theOverlay
	switch uint32(msg) {
	case wmNcHitTest:
		if o != nil && (o.interactive || o.down || o.pressed != "") {
			return htClient
		}
		return htTransparent
	case wmMouseActivate:
		return maNoActivate // 클릭해도 게임/다른 창의 포커스를 뺏지 않음
	case wmSetCursor:
		if o != nil && o.interactive {
			c, _, _ := procLoadCursorW.Call(0, idcHand)
			procSetCursor.Call(c)
			return 1
		}
	case wmLButtonDown:
		if o != nil {
			o.onDown(lpX(lp), lpY(lp))
		}
		return 0
	case wmMouseMove:
		if o != nil {
			o.onMove(lpX(lp), lpY(lp))
		}
		return 0
	case wmLButtonUp:
		if o != nil {
			o.onUp(lpX(lp), lpY(lp))
		}
		return 0
	case wmCaptureChanged:
		// 다른 창이 마우스를 가져간 경우에만 취소 (손을 떼서 직접 놓은 경우는 onUp에서 처리)
		if o != nil && !o.releasing && (o.down || o.pressed != "") {
			o.cancelDrag()
		}
		return 0
	case wmMouseWheel:
		if o != nil {
			o.onWheel(int16(wp >> 16))
		}
		return 0
	case wmTimer:
		if o != nil && o.hwnd == hwnd {
			switch wp {
			case timerFrame:
				o.tick()
			case timerTopmost:
				o.raise()
			case timerHover:
				o.pollHover()
			}
		}
		return 0
	case wmHotkey:
		if o != nil {
			o.onHotkey(wp)
		}
		return 0
	case wmDisplayChange, wmDpiChanged:
		invalidateMonitors()
		if o != nil {
			o.mu.Lock()
			o.fullDirty = true
			o.mu.Unlock()
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

// 맨 위로 다시 올리기 (다른 "항상 위" 창이 나중에 올라와도 그 위로)
func (o *Overlay) raise() {
	if o.hwnd == 0 || !o.shown || !o.app.Settings().OverlayTopmost {
		return
	}
	procSetWindowPos.Call(o.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpNoOwnerZOrder)
}

func (o *Overlay) Toggle() {
	o.mu.Lock()
	o.userHidden = !o.userHidden
	o.mu.Unlock()
}

type OverlayStatus struct {
	Shown        bool          `json:"shown"`
	UserHidden   bool          `json:"userHidden"`
	Hotkey       bool          `json:"hotkey"`
	HotkeyFailed []string      `json:"hotkeyFailed"` // 다른 프로그램이 이미 쓰고 있는 단축키
	Monitors     []MonitorDesc `json:"monitors"`
}

func (o *Overlay) Status() OverlayStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	failed := append([]string{}, o.hotkeyFailed...)
	if !o.hotkeyOK {
		failed = append([]string{"Ctrl+Alt+L"}, failed...)
	}
	return OverlayStatus{Shown: o.shown && o.alpha > 0.01, UserHidden: o.userHidden, Hotkey: o.hotkeyOK, HotkeyFailed: failed}
}

func parseHexColor(h string) color.RGBA {
	if len(h) != 7 || h[0] != '#' {
		return color.RGBA{}
	}
	v, err := strconv.ParseUint(h[1:], 16, 32)
	if err != nil {
		return color.RGBA{}
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}

// 대상 모니터 고르기
func (o *Overlay) monitor(idx int) MonitorDesc {
	ms := listMonitors()
	for _, m := range ms {
		if idx > 0 && m.Index == idx {
			return m
		}
	}
	for _, m := range ms {
		if m.Primary {
			return m
		}
	}
	if len(ms) > 0 {
		return ms[0]
	}
	return MonitorDesc{work: rect{0, 0, 1920, 1040}, dpi: 96}
}

func (o *Overlay) tick() {
	defer func() { _ = recover() }() // 그리기 중 문제가 생겨도 프로그램 전체가 꺼지지 않도록
	now := time.Now()
	dt := now.Sub(o.last).Seconds()
	if dt > 0.5 {
		dt = 0.5
	}
	o.last = now
	s := o.app.Settings()
	np := o.app.NowFor("overlay")
	o.syncHotkeys()
	o.applyOptimistic(&np, now)

	o.mu.Lock()
	userHidden := o.userHidden
	o.mu.Unlock()

	want := np.Running && np.Enabled && np.HasTrack && !userHidden

	// 페이드: 나타날 때 0.35초, 사라질 때/곡 바뀔 때 0.25초
	target := 0.0
	if want {
		target = 1
		if o.curKey != "" && np.Key != o.curKey && o.alpha > 0 {
			target = 0 // 곡이 바뀜 → 먼저 사라졌다가 새 곡으로 나타남
		}
	}
	if target > o.alpha {
		o.alpha = math.Min(target, o.alpha+dt/0.35)
	} else if target < o.alpha {
		o.alpha = math.Max(target, o.alpha-dt/0.25)
	}
	if want && (o.alpha == 0 || o.curKey == "") {
		o.curKey = np.Key
	}

	if o.alpha <= 0 && !want {
		o.hide()
		o.setTimer(250)
		return
	}

	// 위치·크기
	mon := o.monitor(s.OverlayMonitor)
	ms := float64(mon.dpi) / 96
	scale := ms * s.OverlayScale

	if o.dial {
		o.angle = o.baseAngle + o.accDeg // 판이 손을 따라 돎
	} else if np.Playing {
		o.angle = math.Mod(o.angle+dt*360/s.SpinSeconds(), 360)
	}
	if want && o.curKey == np.Key {
		tr := Track{
			Key: np.Key, Title: np.Title, Artist: np.Artist, Album: np.Album, Source: np.Source,
			Art: o.app.accent.Image(np.Artwork), Accent: parseHexColor(np.Accent),
		}
		if np.ShowLink {
			tr.Link = np.LinkText
		}
		o.r.SetContent(tr, Style{Scale: scale, BgAlpha: np.BgAlpha, Glow: np.Glow, ShowLink: np.ShowLink})
	}
	// 재생 중에 1.5초 이내로 뒤로 가는 값은 무시 (읽는 시점 차이로 생기는 흔들림 방지, 직접 뒤로 이동한 건 그대로)
	if np.Playing && !o.dial && o.dispKey == np.Key && np.Position < o.dispPos && o.dispPos-np.Position < 1.5 &&
		!now.Before(o.optPosUntil) && now.Sub(o.seekAt) > 3*time.Second {
		np.Position = o.dispPos
	}
	o.dispPos, o.dispKey = np.Position, np.Key
	prog := 0.0
	if np.Duration > 0 {
		prog = np.Position / np.Duration
		if o.dial {
			prog = o.preview() / np.Duration
		}
	}
	if o.curKey != np.Key {
		prog = o.lastProg // 사라지는 동안은 이전 곡 진행률 유지
	}
	o.lastProg = prog
	if !o.r.ready {
		o.hide()
		return
	}
	o.np = np
	fs := FrameState{Angle: o.angle, Progress: prog, T: now.Sub(o.start).Seconds(), Playing: np.Playing, Hover: o.hover, Pressed: o.pressed}
	if o.dial {
		p := o.preview()
		fs.DragText = FormatTime(p) + " / " + FormatTime(np.Duration)
		fs.DragSub = signedTime(p - o.startPos)
	} else if now.Before(o.infoUntil) {
		fs.DragText, fs.DragSub = o.info, o.infoSub
	} else if now.Before(o.flashUntil) {
		fs.Flash, fs.FlashA = o.flash, math.Min(1, o.flashUntil.Sub(now).Seconds()/0.5)
	}
	dirty := o.r.Frame(fs)
	img := o.r.Image()
	w, h := img.Bounds().Dx(), img.Bounds().Dy()

	mx, my := int32(float64(s.OverlayMarginX)*ms), int32(float64(s.OverlayMarginY)*ms)
	var x, y int32
	switch s.OverlayPos {
	case "bl":
		x, y = mon.work.L+mx, mon.work.B-my-int32(h)
	case "tr":
		x, y = mon.work.R-mx-int32(w), mon.work.T+my
	case "tl":
		x, y = mon.work.L+mx, mon.work.T+my
	default:
		x, y = mon.work.R-mx-int32(w), mon.work.B-my-int32(h)
	}

	if o.ensureDIB(w, h) || o.fullDirty {
		dirty = []image.Rectangle{img.Bounds()}
		o.fullDirty = false
	}
	o.copyBGRA(img, dirty)

	aff := wdaNone
	if s.OverlayHideCapture {
		aff = wdaExcludeCapture
	}
	if aff != o.affinity {
		procSetWindowDisplayAffinity.Call(o.hwnd, uintptr(aff))
		o.affinity = aff
	}

	o.pos = point{x, y}
	sz := size{int32(w), int32(h)}
	src := point{0, 0}
	bf := blendFunction{BlendOp: 0, SourceConstantAlpha: byte(math.Round(255 * o.alpha * s.OverlayOpacity)), AlphaFormat: 1}
	procUpdateLayeredWindow.Call(o.hwnd, o.screenDC, uintptr(unsafe.Pointer(&o.pos)), uintptr(unsafe.Pointer(&sz)),
		o.memDC, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&bf)), ulwAlpha)
	if !o.shown {
		procSetWindowPos.Call(o.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpShowWindow)
		o.mu.Lock()
		o.shown = true
		o.mu.Unlock()
	}

	// 판이 돌거나, 제목이 흐르거나, 페이드 중이면 30fps, 아니면 천천히
	animating := np.Playing || o.r.titleOver > 0 || o.alpha != target || o.down || o.hover != "" ||
		now.Before(o.flashUntil) || now.Before(o.infoUntil)
	if animating {
		o.setTimer(33)
	} else {
		o.setTimer(250)
	}
}

func (o *Overlay) setTimer(ms int) {
	fast := ms < 100
	if fast == o.fast {
		return
	}
	o.fast = fast
	procSetTimer.Call(o.hwnd, timerFrame, uintptr(ms), 0)
}

func (o *Overlay) hide() {
	o.cancelDrag()
	o.setInteractive(false)
	o.hover = ""
	if o.shown {
		procSetWindowPos.Call(o.hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpHideWindow|swpNoOwnerZOrder)
		o.mu.Lock()
		o.shown = false
		o.mu.Unlock()
	}
	o.curKey = ""
}

// 크기가 바뀌면 그림판(DIB) 새로 만들기
func (o *Overlay) ensureDIB(w, h int) bool {
	if o.dib != 0 && o.bw == w && o.bh == h {
		return false
	}
	if o.dib != 0 {
		procDeleteObject.Call(o.dib)
	}
	bi := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits uintptr
	o.dib, _, _ = procCreateDIBSection.Call(o.screenDC, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	procSelectObject.Call(o.memDC, o.dib)
	o.bits = unsafe.Slice((*byte)(unsafe.Pointer(bits)), w*h*4)
	o.bw, o.bh = w, h
	return true
}

// RGBA(premultiplied) → Windows BGRA
func (o *Overlay) copyBGRA(img *image.RGBA, rects []image.Rectangle) {
	for _, rc := range rects {
		rc = rc.Intersect(img.Bounds())
		for y := rc.Min.Y; y < rc.Max.Y; y++ {
			si := img.PixOffset(rc.Min.X, y)
			di := (y*o.bw + rc.Min.X) * 4
			for x := 0; x < rc.Dx(); x++ {
				s := img.Pix[si : si+4 : si+4]
				d := o.bits[di : di+4 : di+4]
				d[0], d[1], d[2], d[3] = s[2], s[1], s[0], s[3]
				si += 4
				di += 4
			}
		}
	}
}

// ── 마우스 조작: LP판 클릭 = 재생/정지, 돌리기 = 이동(손 뗄 때), 휠 = 5초, 버튼 = 이전/다음 ──

func lpX(lp uintptr) float64 { return float64(int16(lp & 0xffff)) }
func lpY(lp uintptr) float64 { return float64(int16((lp >> 16) & 0xffff)) }

func signedTime(d float64) string {
	if d < 0 {
		return "−" + FormatTime(-d)
	}
	return "+" + FormatTime(d)
}

// 클릭 통과 켜고 끄기 (LP판/버튼 위에 있을 때만 클릭을 받음)
func (o *Overlay) setInteractive(on bool) {
	if on == o.interactive || o.hwnd == 0 {
		return
	}
	o.interactive = on
	ex, _, _ := procGetWindowLongPtrW.Call(o.hwnd, gwlExStyle)
	if on {
		ex &^= wsExTransparent
	} else {
		ex |= wsExTransparent
	}
	procSetWindowLongPtrW.Call(o.hwnd, gwlExStyle, ex)
	// 바뀐 스타일을 바로 적용 (SWP_NOMOVE|NOSIZE|NOZORDER|NOACTIVATE|FRAMECHANGED)
	procSetWindowPos.Call(o.hwnd, 0, 0, 0, 0, 0, 0x0001|0x0002|0x0004|0x0010|0x0020)
}

func (o *Overlay) pollHover() {
	if !o.shown || o.alpha < 0.3 {
		o.setInteractive(false)
		return
	}
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	hit := o.r.HitTest(float64(p.X-o.pos.X), float64(p.Y-o.pos.Y))
	o.setInteractive(hit != "" || o.down || o.pressed != "")
	if hit != o.hover && !o.down {
		o.hover = hit
		o.tick() // 버튼 강조 바로 반영
	}
}

func (o *Overlay) canSeek() bool { return o.np.HasTrack && !o.np.IsAd && o.np.Duration > 0 }

func (o *Overlay) curPos() float64 {
	if time.Now().Before(o.optPosUntil) {
		return o.optPos
	}
	return o.np.Position
}

func (o *Overlay) preview() float64 {
	return ClampSeek(o.startPos+o.accDeg/360*SecondsPerTurn(o.np.Duration), o.np.Duration)
}

func (o *Overlay) onDown(x, y float64) {
	hit := o.r.HitTest(x, y)
	switch hit {
	case "prev", "next":
		o.pressed = hit
		procSetCapture.Call(o.hwnd)
	case "disc":
		o.down, o.moved, o.dial, o.accDeg, o.angValid = true, false, false, 0, false
		o.downX, o.downY = x, y
		o.startPos, o.baseAngle = o.curPos(), o.angle
		o.trackAngle(x, y)
		procSetCapture.Call(o.hwnd)
	}
	o.tick()
}

// 판 중심 기준 각도 변화 누적 (가운데 구멍 근처에서는 각도가 튀니 잠깐 멈춤)
func (o *Overlay) trackAngle(x, y float64) {
	cx, cy := o.r.DiscCenter()
	dx, dy := x-cx, y-cy
	if math.Hypot(dx, dy) < o.r.DeadZone() {
		o.angValid = false
		return
	}
	ang := math.Atan2(dx, -dy) * 180 / math.Pi // 위쪽 0도, 시계방향 +
	if o.angValid {
		d := math.Mod(ang-o.lastAng+540, 360) - 180
		o.accDeg += d
	}
	o.lastAng, o.angValid = ang, true
}

func (o *Overlay) releaseCapture() {
	o.releasing = true
	procReleaseCapture.Call()
	o.releasing = false
}

func (o *Overlay) onMove(x, y float64) {
	if o.pressed != "" {
		hit := o.r.HitTest(x, y)
		if hit == o.pressed {
			o.hover = hit
		} else {
			o.hover = ""
		}
		return
	}
	if !o.down {
		return
	}
	o.trackAngle(x, y)
	// 손 떨림은 클릭으로: 6px 넘게 움직여야 "끌기", 그중 8도 넘게 돌려야 다이얼
	if math.Hypot(x-o.downX, y-o.downY) > 6*o.r.s {
		o.moved = true
	}
	if o.moved && math.Abs(o.accDeg) > 8 && o.canSeek() {
		o.dial = true
	}
}

func (o *Overlay) onUp(x, y float64) {
	defer o.releaseCapture() // 결과를 다 처리한 다음에 마우스 붙잡기 해제
	now := time.Now()
	if o.pressed != "" {
		if o.r.HitTest(x, y) == o.pressed {
			o.app.Command(o.pressed, 0)
		}
		o.pressed = ""
		o.tick()
		return
	}
	if !o.down {
		return
	}
	o.down = false
	switch {
	case o.dial:
		target := o.preview()
		if o.app.Command("seek", target) {
			o.optPos, o.optPosUntil, o.seekAt = target, now.Add(2500*time.Millisecond), now
		}
		o.angle = math.Mod(o.baseAngle+o.accDeg+3600, 360)
	case !o.moved:
		// 그냥 클릭 → 재생/정지
		if o.app.Command("toggle", 0) {
			o.optPlay, o.optPlayTill = !o.np.Playing, now.Add(1500*time.Millisecond)
			o.flash, o.flashUntil = "pause", now.Add(800*time.Millisecond)
			if o.optPlay {
				o.flash = "play"
			}
		}
	}
	o.dial, o.moved, o.accDeg = false, false, 0
	o.tick()
}

func (o *Overlay) cancelDrag() {
	if o.down || o.pressed != "" {
		o.down, o.dial, o.moved, o.accDeg, o.pressed = false, false, false, 0, ""
	}
}

// 휠 위로 = 앞으로 5초, 아래로 = 뒤로 5초 (LP판 위에서)
func (o *Overlay) onWheel(delta int16) {
	if o.hover != "disc" || o.down {
		return
	}
	notches := float64(delta) / 120
	if notches == 0 {
		return
	}
	o.seekRelative(5 * notches)
}

func (o *Overlay) seekRelative(sec float64) {
	if !o.canSeek() {
		return
	}
	now := time.Now()
	from := o.curPos()
	if now.After(o.infoUntil) {
		o.startPos = from // 연속으로 굴릴 때 누적 표시 기준
	}
	target := ClampSeek(from+sec, o.np.Duration)
	if o.app.Command("seek", target) {
		o.optPos, o.optPosUntil, o.seekAt = target, now.Add(2500*time.Millisecond), now
		o.info = FormatTime(target) + " / " + FormatTime(o.np.Duration)
		o.infoSub = signedTime(target - o.startPos)
		o.infoUntil = now.Add(1200 * time.Millisecond)
	}
	o.tick()
}

// 명령을 보낸 직후, 실제 반영(약 1초)까지 화면에 먼저 결과를 보여 줌
func (o *Overlay) applyOptimistic(np *NowPlaying, now time.Time) {
	if now.Before(o.optPlayTill) {
		if np.Playing == o.optPlay {
			o.optPlayTill = time.Time{}
		} else {
			np.Playing = o.optPlay
		}
	}
	if now.Before(o.optPosUntil) {
		if math.Abs(np.Position-o.optPos) < 2 {
			o.optPosUntil = time.Time{}
		} else {
			np.Position = o.optPos
		}
	}
}

// ── 단축키 ──

func (o *Overlay) syncHotkeys() {
	want := o.app.Running()
	if want == o.hotkeysOn {
		return
	}
	o.hotkeysOn = want
	var failed []string
	for _, hk := range mediaHotkeys {
		if want {
			if r, _, _ := procRegisterHotKey.Call(o.hwnd, hk.id, modControl|modAlt|modNoRepeat, hk.vk); r == 0 {
				failed = append(failed, hk.name)
			}
		} else {
			procUnregisterHotKey.Call(o.hwnd, hk.id)
		}
	}
	o.mu.Lock()
	o.hotkeyFailed = failed
	o.mu.Unlock()
}

func (o *Overlay) onHotkey(id uintptr) {
	now := time.Now()
	switch id {
	case hotkeyToggle:
		o.mu.Lock()
		o.userHidden = !o.userHidden
		o.mu.Unlock()
	case 10:
		o.app.Command("prev", 0)
	case 11:
		o.app.Command("next", 0)
	}
	_ = now
	o.tick()
}
