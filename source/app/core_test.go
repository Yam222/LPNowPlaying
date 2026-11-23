package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestPFN(t *testing.T) {
	got := PackageFamilyName("Microsoft.WindowsCalculator", "CN=Microsoft Corporation, O=Microsoft Corporation, L=Redmond, S=Washington, C=US")
	if got != "Microsoft.WindowsCalculator_8wekyb3d8bbwe" {
		t.Fatalf("got %s", got)
	}
}

func TestSelect(t *testing.T) {
	tabs := []TabState{
		{TabID: 3, WindowID: 2, Index: 0, Title: "C", Playing: true},
		{TabID: 2, WindowID: 1, Index: 4, Title: "B", Playing: true},
		{TabID: 1, WindowID: 1, Index: 1, Title: "A"},
	}
	if s, _ := SelectTab(tabs, RuleFirstTab); s.Title != "A" {
		t.Fatalf("firstTab: %s", s.Title)
	}
	if s, _ := SelectTab(tabs, RulePlayingFirst); s.Title != "B" {
		t.Fatalf("playingFirst: %s", s.Title)
	}
	tabs[0].Playing, tabs[1].Playing = false, false
	if s, _ := SelectTab(tabs, RulePlayingFirst); s.Title != "A" {
		t.Fatalf("fallback: %s", s.Title)
	}
}

func TestServer(t *testing.T) {
	s := DefaultSettings()
	s.Port = 47899
	s.OBS = false
	a := NewApp(s)
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	body, _ := json.Marshal(TabsPayload{Tabs: []TabState{{TabID: 1, Title: " 좋은  날 ", Artist: "아이유", VideoID: "abc", Playing: true, Position: 10, Duration: 200, TS: time.Now().UnixMilli()}}})
	r, err := http.Post("http://127.0.0.1:47899/api/tabs", "application/json", bytes.NewReader(body))
	if err != nil || r.StatusCode != 200 {
		t.Fatal(err, r)
	}
	var np NowPlaying
	r, _ = http.Get("http://127.0.0.1:47899/api/now?target=widget")
	_ = json.NewDecoder(r.Body).Decode(&np)
	if !np.HasTrack || np.Title != "좋은 날" || np.URL != "https://music.youtube.com/watch?v=abc" || !np.Enabled || !np.Running {
		t.Fatalf("%+v", np)
	}
	r, _ = http.Get("http://127.0.0.1:47899/api/now?target=obs")
	_ = json.NewDecoder(r.Body).Decode(&np)
	if np.Enabled {
		t.Fatal("obs should be disabled")
	}
	r, _ = http.Get("http://127.0.0.1:47899/overlay")
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	a.Stop()
	if _, err := http.Get("http://127.0.0.1:47899/api/now"); err == nil {
		t.Fatal("server should be stopped")
	}
}

func solid(c color.RGBA, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestAccent(t *testing.T) {
	// 대부분 어두운 배경 + 파란 영역 → 파란 계열이 나와야 함
	img := solid(color.RGBA{15, 15, 20, 255}, 100, 100)
	for y := 20; y < 70; y++ {
		for x := 20; x < 70; x++ {
			img.Set(x, y, color.RGBA{30, 80, 200, 255})
		}
	}
	got := AccentFromImage(img)
	h, s, v := rgbToHSV(hexf(got[1:3]), hexf(got[3:5]), hexf(got[5:7]))
	if h < 200 || h > 240 || s < 0.45 || v < 0.89 {
		t.Fatalf("blue accent: %s (h=%.0f s=%.2f v=%.2f)", got, h, s, v)
	}
	if g := AccentFromImage(solid(color.RGBA{128, 128, 128, 255}, 50, 50)); g != neutralAccent {
		t.Fatalf("gray: %s", g)
	}
	if g := AccentFromImage(solid(color.RGBA{220, 30, 40, 255}, 50, 50)); g[1] < 'd' {
		t.Fatalf("red: %s", g)
	}
	if allowedArtHost("evil.com") || !allowedArtHost("lh3.googleusercontent.com") || !allowedArtHost("i.ytimg.com") {
		t.Fatal("host filter")
	}
}

func hexf(s string) float64 {
	v, _ := strconv.ParseUint(s, 16, 8)
	return float64(v) / 255
}

func TestAccentCacheDataURL(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, solid(color.RGBA{40, 200, 90, 255}, 20, 20))
	u := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	c := NewAccentCache()
	c.Get(u)
	for i := 0; i < 50 && c.Get(u) == ""; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if got := c.Get(u); got == "" || got == neutralAccent {
		t.Fatalf("got %q", got)
	}
}
