package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"
)

func testArt() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 320, 180)) // 16:9 썸네일처럼
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{uint8(40 + x/3), uint8(60 + y/2), 200, 255})
		}
	}
	for y := 60; y < 120; y++ {
		for x := 130; x < 190; x++ {
			img.Set(x, y, color.RGBA{255, 210, 60, 255})
		}
	}
	return img
}

func savePNG(t *testing.T, name string, img image.Image) {
	if dir := os.Getenv("LP_RENDER_OUT"); dir != "" {
		f, _ := os.Create(dir + "/" + name)
		_ = png.Encode(f, img)
		f.Close()
	}
}

func TestRenderFrames(t *testing.T) {
	fonts := LoadFonts()
	if len(fonts.Bold) == 0 {
		t.Skip("no fonts")
	}
	r := NewRenderer(fonts)
	tr := Track{Key: "a", Title: "Love wins all", Artist: "아이유 (IU)", Album: "The Winning", Source: "ytmusic",
		Link: "music.youtube.com/watch?v=JleoAppaxi0", Art: testArt(), Accent: color.RGBA{90, 140, 255, 255}}
	yt := Track{Key: "b", Title: "【MV】 정말 아주 아주 긴 노래 제목이 카드 너비를 넘어가는 경우 테스트", Artist: "HYBE LABELS", Source: "youtube", Link: "youtu.be/abcdEFGhijk", Accent: color.RGBA{255, 120, 60, 255}}
	for _, c := range []struct {
		name string
		st   Style
		tr   Track
		fs   FrameState
	}{
		{"a_default.png", Style{Scale: 1, ShowLink: true, BgAlpha: 1, Glow: 1}, tr, FrameState{Angle: 37, Progress: 0.36, T: 2.6, Playing: true}},
		{"b_bg50_hover.png", Style{Scale: 1, ShowLink: true, BgAlpha: 0.5, Glow: 1}, tr, FrameState{Angle: 37, Progress: 0.36, T: 2.6, Playing: true, Hover: "next"}},
		{"c_clear_drag.png", Style{Scale: 1, ShowLink: true, BgAlpha: 0, Glow: 1}, tr, FrameState{Angle: 95, Progress: 0.52, Playing: true, Hover: "disc", DragText: "2:31 / 4:05", DragSub: "+0:24"}},
		{"d_glow0_flash.png", Style{Scale: 1, ShowLink: true, BgAlpha: 1, Glow: 0}, tr, FrameState{Angle: 10, Progress: 0.2, Flash: "pause", FlashA: 1}},
		{"e_glow150_15x.png", Style{Scale: 1.5, ShowLink: true, BgAlpha: 1, Glow: 1.5}, yt, FrameState{Angle: 0, Progress: 0.7, T: 2.6, Playing: true, Pressed: "prev", Hover: "prev"}},
	} {
		r.SetContent(c.tr, c.st)
		r.Frame(FrameState{})
		r.Frame(c.fs)
		savePNG(t, c.name, r.Image())
	}
	r.SetContent(tr, Style{Scale: 1, BgAlpha: 1, Glow: 1, ShowLink: true})
	r.Frame(FrameState{})
	for _, h := range []struct {
		x, y float64
		want string
	}{{430, 100, "disc"}, {430 - 70, 100, "disc"}, {301, 143, "prev"}, {329, 143, "next"}, {200, 100, ""}, {260, 143, ""}} {
		if got := r.HitTest(h.x, h.y); got != h.want {
			t.Errorf("HitTest(%v,%v)=%q want %q", h.x, h.y, got, h.want)
		}
	}
}

func BenchmarkFrame(b *testing.B) {
	fonts := LoadFonts()
	r := NewRenderer(fonts)
	r.SetContent(Track{Key: "a", Title: "Love wins all", Artist: "아이유", Source: "ytmusic", Link: "music.youtube.com/watch?v=x", Art: testArt()}, Style{Scale: 1.5, ShowLink: true, BgAlpha: 1, Glow: 1})
	r.Frame(FrameState{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Frame(FrameState{Angle: float64(i), Progress: 0.4, T: float64(i) / 30, Playing: true})
	}
}

func TestSetContentSpeed(t *testing.T) {
	fonts := LoadFonts()
	r := NewRenderer(fonts)
	t0 := time.Now()
	r.SetContent(Track{Key: "a", Title: "Love wins all", Artist: "아이유", Source: "ytmusic", Link: "x", Art: testArt()}, Style{Scale: 1.5, ShowLink: true, BgAlpha: 1, Glow: 1})
	t.Logf("SetContent(1.5x): %v", time.Since(t0))
}
