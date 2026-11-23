package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

const DefaultPort = 47800

type Settings struct {
	Port      int    `json:"port"`
	TabRule   string `json:"tabRule"`   // playingFirst | firstTab
	ShowLink  bool   `json:"showLink"`  // 링크 줄 표시
	AutoStart bool   `json:"autoStart"` // 실행하자마자 시작
	SpinSpeed string `json:"spinSpeed"` // slow | normal | fast | real

	// 화면 오버레이 · OBS 공통 표시
	BgTransparency float64 `json:"bgTransparency"` // 카드 배경 투명도 0(기본 카드) ~ 1(완전 투명)
	AccentStrength float64 `json:"accentStrength"` // 앨범 색 강조 0(색 없음) ~ 1(기본) ~ 1.5(더 진하게)

	// 화면 오버레이 (다른 프로그램 위에 강제로 표시)
	Overlay            bool    `json:"overlay"`
	OverlayPos         string  `json:"overlayPos"`         // br | bl | tr | tl
	OverlayMonitor     int     `json:"overlayMonitor"`     // 0 = 주 모니터, 1~ = 모니터 번호
	OverlayScale       float64 `json:"overlayScale"`       // 크기 배율 (모니터 배율에 곱함)
	OverlayMarginX     int     `json:"overlayMarginX"`     // 화면 가장자리에서 떨어진 거리(px, 배율 1 기준)
	OverlayMarginY     int     `json:"overlayMarginY"`     //
	OverlayOpacity     float64 `json:"overlayOpacity"`     // 0.2 ~ 1
	OverlayHideCapture bool    `json:"overlayHideCapture"` // 녹화/방송 화면 캡처에서 숨기기
	OverlayTopmost     bool    `json:"overlayTopmost"`     // 계속 맨 위로 다시 올리기

	// OBS 브라우저 소스
	OBS      bool    `json:"obs"`
	ObsPos   string  `json:"obsPos"`
	ObsScale float64 `json:"obsScale"`
}

func DefaultSettings() Settings {
	return Settings{
		Port: DefaultPort, TabRule: RulePlayingFirst, ShowLink: true, SpinSpeed: "normal",
		BgTransparency: 0, AccentStrength: 1,
		Overlay: true, OverlayPos: "br", OverlayScale: 1, OverlayMarginX: 24, OverlayMarginY: 56,
		OverlayOpacity: 1, OverlayTopmost: true,
		OBS: false, ObsPos: "br", ObsScale: 1,
	}
}

func validPos(p string) bool { return p == "br" || p == "bl" || p == "tr" || p == "tl" }

func (s *Settings) normalize() {
	if s.Port < 1024 || s.Port > 65535 {
		s.Port = DefaultPort
	}
	if s.TabRule != RuleFirstTab {
		s.TabRule = RulePlayingFirst
	}
	switch s.SpinSpeed {
	case "slow", "normal", "fast", "real":
	default:
		s.SpinSpeed = "normal"
	}
	if !validPos(s.OverlayPos) {
		s.OverlayPos = "br"
	}
	if !validPos(s.ObsPos) {
		s.ObsPos = "br"
	}
	if s.OverlayScale < 0.5 || s.OverlayScale > 3 {
		s.OverlayScale = 1
	}
	if s.ObsScale < 0.5 || s.ObsScale > 3 {
		s.ObsScale = 1
	}
	if s.OverlayOpacity < 0.2 || s.OverlayOpacity > 1 {
		s.OverlayOpacity = 1
	}
	if s.OverlayMonitor < 0 {
		s.OverlayMonitor = 0
	}
	s.BgTransparency = math.Max(0, math.Min(1, s.BgTransparency))
	s.AccentStrength = math.Max(0, math.Min(1.5, s.AccentStrength))
	s.OverlayMarginX = clampi(s.OverlayMarginX, 0, 2000)
	s.OverlayMarginY = clampi(s.OverlayMarginY, 0, 2000)
}

// LP판 회전 속도 → 한 바퀴 시간(초)
func (s Settings) SpinSeconds() float64 {
	switch s.SpinSpeed {
	case "slow":
		return 14
	case "fast":
		return 4
	case "real":
		return 1.8
	default:
		return 8
	}
}

func dataDir() string {
	base, err := os.UserConfigDir() // Windows: %APPDATA%
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "LPNowPlaying2")
}

func settingsPath() string { return filepath.Join(dataDir(), "settings.json") }

func LoadSettings() Settings {
	s := DefaultSettings()
	if b, err := os.ReadFile(settingsPath()); err == nil {
		_ = json.Unmarshal(b, &s)
		// v2.0 → v2.1: "배경 투명" 켜기/끄기 → 배경 투명도 슬라이더
		var old struct {
			OverlayTransparent *bool    `json:"overlayTransparent"`
			BgTransparency     *float64 `json:"bgTransparency"`
		}
		if json.Unmarshal(b, &old) == nil && old.BgTransparency == nil && old.OverlayTransparent != nil && *old.OverlayTransparent {
			s.BgTransparency = 1
		}
	}
	s.normalize()
	return s
}

func SaveSettings(s Settings) error {
	s.normalize()
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(settingsPath(), b, 0o644)
}

// OBS 브라우저 소스에 넣을 주소
func (s Settings) OBSURL() string {
	u := fmt.Sprintf("http://127.0.0.1:%d/overlay?target=obs", s.Port)
	if s.ObsPos != "br" && s.ObsPos != "" {
		u += "&pos=" + s.ObsPos
	}
	if s.ObsScale != 1 && s.ObsScale != 0 {
		u += fmt.Sprintf("&scale=%g", s.ObsScale)
	}
	return u
}
