package main

import (
	"context"
	"fmt"
	"image/color"
	"math"
	"sync"
	"time"
)

// ── 조작 명령 (프로그램 → 확장 프로그램 → 유튜브 플레이어) ─────────────

type Command struct {
	ID    int64   `json:"id"`
	TabID int     `json:"tabId"`
	Type  string  `json:"type"`  // toggle | play | pause | next | prev | seek
	Value float64 `json:"value"` // seek: 이동할 위치(초)
	at    time.Time
}

type CommandQueue struct {
	mu        sync.Mutex
	nextID    int64
	items     []Command
	notify    chan struct{}
	delivered int64 // 확장 프로그램이 가져간 마지막 명령 번호
}

// 명령 번호는 시각 기반으로 시작 → 프로그램을 다시 켜도 이전보다 항상 큰 번호
// (YouTube 탭은 마지막으로 실행한 번호보다 작은 명령을 중복으로 보고 무시하기 때문)
func NewCommandQueue() *CommandQueue {
	return &CommandQueue{notify: make(chan struct{}), nextID: time.Now().UnixMilli() * 10}
}

func (q *CommandQueue) Push(c Command) int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextID++
	c.ID, c.at = q.nextID, time.Now()
	// 같은 탭에 대한 이동(seek)은 마지막 것만 남김 (휠을 빠르게 굴렸을 때)
	if c.Type == "seek" {
		kept := q.items[:0]
		for _, it := range q.items {
			if !(it.Type == "seek" && it.TabID == c.TabID) {
				kept = append(kept, it)
			}
		}
		q.items = kept
	}
	q.items = append(q.items, c)
	close(q.notify)
	q.notify = make(chan struct{})
	return c.ID
}

func (q *CommandQueue) Delivered() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.delivered
}

// 쌓인 명령을 가져감. 없으면 최대 wait 동안 기다림. 3초 넘게 묵은 명령은 버림
func (q *CommandQueue) Take(ctx context.Context, wait time.Duration) []Command {
	deadline := time.After(wait)
	for {
		q.mu.Lock()
		var out []Command
		for _, c := range q.items {
			if time.Since(c.at) < 3*time.Second {
				out = append(out, c)
			}
		}
		q.items = nil
		for _, c := range out {
			if c.ID > q.delivered {
				q.delivered = c.ID
			}
		}
		ch := q.notify
		q.mu.Unlock()
		if len(out) > 0 || wait <= 0 {
			return out
		}
		select {
		case <-ch:
		case <-deadline:
			return []Command{}
		case <-ctx.Done():
			return []Command{}
		}
	}
}

// 마지막 조작이 어디까지 갔는지 (설정 창 "마지막 조작" 표시용)
type CmdStatus struct {
	Type   string  `json:"type"`
	State  string  `json:"state"` // rejected | queued | delivered | done
	Reason string  `json:"reason"`
	AgoSec float64 `json:"agoSec"`
	id     int64
	at     time.Time
}

func (a *App) LastCommand() CmdStatus {
	a.mu.Lock()
	c := a.lastCmd
	a.mu.Unlock()
	if c.at.IsZero() {
		return CmdStatus{}
	}
	c.AgoSec = time.Since(c.at).Seconds()
	if c.State != "rejected" {
		switch {
		case a.store.MaxAck() >= c.id:
			c.State = "done"
		case a.cmds.Delivered() >= c.id:
			c.State = "delivered"
		}
	}
	return c
}

func (a *App) setLastCmd(c CmdStatus) {
	c.at = time.Now()
	a.mu.Lock()
	a.lastCmd = c
	a.mu.Unlock()
}

// 지금 표시 중인 탭에 명령 보내기
func (a *App) Command(typ string, value float64) bool {
	reject := func(why string) bool {
		a.setLastCmd(CmdStatus{Type: typ, State: "rejected", Reason: why})
		return false
	}
	if !a.Running() {
		return reject("프로그램이 [중지] 상태예요")
	}
	np := a.NowFor("preview")
	if !np.Connected || np.TabID == 0 {
		return reject("확장 프로그램과 연결된 YouTube 탭이 없어요")
	}
	if !np.HasTrack && typ != "toggle" && typ != "next" && typ != "prev" {
		return reject("재생 중인 곡이 없어요")
	}
	switch typ {
	case "toggle", "play", "pause", "next", "prev":
	case "seekBy": // 상대 이동(초) → 절대 위치로
		typ, value = "seek", np.Position+value
		fallthrough
	case "seek":
		if np.IsAd {
			return reject("광고 중에는 이동할 수 없어요")
		}
		if np.Duration <= 0 {
			return reject("길이를 알 수 없는 영상(라이브 등)이에요")
		}
		value = ClampSeek(value, np.Duration)
	default:
		return reject("알 수 없는 명령")
	}
	id := a.cmds.Push(Command{TabID: np.TabID, Type: typ, Value: value})
	a.setLastCmd(CmdStatus{Type: typ, State: "queued", id: id})
	return true
}

// 이동 범위: 0 ~ (끝나기 1초 전) — 다음 곡으로 넘어가지 않게
func ClampSeek(v, dur float64) float64 {
	return math.Max(0, math.Min(v, math.Max(0, dur-1)))
}

// LP판 한 바퀴 = 30초, 긴 영상은 (길이 ÷ 20) — 어떤 길이든 20바퀴면 처음부터 끝까지
func SecondsPerTurn(dur float64) float64 { return math.Max(30, dur/20) }

// "1:05", "1:02:03"
func FormatTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// ── 앨범 색 강조 ─────────────────────────────────────────

const defaultAccent = "#ff4e45"

// strength 0 = 색 없이 은은한 흰색, 1 = 앨범 색 그대로, 1.5 = 채도를 올려 더 진하게
func AdjustAccent(hex string, strength float64) string {
	c := parseHex(hex)
	if c.A == 0 {
		c = parseHex(defaultAccent)
	}
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	if strength >= 1 {
		h, s, v := rgbToHSV(r, g, b)
		s = math.Min(1, s*(1+(strength-1)*1.4))
		r, g, b = hsvToRGB(h, s, v)
	} else {
		n := parseHex(neutralAccent)
		k := math.Max(0, strength)
		r = float64(n.R)/255*(1-k) + r*k
		g = float64(n.G)/255*(1-k) + g*k
		b = float64(n.B)/255*(1-k) + b*k
	}
	return fmt.Sprintf("#%02x%02x%02x", int(r*255+0.5), int(g*255+0.5), int(b*255+0.5))
}

func parseHex(h string) color.RGBA {
	if len(h) != 7 || h[0] != '#' {
		return color.RGBA{}
	}
	var v uint32
	if _, err := fmt.Sscanf(h[1:], "%06x", &v); err != nil {
		return color.RGBA{}
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}
