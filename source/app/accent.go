package main

import (
	"bytes"

	"encoding/base64"
	"errors"
	"fmt"
	xdraw "golang.org/x/image/draw"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 앨범아트를 내려받아 대표 색(포인트 컬러)과 그림(화면 오버레이용)을 URL별로 캐시
type AccentCache struct {
	mu       sync.Mutex
	m        map[string]string
	imgs     map[string]image.Image // 화면 오버레이 라벨용 (정사각형, 최대 320px)
	imgOrder []string
	inflight map[string]bool
	client   *http.Client
}

func NewAccentCache() *AccentCache {
	return &AccentCache{
		m:        map[string]string{},
		imgs:     map[string]image.Image{},
		inflight: map[string]bool{},
		client:   &http.Client{Timeout: 8 * time.Second},
	}
}

// 준비된 색을 돌려주고, 아직 없으면 ""를 돌려주면서 백그라운드에서 계산을 시작
func (c *AccentCache) Get(artwork string) string {
	if artwork == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.m[artwork]; ok {
		return v
	}
	if !c.inflight[artwork] {
		c.inflight[artwork] = true
		go c.compute(artwork)
	}
	return ""
}

func (c *AccentCache) compute(artwork string) {
	col := ""
	var small image.Image
	if img, err := c.load(artwork); err == nil {
		col = AccentFromImage(img)
		small = squareThumb(img, 320)
	}
	c.mu.Lock()
	if small != nil {
		c.imgs[artwork] = small
		c.imgOrder = append(c.imgOrder, artwork)
		if len(c.imgOrder) > 20 {
			delete(c.imgs, c.imgOrder[0])
			c.imgOrder = c.imgOrder[1:]
		}
	}
	if len(c.m) > 300 {
		c.m = map[string]string{}
	}
	c.m[artwork] = col // 실패해도 ""로 저장해서 반복 요청하지 않음 (기본 색 사용)
	delete(c.inflight, artwork)
	c.mu.Unlock()
}

// 앨범아트를 제공하는 구글/유튜브 이미지 서버만 허용
func allowedArtHost(host string) bool {
	host = strings.ToLower(host)
	for _, suf := range []string{".googleusercontent.com", ".ytimg.com", ".ggpht.com"} {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

func (c *AccentCache) load(artwork string) (image.Image, error) {
	if strings.HasPrefix(artwork, "data:image/") {
		i := strings.Index(artwork, ";base64,")
		if i < 0 {
			return nil, errors.New("unsupported data url")
		}
		b, err := base64.StdEncoding.DecodeString(artwork[i+8:])
		if err != nil {
			return nil, err
		}
		img, _, err := image.Decode(bytes.NewReader(b))
		return img, err
	}
	u, err := url.Parse(artwork)
	if err != nil || u.Scheme != "https" || !allowedArtHost(u.Hostname()) {
		return nil, errors.New("artwork host not allowed")
	}
	req, _ := http.NewRequest(http.MethodGet, artwork, nil)
	req.Header.Set("Accept", "image/jpeg,image/png;q=0.9,*/*;q=0.5")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, 8<<20))
	return img, err
}

const neutralAccent = "#e4e6ee" // 흑백 앨범아트일 때

// 앨범아트에서 "눈에 띄는 대표 색"을 골라 어두운 배경에서도 잘 보이게 밝기를 보정
func AccentFromImage(img image.Image) string {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return ""
	}
	const n = 48
	type bin struct{ w, r, g, b float64 }
	var bins [36]bin
	total := 0.0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			r16, g16, b16, a16 := img.At(b.Min.X+x*w/n, b.Min.Y+y*h/n).RGBA()
			if a16 < 0x8000 {
				continue
			}
			r, g, bl := float64(r16)/65535, float64(g16)/65535, float64(b16)/65535
			total++
			hh, s, v := rgbToHSV(r, g, bl)
			if v < 0.18 || s < 0.22 {
				continue // 너무 어둡거나 회색에 가까운 픽셀은 제외
			}
			wt := s * s * (0.35 + v)
			i := int(hh/10) % 36
			bins[i].w += wt
			bins[i].r += r * wt
			bins[i].g += g * wt
			bins[i].b += bl * wt
		}
	}
	if total == 0 {
		return ""
	}
	best, bestScore := -1, 0.0
	for i := range bins {
		sc := bins[i].w + 0.5*bins[(i+35)%36].w + 0.5*bins[(i+1)%36].w
		if sc > bestScore {
			best, bestScore = i, sc
		}
	}
	if best < 0 || bestScore/total < 0.03 {
		return neutralAccent
	}
	var sw, sr, sg, sb float64
	for _, j := range []int{(best + 35) % 36, best, (best + 1) % 36} {
		sw += bins[j].w
		sr += bins[j].r
		sg += bins[j].g
		sb += bins[j].b
	}
	hh, s, v := rgbToHSV(sr/sw, sg/sw, sb/sw)
	s = math.Min(math.Max(s, 0.45), 0.85)
	v = math.Max(v, 0.9)
	r, g, bl := hsvToRGB(hh, s, v)
	return fmt.Sprintf("#%02x%02x%02x", int(r*255+0.5), int(g*255+0.5), int(bl*255+0.5))
}

func rgbToHSV(r, g, b float64) (h, s, v float64) {
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	v = mx
	d := mx - mn
	if mx > 0 {
		s = d / mx
	}
	if d == 0 {
		return 0, s, v
	}
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return
}

func hsvToRGB(h, s, v float64) (r, g, b float64) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

func (c *AccentCache) Has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.m[key]
	return ok
}

// 이미 계산한 색을 직접 넣기 (Spotify 앨범아트처럼 프로그램이 이미지를 갖고 있을 때)
func (c *AccentCache) Put(key, col string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 300 {
		c.m = map[string]string{}
	}
	c.m[key] = col
}

// 내려받은 앨범아트 그림 (아직 없으면 nil)
func (c *AccentCache) Image(artwork string) image.Image {
	if artwork == "" {
		return nil
	}
	c.Get(artwork) // 없으면 내려받기 시작
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imgs[artwork]
}

// 가운데 정사각형으로 잘라 size 이하로 줄이기
func squareThumb(img image.Image, size int) image.Image {
	sq := centerSquare(img)
	n := sq.Dx()
	if n > size {
		n = size
	}
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, sq, xdraw.Src, nil)
	return dst
}
