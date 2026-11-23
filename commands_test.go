package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandQueueLongPoll(t *testing.T) {
	q := NewCommandQueue()
	go func() { time.Sleep(100 * time.Millisecond); q.Push(Command{TabID: 5, Type: "toggle"}) }()
	t0 := time.Now()
	got := q.Take(context.Background(), 3*time.Second)
	if len(got) != 1 || got[0].Type != "toggle" || time.Since(t0) > time.Second {
		t.Fatalf("%v %v", got, time.Since(t0))
	}
	// 같은 탭 seek는 마지막 것만
	q.Push(Command{TabID: 5, Type: "seek", Value: 10})
	q.Push(Command{TabID: 5, Type: "seek", Value: 15})
	q.Push(Command{TabID: 5, Type: "next"})
	got = q.Take(context.Background(), 0)
	if len(got) != 2 || got[0].Value != 15 || got[1].Type != "next" {
		t.Fatalf("%+v", got)
	}
	if got := q.Take(context.Background(), 50*time.Millisecond); len(got) != 0 {
		t.Fatal("should time out empty")
	}
}

func TestDialMath(t *testing.T) {
	cases := map[float64]float64{180: 30, 600: 30, 3600: 180, 10800: 540}
	for dur, want := range cases {
		if got := SecondsPerTurn(dur); got != want {
			t.Errorf("SecondsPerTurn(%v)=%v want %v", dur, got, want)
		}
	}
	if ClampSeek(-5, 200) != 0 || ClampSeek(500, 200) != 199 || ClampSeek(50, 200) != 50 {
		t.Fatal("clamp")
	}
	if FormatTime(65) != "1:05" || FormatTime(3723) != "1:02:03" {
		t.Fatal(FormatTime(65), FormatTime(3723))
	}
}

func TestAdjustAccent(t *testing.T) {
	if AdjustAccent("#3366cc", 1) != "#3366cc" {
		t.Fatal(AdjustAccent("#3366cc", 1))
	}
	if AdjustAccent("#3366cc", 0) != neutralAccent {
		t.Fatal(AdjustAccent("#3366cc", 0))
	}
	strong := AdjustAccent("#5577aa", 1.5)
	_, s1, _ := rgbToHSV(hexf("55"), hexf("77"), hexf("aa"))
	_, s2, _ := rgbToHSV(hexf(strong[1:3]), hexf(strong[3:5]), hexf(strong[5:7]))
	if s2 <= s1 {
		t.Fatalf("stronger saturation expected %s", strong)
	}
	if AdjustAccent("", 1) != defaultAccent {
		t.Fatal("default")
	}
}

func TestSettingsMigration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	_ = os.MkdirAll(filepath.Join(dir, "LPNowPlaying2"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "LPNowPlaying2", "settings.json"), []byte(`{"overlay":true,"overlayTransparent":true}`), 0o644)
	if s := LoadSettings(); s.BgTransparency != 1 || s.AccentStrength != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestAppCommand(t *testing.T) {
	s := DefaultSettings()
	s.Port = 47897
	a := NewApp(s)
	if a.Command("toggle", 0) {
		t.Fatal("not running")
	}
	_ = a.Start()
	defer a.Stop()
	a.store.Update(TabsPayload{Tabs: []TabState{{TabID: 42, Title: "x", Artist: "y", VideoID: "v", Playing: true, Position: 100, Duration: 200, TS: time.Now().UnixMilli()}}})
	if !a.Command("seekBy", 10) || !a.Command("next", 0) {
		t.Fatal("commands rejected")
	}
	got := a.cmds.Take(context.Background(), 0)
	if len(got) != 2 || got[0].TabID != 42 || got[0].Type != "seek" || got[0].Value < 109.9 || got[0].Value > 110.5 {
		t.Fatalf("%+v", got)
	}
	a.Command("seek", 1e6)
	if got := a.cmds.Take(context.Background(), 0); got[0].Value != 199 {
		t.Fatalf("clamp %+v", got)
	}
}
