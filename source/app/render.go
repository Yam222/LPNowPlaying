package main

// 화면 오버레이용 렌더러: LP 카드와 회전하는 판을 픽셀(RGBA, premultiplied)로 직접 그림.
// 판에서 회전으로 모양이 바뀌는 부분(앨범아트 라벨, 둘레 글자)만 매 프레임 다시 그려서 가볍게 동작.

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// ── 기준 레이아웃 (배율 1 기준, 픽셀) ─────────────────────────
const (
	baseW      = 520.0
	baseH      = 200.0
	cardW      = 414.0
	cardH      = 124.0
	cardR      = 18.0
	textX      = 20.0
	textRight  = 84.0 // 카드 오른쪽에서 LP판과 겹치는 영역
	deckCX     = 430.0
	deckCY     = 100.0
	deckHalf   = 86.0
	discR      = 79.0
	labelR     = 49.0
	holeR      = 5.5
	ringTextR  = 69.0
	progR      = 84.75
	progStroke = 2.5
	btnY       = 143.0 // 이전/다음 버튼: 카드 오른쪽 아래 (LP판 왼쪽)
	btnR       = 11.5
	btnPrevX   = 301.0
	btnNextX   = 329.0
)

// ── 글꼴 ───────────────────────────────────────────────

type Fonts struct {
	Bold, Regular, Mono []*opentype.Font // 앞에서부터 글자가 있는 글꼴을 사용 (한글/영문/기타 대체)
}

func loadFontFile(path string) []*opentype.Font {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if f, err := opentype.Parse(b); err == nil {
		return []*opentype.Font{f}
	}
	if c, err := opentype.ParseCollection(b); err == nil && c.NumFonts() > 0 {
		f, err := c.Font(0)
		if err == nil {
			return []*opentype.Font{f}
		}
	}
	return nil
}

func loadChain(paths []string) []*opentype.Font {
	var out []*opentype.Font
	for _, p := range paths {
		out = append(out, loadFontFile(p)...)
	}
	return out
}

// Windows 기본 글꼴 (+ 개발/테스트용 리눅스 글꼴)
func LoadFonts() *Fonts {
	win := os.Getenv("WINDIR")
	if win == "" {
		win = `C:\Windows`
	}
	wf := func(n string) string { return win + `\Fonts\` + n }
	return &Fonts{
		Bold: loadChain([]string{wf("malgunbd.ttf"), wf("segoeuib.ttf"),
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc", "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"}),
		Regular: loadChain([]string{wf("malgun.ttf"), wf("segoeui.ttf"),
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"}),
		Mono: loadChain([]string{wf("consola.ttf"), wf("malgun.ttf"),
			"/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf", "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"}),
	}
}

type textFace struct {
	faces  []font.Face
	ascent float64
	height float64
}

func (fs *Fonts) face(chain []*opentype.Font, size float64) *textFace {
	tf := &textFace{}
	for _, f := range chain {
		fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
		if err == nil {
			tf.faces = append(tf.faces, fc)
		}
	}
	if len(tf.faces) > 0 {
		m := tf.faces[0].Metrics()
		tf.ascent = float64(m.Ascent) / 64
		tf.height = float64(m.Ascent+m.Descent) / 64
	} else {
		tf.ascent, tf.height = size*0.9, size*1.2
	}
	return tf
}

func (tf *textFace) pick(r rune) font.Face {
	for _, f := range tf.faces {
		if _, ok := f.GlyphAdvance(r); ok {
			return f
		}
	}
	if len(tf.faces) > 0 {
		return tf.faces[0]
	}
	return nil
}

func (tf *textFace) advance(r rune) float64 {
	f := tf.pick(r)
	if f == nil {
		return 0
	}
	a, _ := f.GlyphAdvance(r)
	return float64(a) / 64
}

func (tf *textFace) measure(s string, spacing float64) float64 {
	w := 0.0
	n := 0
	for _, r := range s {
		w += tf.advance(r) + spacing
		n++
	}
	if n > 0 {
		w -= spacing
	}
	return w
}

// 기준선(baseline) y에 글자 그리기 (src = 색 또는 그림)
func (tf *textFace) draw(dst draw.Image, x, baseline float64, s string, src image.Image, spacing float64) float64 {
	for _, r := range s {
		f := tf.pick(r)
		if f == nil {
			continue
		}
		dot := fixed.Point26_6{X: fixed.Int26_6(math.Round(x * 64)), Y: fixed.Int26_6(math.Round(baseline * 64))}
		dr, mask, mp, adv, ok := f.Glyph(dot, r)
		if ok && mask != nil {
			draw.DrawMask(dst, dr, src, image.Point{}, mask, mp, draw.Over)
		}
		x += float64(adv)/64 + spacing
	}
	return x
}

// 너비를 넘으면 뒤를 잘라 "…" 붙이기
func (tf *textFace) ellipsize(s string, maxW, spacing float64) string {
	if tf.measure(s, spacing) <= maxW {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 {
		rs = rs[:len(rs)-1]
		t := strings.TrimRight(string(rs), " ") + "…"
		if tf.measure(t, spacing) <= maxW {
			return t
		}
	}
	return "…"
}

// ── 그리기 도구 ───────────────────────────────────────

func rgba(r, g, b uint8, a float64) color.RGBA {
	al := math.Max(0, math.Min(1, a))
	return color.RGBA{uint8(float64(r) * al), uint8(float64(g) * al), uint8(float64(b) * al), uint8(255 * al)}
}

func uni(c color.RGBA) *image.Uniform { return image.NewUniform(c) }

func addCircle(z *vector.Rasterizer, cx, cy, r float64, ccw bool) {
	n := int(math.Max(24, r*1.2))
	for i := 0; i <= n; i++ {
		a := 2 * math.Pi * float64(i) / float64(n)
		if ccw {
			a = -a
		}
		x, y := float32(cx+r*math.Sin(a)), float32(cy-r*math.Cos(a))
		if i == 0 {
			z.MoveTo(x, y)
		} else {
			z.LineTo(x, y)
		}
	}
	z.ClosePath()
}

func newRaster(b image.Rectangle) *vector.Rasterizer {
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	z.DrawOp = draw.Over
	return z
}

// dst 좌표계(원점 = dst.Bounds().Min)로 원 채우기
func fillCircle(dst *image.RGBA, cx, cy, r float64, c color.RGBA) {
	b := dst.Bounds()
	z := newRaster(b)
	addCircle(z, cx-float64(b.Min.X), cy-float64(b.Min.Y), r, false)
	z.Draw(dst, b, uni(c), image.Point{})
}

func strokeCircle(dst *image.RGBA, cx, cy, r, w float64, c color.RGBA) {
	b := dst.Bounds()
	z := newRaster(b)
	ox, oy := cx-float64(b.Min.X), cy-float64(b.Min.Y)
	addCircle(z, ox, oy, r+w/2, false)
	addCircle(z, ox, oy, math.Max(0, r-w/2), true)
	z.Draw(dst, b, uni(c), image.Point{})
}

func roundRectMask(w, h int, x, y, rw, rh, rad float64) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, w, h))
	z := vector.NewRasterizer(w, h)
	k := 0.5523 * rad
	z.MoveTo(float32(x+rad), float32(y))
	z.LineTo(float32(x+rw-rad), float32(y))
	z.CubeTo(float32(x+rw-rad+k), float32(y), float32(x+rw), float32(y+rad-k), float32(x+rw), float32(y+rad))
	z.LineTo(float32(x+rw), float32(y+rh-rad))
	z.CubeTo(float32(x+rw), float32(y+rh-rad+k), float32(x+rw-rad+k), float32(y+rh), float32(x+rw-rad), float32(y+rh))
	z.LineTo(float32(x+rad), float32(y+rh))
	z.CubeTo(float32(x+rad-k), float32(y+rh), float32(x), float32(y+rh-rad+k), float32(x), float32(y+rh-rad))
	z.LineTo(float32(x), float32(y+rad))
	z.CubeTo(float32(x), float32(y+rad-k), float32(x+rad-k), float32(y), float32(x+rad), float32(y))
	z.ClosePath()
	z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	return m
}

// 마스크 영역을 위치별 색(f)으로 칠하기
func fillMaskFunc(dst *image.RGBA, m *image.Alpha, f func(x, y int) color.RGBA) {
	b := m.Bounds().Intersect(dst.Bounds())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			a := m.AlphaAt(x, y).A
			if a == 0 {
				continue
			}
			c := f(x, y)
			if c.A == 0 {
				continue
			}
			k := uint32(a)
			overPix(dst, x, y, uint8(uint32(c.R)*k/255), uint8(uint32(c.G)*k/255), uint8(uint32(c.B)*k/255), uint8(uint32(c.A)*k/255))
		}
	}
}

// premultiplied src-over 한 픽셀
func overPix(dst *image.RGBA, x, y int, r, g, b, a uint8) {
	i := dst.PixOffset(x, y)
	p := dst.Pix[i : i+4 : i+4]
	ia := 255 - uint32(a)
	p[0] = uint8(uint32(r) + uint32(p[0])*ia/255)
	p[1] = uint8(uint32(g) + uint32(p[1])*ia/255)
	p[2] = uint8(uint32(b) + uint32(p[2])*ia/255)
	p[3] = uint8(uint32(a) + uint32(p[3])*ia/255)
}

// 알파 마스크 흐리게 (그림자용, 상자 흐림 3번 ≈ 가우시안)
func blurAlpha(m *image.Alpha, rad int) {
	if rad < 1 {
		return
	}
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	tmp := make([]uint8, len(m.Pix))
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < h; y++ {
			row := m.Pix[y*m.Stride : y*m.Stride+w]
			sum := 0
			for x := -rad; x <= rad; x++ {
				sum += int(row[clampi(x, 0, w-1)])
			}
			for x := 0; x < w; x++ {
				tmp[y*m.Stride+x] = uint8(sum / (2*rad + 1))
				sum += int(row[clampi(x+rad+1, 0, w-1)]) - int(row[clampi(x-rad, 0, w-1)])
			}
		}
		for x := 0; x < w; x++ {
			sum := 0
			for y := -rad; y <= rad; y++ {
				sum += int(tmp[clampi(y, 0, h-1)*m.Stride+x])
			}
			for y := 0; y < h; y++ {
				m.Pix[y*m.Stride+x] = uint8(sum / (2*rad + 1))
				sum += int(tmp[clampi(y+rad+1, 0, h-1)*m.Stride+x]) - int(tmp[clampi(y-rad, 0, h-1)*m.Stride+x])
			}
		}
	}
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// 정사각형 가운데 부분으로 자르기 (16:9 썸네일 → 정사각형 라벨)
func centerSquare(img image.Image) image.Rectangle {
	b := img.Bounds()
	s := b.Dx()
	if b.Dy() < s {
		s = b.Dy()
	}
	x := b.Min.X + (b.Dx()-s)/2
	y := b.Min.Y + (b.Dy()-s)/2
	return image.Rect(x, y, x+s, y+s)
}

// ── 렌더러 ─────────────────────────────────────────────

type Track struct {
	Key, Title, Artist, Album, Source, Link string
	Art                                     image.Image // nil이면 포인트 컬러 라벨
	Accent                                  color.RGBA  // 불투명 색
}

type Style struct {
	Scale    float64
	BgAlpha  float64 // 카드 배경 진하기 0(투명) ~ 1(기본)
	Glow     float64 // 앨범 색 빛 세기 0 ~ 1.5
	ShowLink bool
}

type pt struct{ x, y int32 }

type Renderer struct {
	fonts *Fonts
	W, H  int
	s     float64
	tr    Track
	st    Style
	ready bool

	static *image.RGBA // 고정 부분 (카드, 글자, 판 바탕/홈, 진행 원 바탕)
	frame  *image.RGBA // 최종 화면

	deck    image.Rectangle // LP판 + 진행 원 영역 (캔버스 좌표)
	rot     *image.RGBA     // 회전하는 내용 (라벨 + 둘레 글자), deck 좌표
	rotPts  []pt            // 회전 샘플링이 필요한 픽셀 (deck 좌표)
	top     *image.RGBA     // 판 위 고정 레이어 (구멍, 라벨 테두리, 빛 반사)
	topPts  []pt
	arc     *image.RGBA // 진행률 원호
	arcPts  []pt
	arcProg float64

	title       *image.RGBA // 제목 한 줄 전체 (흐르는 제목용)
	titleRect   image.Rectangle
	titleOver   float64 // 넘치는 길이(px), 0이면 고정
	eqRect      image.Rectangle
	eqBaseline  float64
	needFull    bool
	titleStatic bool
	fBig, fSm   *textFace // 판 위 안내 글자 (이동 시간)
}

func NewRenderer(f *Fonts) *Renderer { return &Renderer{fonts: f, arcProg: -1} }

func (r *Renderer) px(v float64) float64 { return v * r.s }

// 곡이나 표시 옵션이 바뀌면 고정 레이어를 새로 만듦
func (r *Renderer) SetContent(tr Track, st Style) {
	if st.Scale <= 0.2 {
		st.Scale = 1
	}
	if r.ready && tr == r.tr && st == r.st {
		return
	}
	r.tr, r.st, r.s = tr, st, st.Scale
	r.W, r.H = int(math.Ceil(baseW*r.s)), int(math.Ceil(baseH*r.s))
	r.static = image.NewRGBA(image.Rect(0, 0, r.W, r.H))
	r.frame = image.NewRGBA(image.Rect(0, 0, r.W, r.H))
	r.fBig = r.fonts.face(r.fonts.Bold, r.px(14))
	r.fSm = r.fonts.face(r.fonts.Bold, r.px(10.5))
	r.buildCard()
	r.buildDeck()
	r.arcProg = -1
	r.ready = true
	r.needFull = true
	r.titleStatic = false
}

func (r *Renderer) accent(a float64) color.RGBA {
	c := r.tr.Accent
	if c.A == 0 {
		c = color.RGBA{0xff, 0x4e, 0x45, 0xff}
	}
	return rgba(c.R, c.G, c.B, a)
}

// 그림자 + 글자 (투명 모드에서는 그림자를 진하게)
func (r *Renderer) drawText(dst *image.RGBA, tf *textFace, x, baseline float64, s string, c color.RGBA, spacing float64) {
	if sh := 1 - r.st.BgAlpha; sh > 0.05 { // 배경이 옅을수록 글자 그림자를 진하게
		w := int(tf.measure(s, spacing)) + int(r.px(16))
		h := int(tf.height) + int(r.px(16))
		ox, oy := int(x)-int(r.px(8)), int(baseline-tf.ascent)-int(r.px(8))
		m := image.NewAlpha(image.Rect(0, 0, w, h))
		tf.draw(m, x-float64(ox), baseline-float64(oy), s, image.Opaque, spacing)
		hard := image.NewAlpha(m.Bounds())
		copy(hard.Pix, m.Pix)
		blurAlpha(m, int(math.Max(1, r.px(3))))
		for i := range m.Pix { // 흐린 그림자 + 글자 바로 뒤 진한 그림자
			v := int(m.Pix[i])*2 + int(hard.Pix[i])/2
			if v > 255 {
				v = 255
			}
			m.Pix[i] = uint8(v)
		}
		draw.DrawMask(dst, m.Bounds().Add(image.Pt(ox, oy+int(r.px(1)))), uni(rgba(0, 0, 0, 0.9*sh)), image.Point{}, m, image.Point{}, draw.Over)
	}
	tf.draw(dst, x, baseline, s, uni(c), spacing)
}

func (r *Renderer) buildCard() {
	s := r.s
	cy := deckCY - cardH/2
	bgA := math.Max(0, math.Min(1, r.st.BgAlpha))
	// 판 그림자 (회전과 무관)
	sh := image.NewAlpha(r.static.Bounds())
	z := vector.NewRasterizer(r.W, r.H)
	addCircle(z, r.px(deckCX), r.px(deckCY+6), r.px(discR), false)
	z.Draw(sh, sh.Bounds(), image.Opaque, image.Point{})
	blurAlpha(sh, int(r.px(7)))

	if bgA > 0.01 {
		m := roundRectMask(r.W, r.H, 0.5, r.px(cy)+0.5, r.px(cardW)-1, r.px(cardH)-1, r.px(cardR))
		// 카드 그림자
		cs := image.NewAlpha(m.Bounds())
		copy(cs.Pix, m.Pix)
		blurAlpha(cs, int(r.px(9)))
		draw.DrawMask(r.static, cs.Bounds().Add(image.Pt(0, int(r.px(8)))), uni(rgba(0, 0, 0, 0.35*bgA)), image.Point{}, cs, image.Point{}, draw.Over)
		w, h := r.px(cardW), r.px(cardH)
		y0 := r.px(cy)
		ac := r.tr.Accent
		if ac.A == 0 {
			ac = color.RGBA{0xff, 0x4e, 0x45, 0xff}
		}
		fillMaskFunc(r.static, m, func(x, y int) color.RGBA {
			t := (float64(x)/w + (float64(y)-y0)/h) / 2
			bg := [2][4]float64{{22, 22, 28, 0.92}, {12, 12, 16, 0.86}}
			cr := bg[0][0] + (bg[1][0]-bg[0][0])*t
			cg := bg[0][1] + (bg[1][1]-bg[0][1])*t
			cb := bg[0][2] + (bg[1][2]-bg[0][2])*t
			ca := bg[0][3] + (bg[1][3]-bg[0][3])*t
			// 앨범 색: 카드 전체에 은은하게 + 왼쪽 위에서 퍼지는 빛
			g := (float64(x)/w*0.75 + (float64(y)-y0)/h*0.9) / (0.75*0.75 + 0.9*0.9)
			tint := 0.16 * r.st.Glow
			ga := math.Min(0.9, tint+math.Max(0, math.Min(0.8, 0.55*r.st.Glow)*(1-g/0.85)))
			cr = cr*(1-ga) + float64(ac.R)*0.62*ga
			cg = cg*(1-ga) + float64(ac.G)*0.62*ga
			cb = cb*(1-ga) + float64(ac.B)*0.62*ga
			ca = ca + (1-ca)*ga*0.4
			return rgba(uint8(cr), uint8(cg), uint8(cb), ca*bgA)
		})
		// 테두리
		inner := roundRectMask(r.W, r.H, 1.5, r.px(cy)+1.5, r.px(cardW)-3, r.px(cardH)-3, r.px(cardR)-1)
		for i := range m.Pix {
			v := int(m.Pix[i]) - int(inner.Pix[i])
			if v < 0 {
				v = 0
			}
			m.Pix[i] = uint8(v)
		}
		draw.DrawMask(r.static, m.Bounds(), uni(rgba(255, 255, 255, 0.07*bgA)), image.Point{}, m, image.Point{}, draw.Over)
		if r.st.Glow > 0.05 { // 앨범 색 테두리
			draw.DrawMask(r.static, m.Bounds(), uni(r.accent(math.Min(0.6, 0.32*r.st.Glow)*bgA)), image.Point{}, m, image.Point{}, draw.Over)
		}
	}
	draw.DrawMask(r.static, sh.Bounds(), uni(rgba(0, 0, 0, 0.6)), image.Point{}, sh, image.Point{}, draw.Over)

	// 글자
	fs := r.fonts
	eyebrow := fs.face(fs.Bold, r.px(10))
	titleF := fs.face(fs.Bold, r.px(19))
	artistF := fs.face(fs.Regular, r.px(13))
	linkF := fs.face(fs.Mono, r.px(10.5))
	maxW := r.px(cardW - textRight - textX)
	x0 := r.px(textX)

	lines := 26.0 + 18 + 12 + 4
	if r.st.ShowLink && r.tr.Link != "" {
		lines += 18
	}
	y := r.px(cy + (cardH-lines)/2)

	// 1) NOW PLAYING · 소스 (이퀄라이저 막대는 프레임마다 그림)
	eqW := r.px(13)
	r.eqRect = image.Rect(int(x0)-1, int(y)-1, int(x0+eqW)+2, int(y+r.px(11))+2)
	r.eqBaseline = y + r.px(10)
	spacing := r.px(1.8)
	bx := x0 + eqW + r.px(7)
	label := "NOW PLAYING"
	src := map[string]string{"youtube": "YOUTUBE", "ytmusic": "YOUTUBE MUSIC"}[r.tr.Source]
	white := func(a float64) color.RGBA { return rgba(255, 255, 255, a) }
	ea := 0.55 + 0.30*(1-bgA)
	part := func(t string, a, sp float64) {
		r.drawText(r.static, eyebrow, bx, r.eqBaseline, t, white(a), sp)
		bx += eyebrow.measure(t, sp)
	}
	part(label, ea, spacing)
	if src != "" {
		bx += r.px(6)
		part("·", ea*0.7, 0)
		bx += r.px(6)
		part(src, ea*0.7, spacing*0.8)
	}
	y += r.px(12 + 4)

	// 2) 제목 (넘치면 흐르게)
	tw := titleF.measure(r.tr.Title, 0)
	r.titleRect = image.Rect(int(x0), int(y-r.px(2)), int(x0+maxW), int(y+r.px(28)))
	pad := int(r.px(10))
	r.title = image.NewRGBA(image.Rect(0, 0, int(tw)+pad*2+1, r.titleRect.Dy()))
	r.drawText(r.title, titleF, float64(pad), r.px(2)+titleF.ascent+r.px(1), r.tr.Title, white(1), 0)
	r.titleOver = 0
	if tw > maxW+1 {
		r.titleOver = tw - maxW + r.px(16)
	}
	y += r.px(26)

	// 3) 아티스트 · 앨범
	artist := r.tr.Artist
	if r.tr.Album != "" && r.tr.Album != r.tr.Title {
		if artist != "" {
			artist += " · "
		}
		artist += r.tr.Album
	}
	aa := 0.78 + 0.16*(1-bgA)
	r.drawText(r.static, artistF, x0, y+artistF.ascent, artistF.ellipsize(artist, maxW, 0), white(aa), 0)
	y += r.px(18 + 4)

	// 4) 링크
	if r.st.ShowLink && r.tr.Link != "" {
		linkW := r.px(btnPrevX-btnR-8) - x0 // 오른쪽 아래 버튼 자리 비우기
		r.drawText(r.static, linkF, x0, y+linkF.ascent, linkF.ellipsize(r.tr.Link, linkW, 0), r.accent(1), 0)
	}
	_ = s
}

func (r *Renderer) buildDeck() {
	hs := r.px(deckHalf)
	cx, cy := r.px(deckCX), r.px(deckCY)
	r.deck = image.Rect(int(math.Floor(cx-hs))-1, int(math.Floor(cy-hs))-1, int(math.Ceil(cx+hs))+1, int(math.Ceil(cy+hs))+1).Intersect(r.static.Bounds())
	D := r.deck
	lcx, lcy := cx-float64(D.Min.X), cy-float64(D.Min.Y) // deck 좌표의 중심

	// 고정: 판 바탕, 홈(회전해도 똑같음), 바깥 테두리, 진행 원 바탕
	st := r.static.SubImage(D).(*image.RGBA)
	fillCircle(st, cx, cy, r.px(discR), rgba(17, 17, 20, 1))
	i := 0
	for rr := 76.0; rr > 50; rr -= 2.4 {
		a := 0.11
		if i%2 == 1 {
			a = 0.06
		}
		strokeCircle(st, cx, cy, r.px(rr), math.Max(0.6, r.px(0.6)), rgba(255, 255, 255, a))
		i++
	}
	strokeCircle(st, cx, cy, r.px(discR)-r.px(1.5), r.px(3), rgba(8, 8, 10, 1))
	strokeCircle(st, cx, cy, r.px(progR), r.px(progStroke), rgba(255, 255, 255, 0.08))

	// 회전: 앨범아트 라벨 + 둘레 글자
	r.rot = image.NewRGBA(image.Rect(0, 0, D.Dx(), D.Dy()))
	lr := r.px(labelR)
	if r.tr.Art != nil {
		size := int(math.Ceil(lr * 2))
		sq := image.NewRGBA(image.Rect(0, 0, size, size))
		xdraw.CatmullRom.Scale(sq, sq.Bounds(), r.tr.Art, centerSquare(r.tr.Art), xdraw.Src, nil)
		m := image.NewAlpha(sq.Bounds())
		z := vector.NewRasterizer(size, size)
		addCircle(z, float64(size)/2, float64(size)/2, lr, false)
		z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
		off := image.Pt(int(math.Round(lcx-float64(size)/2)), int(math.Round(lcy-float64(size)/2)))
		draw.DrawMask(r.rot, sq.Bounds().Add(off), sq, image.Point{}, m, image.Point{}, draw.Over)
	} else {
		fillCircle(r.rot, lcx, lcy, lr, r.accent(1))
	}
	r.drawRing(lcx, lcy)

	r.rotPts = r.rotPts[:0]
	for y := 0; y < D.Dy(); y++ {
		for x := 0; x < D.Dx(); x++ {
			d := math.Hypot(float64(x)+0.5-lcx, float64(y)+0.5-lcy)
			if d <= lr+1.5 || (d >= r.px(ringTextR-8) && d <= r.px(ringTextR+8)) {
				r.rotPts = append(r.rotPts, pt{int32(x), int32(y)})
			}
		}
	}

	// 위에 고정: 라벨 테두리, 가운데 구멍, 빛 반사
	r.top = image.NewRGBA(r.rot.Bounds())
	strokeCircle(r.top, lcx, lcy, lr, r.px(2), rgba(0, 0, 0, 0.6))
	fillCircle(r.top, lcx, lcy, r.px(holeR), rgba(13, 13, 16, 1))
	strokeCircle(r.top, lcx, lcy, r.px(holeR), r.px(1.5), rgba(255, 255, 255, 0.25))
	for y := 0; y < D.Dy(); y++ {
		for x := 0; x < D.Dx(); x++ {
			dx, dy := float64(x)+0.5-lcx, float64(y)+0.5-lcy
			d := math.Hypot(dx, dy)
			if d < lr || d > r.px(discR)-1 {
				continue
			}
			ang := math.Mod(math.Atan2(dx, -dy)*180/math.Pi+360, 360) // 위쪽 0도, 시계 방향
			a := sheen(ang)
			if a > 0.003 {
				overPix(r.top, x, y, uint8(255*a), uint8(255*a), uint8(255*a), uint8(255*a))
			}
		}
	}
	r.topPts = nonZero(r.top, r.topPts[:0])
}

// CSS conic-gradient(from 20deg, …)와 같은 빛 반사 모양
func sheen(ang float64) float64 {
	p := math.Mod(ang-20+360, 360) / 360
	tri := func(lo, peak, hi, a float64) float64 {
		switch {
		case p <= lo || p >= hi:
			return 0
		case p < peak:
			return a * (p - lo) / (peak - lo)
		default:
			return a * (hi - p) / (hi - peak)
		}
	}
	return tri(0.08, 0.13, 0.20, 0.09) + tri(0.50, 0.63, 0.70, 0.06)
}

func nonZero(img *image.RGBA, out []pt) []pt {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.Pix[img.PixOffset(x, y)+3] != 0 {
				out = append(out, pt{int32(x), int32(y)})
			}
		}
	}
	return out
}

// 판 둘레를 따라 "제목 • 아티스트 •" 글자 배치
func (r *Renderer) drawRing(cx, cy float64) {
	base := strings.ToUpper(strings.TrimSpace(strings.Join(nonEmpty(r.tr.Title, r.tr.Artist), "  •  ")))
	if base == "" {
		base = "NOW PLAYING"
	}
	wide := false
	for _, c := range base {
		if c >= 0x1100 {
			wide = true
			break
		}
	}
	max, min := 58, 46
	if wide {
		max, min = 40, 32
	}
	if utf8.RuneCountInString(base) > max {
		base = string([]rune(base)[:max-1]) + "…"
	}
	s := base + "  •  "
	for utf8.RuneCountInString(s) < min {
		s += base + "  •  "
	}
	rs := []rune(s)
	n := len(rs)
	tf := r.fonts.face(r.fonts.Bold, r.px(8.5))
	cell := int(math.Ceil(r.px(14)))
	col := uni(rgba(255, 255, 255, 0.63))
	for i, ch := range rs {
		if ch == ' ' {
			continue
		}
		g := image.NewRGBA(image.Rect(0, 0, cell, cell))
		adv := tf.advance(ch)
		tf.draw(g, (float64(cell)-adv)/2, float64(cell)/2+tf.ascent/2-r.px(0.5), string(ch), col, 0)
		th := 2 * math.Pi * float64(i) / float64(n)
		c, sn := math.Cos(th), math.Sin(th)
		hw, hh := float64(cell)/2, float64(cell)/2+r.px(ringTextR)
		// 글자 칸 가운데를 (0,-반지름)에 두고 θ만큼 회전 → 판 중심으로 이동
		m := f64.Aff3{c, -sn, c*(-hw) - sn*(-hh) + cx, sn, c, sn*(-hw) + c*(-hh) + cy}
		xdraw.BiLinear.Transform(r.rot, m, g, g.Bounds(), xdraw.Over, nil)
	}
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}

// 진행률 원호 (값이 조금이라도 바뀔 때만 다시 그림)
func (r *Renderer) buildArc(p float64) {
	D := r.deck
	if r.arc == nil || r.arc.Bounds().Dx() != D.Dx() || r.arc.Bounds().Dy() != D.Dy() {
		r.arc = image.NewRGBA(image.Rect(0, 0, D.Dx(), D.Dy()))
	} else {
		for i := range r.arc.Pix {
			r.arc.Pix[i] = 0
		}
	}
	r.arcProg = p
	r.arcPts = r.arcPts[:0]
	if p <= 0.002 {
		return
	}
	p = math.Min(p, 0.9995)
	cx, cy := r.px(deckCX)-float64(D.Min.X), r.px(deckCY)-float64(D.Min.Y)
	ro, ri := r.px(progR)+r.px(progStroke)/2, r.px(progR)-r.px(progStroke)/2
	z := newRaster(r.arc.Bounds())
	end := p * 2 * math.Pi
	n := int(math.Max(8, end*ro/2))
	for i := 0; i <= n; i++ {
		a := end * float64(i) / float64(n)
		x, y := float32(cx+ro*math.Sin(a)), float32(cy-ro*math.Cos(a))
		if i == 0 {
			z.MoveTo(x, y)
		} else {
			z.LineTo(x, y)
		}
	}
	for i := n; i >= 0; i-- {
		a := end * float64(i) / float64(n)
		z.LineTo(float32(cx+ri*math.Sin(a)), float32(cy-ri*math.Cos(a)))
	}
	z.ClosePath()
	c := r.accent(1)
	z.Draw(r.arc, r.arc.Bounds(), uni(c), image.Point{})
	rc := r.px(progStroke) / 2 // 둥근 끝
	fillCircle(r.arc, cx, cy-r.px(progR), rc, c)
	fillCircle(r.arc, cx+r.px(progR)*math.Sin(end), cy-r.px(progR)*math.Cos(end), rc, c)
	r.arcPts = nonZero(r.arc, r.arcPts)
}

type FrameState struct {
	Angle    float64 // 판 회전 각도(도)
	Progress float64 // 0~1
	T        float64 // 애니메이션 시계(초): 이퀄라이저, 흐르는 제목
	Playing  bool

	Hover, Pressed string  // "prev" | "next" | "disc" | ""
	DragText       string  // 판을 돌리는 중: "2:31 / 4:05"
	DragSub        string  // "+0:24"
	Flash          string  // "play" | "pause" | "" (클릭했을 때 잠깐 표시)
	FlashA         float64 // 0~1
}

// 한 프레임 그리기. 바뀐 영역(캔버스 좌표)을 돌려줌
func (r *Renderer) Frame(fs FrameState) []image.Rectangle {
	if !r.ready {
		return nil
	}
	var dirty []image.Rectangle
	if r.needFull {
		copy(r.frame.Pix, r.static.Pix)
		dirty = append(dirty, r.frame.Bounds())
		r.needFull = false
	}
	if math.Abs(fs.Progress-r.arcProg) > 0.0008 || (fs.Progress <= 0.002) != (r.arcProg <= 0.002) {
		r.buildArc(fs.Progress)
	}
	r.drawDeck(fs.Angle)
	r.drawDeckInfo(fs)
	dirty = append(dirty, r.deck)
	dirty = append(dirty, r.drawButtons(fs))
	dirty = append(dirty, r.drawEq(fs))
	if rr, ok := r.drawTitle(fs); ok {
		dirty = append(dirty, rr)
	}
	return dirty
}

func (r *Renderer) restore(rect image.Rectangle) {
	rect = rect.Intersect(r.frame.Bounds())
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		i := r.frame.PixOffset(rect.Min.X, y)
		j := i + rect.Dx()*4
		copy(r.frame.Pix[i:j], r.static.Pix[i:j])
	}
}

func (r *Renderer) drawDeck(angle float64) {
	D := r.deck
	r.restore(D)
	th := -angle * math.Pi / 180 // 화면 픽셀 → 원래(회전 전) 위치
	c, sn := math.Cos(th), math.Sin(th)
	cx, cy := r.px(deckCX)-float64(D.Min.X), r.px(deckCY)-float64(D.Min.Y)
	src := r.rot
	sw, shh := src.Bounds().Dx(), src.Bounds().Dy()
	for _, p := range r.rotPts {
		dx, dy := float64(p.x)+0.5-cx, float64(p.y)+0.5-cy
		sx := c*dx - sn*dy + cx - 0.5
		sy := sn*dx + c*dy + cy - 0.5
		x0, y0 := int(math.Floor(sx)), int(math.Floor(sy))
		if x0 < 0 || y0 < 0 || x0+1 >= sw || y0+1 >= shh {
			continue
		}
		fx, fy := sx-float64(x0), sy-float64(y0)
		i00 := src.PixOffset(x0, y0)
		i10 := i00 + 4
		i01 := i00 + src.Stride
		i11 := i01 + 4
		if src.Pix[i00+3]|src.Pix[i10+3]|src.Pix[i01+3]|src.Pix[i11+3] == 0 {
			continue
		}
		var o [4]uint8
		for k := 0; k < 4; k++ {
			v := (float64(src.Pix[i00+k])*(1-fx)+float64(src.Pix[i10+k])*fx)*(1-fy) +
				(float64(src.Pix[i01+k])*(1-fx)+float64(src.Pix[i11+k])*fx)*fy
			o[k] = uint8(v + 0.5)
		}
		overPix(r.frame, int(p.x)+D.Min.X, int(p.y)+D.Min.Y, o[0], o[1], o[2], o[3])
	}
	for _, p := range r.topPts {
		i := r.top.PixOffset(int(p.x), int(p.y))
		q := r.top.Pix[i : i+4]
		overPix(r.frame, int(p.x)+D.Min.X, int(p.y)+D.Min.Y, q[0], q[1], q[2], q[3])
	}
	for _, p := range r.arcPts {
		i := r.arc.PixOffset(int(p.x), int(p.y))
		q := r.arc.Pix[i : i+4]
		overPix(r.frame, int(p.x)+D.Min.X, int(p.y)+D.Min.Y, q[0], q[1], q[2], q[3])
	}
}

func (r *Renderer) drawEq(fs FrameState) image.Rectangle {
	R := r.eqRect
	r.restore(R)
	periods := []float64{1.0, 0.8, 1.15}
	phase := []float64{0, 0.35, 0.6}
	x := float64(R.Min.X) + 1
	for i := 0; i < 3; i++ {
		k := 0.3
		if fs.Playing {
			k = 0.25 + 0.75*(0.5-0.5*math.Cos(2*math.Pi*(fs.T/periods[i]+phase[i])))
		}
		h := r.px(10) * k
		bar := image.Rect(int(x), int(r.eqBaseline-h), int(x+r.px(3)), int(r.eqBaseline))
		draw.Draw(r.frame, bar, uni(r.accent(1)), image.Point{}, draw.Over)
		x += r.px(5)
	}
	return R
}

// 제목: 넘치면 왼쪽으로 흘렀다가 돌아옴 (CSS 버전과 같은 타이밍)
func (r *Renderer) drawTitle(fs FrameState) (image.Rectangle, bool) {
	R := r.titleRect
	if r.titleOver <= 0 {
		if r.title != nil && !r.titleDrawnStatic() {
			r.blitTitle(0)
			return R, true
		}
		return R, false
	}
	move := math.Max(2.5, r.titleOver/r.px(30))
	cycle := 3.0 + move*2
	t := math.Mod(fs.T, cycle)
	off := 0.0
	switch {
	case t < 1.5:
	case t < 1.5+move:
		off = r.titleOver * (t - 1.5) / move
	case t < 3+move:
		off = r.titleOver
	default:
		off = r.titleOver * (1 - (t-3-move)/move)
	}
	r.restore(R)
	r.blitTitle(off)
	return R, true
}

func (r *Renderer) titleDrawnStatic() bool { return r.titleStatic }

func (r *Renderer) blitTitle(off float64) {
	R := r.titleRect
	pad := int(r.px(10))
	sp := image.Pt(pad+int(math.Round(off)), 0)
	dst := r.frame.SubImage(R).(*image.RGBA)
	draw.Draw(dst, R, r.title, sp, draw.Over)
	// 흐르는 중엔 왼쪽 여백(그림자) 일부도 보이도록, 고정이면 그대로
	r.titleStatic = r.titleOver <= 0
}

func (r *Renderer) Image() *image.RGBA { return r.frame }

// ── 이전/다음 버튼 · 판 위 안내 ───────────────────────────

func (r *Renderer) btnCenter(which string) (float64, float64) {
	if which == "prev" {
		return r.px(btnPrevX), r.px(btnY)
	}
	return r.px(btnNextX), r.px(btnY)
}

func (r *Renderer) buttonsRect() image.Rectangle {
	pad := btnR + 4
	return image.Rect(int(r.px(btnPrevX-pad)), int(r.px(btnY-pad)), int(math.Ceil(r.px(btnNextX+pad))), int(math.Ceil(r.px(btnY+pad)))).Intersect(r.frame.Bounds())
}

func (r *Renderer) drawButtons(fs FrameState) image.Rectangle {
	R := r.buttonsRect()
	r.restore(R)
	dst := r.frame.SubImage(R).(*image.RGBA)
	for _, which := range []string{"prev", "next"} {
		cx, cy := r.btnCenter(which)
		rad := r.px(btnR)
		if fs.Pressed == which {
			rad *= 0.9
		}
		bg, ring := rgba(18, 18, 22, 0.82), rgba(255, 255, 255, 0.14)
		if fs.Hover == which {
			bg, ring = rgba(34, 34, 42, 0.92), r.accent(0.9)
		}
		fillCircle(dst, cx, cy+r.px(1.5), rad+r.px(1), rgba(0, 0, 0, 0.35)) // 그림자
		fillCircle(dst, cx, cy, rad, bg)
		strokeCircle(dst, cx, cy, rad-r.px(0.5), r.px(1), ring)
		// 아이콘: ⏮ = 막대 + 왼쪽 삼각형, ⏭ = 오른쪽 삼각형 + 막대
		ic := rgba(255, 255, 255, 0.94)
		u := r.px(1)
		z := newRaster(R)
		ox, oy := cx-float64(R.Min.X), cy-float64(R.Min.Y)
		dir := 1.0
		if which == "prev" {
			dir = -1
		}
		tip := ox + dir*4.5*u
		z.MoveTo(float32(tip), float32(oy))
		z.LineTo(float32(ox-dir*3.5*u), float32(oy-5*u))
		z.LineTo(float32(ox-dir*3.5*u), float32(oy+5*u))
		z.ClosePath()
		bx := ox + dir*5*u
		z.MoveTo(float32(bx-0.9*u), float32(oy-5*u))
		z.LineTo(float32(bx+0.9*u), float32(oy-5*u))
		z.LineTo(float32(bx+0.9*u), float32(oy+5*u))
		z.LineTo(float32(bx-0.9*u), float32(oy+5*u))
		z.ClosePath()
		z.Draw(dst, R, uni(ic), image.Point{})
	}
	return R
}

// 판을 돌리는 중엔 이동할 시간, 클릭하면 ▶ / ❚❚ 아이콘을 라벨 위에 표시
func (r *Renderer) drawDeckInfo(fs FrameState) {
	if fs.DragText == "" && (fs.Flash == "" || fs.FlashA <= 0.01) {
		return
	}
	dst := r.frame.SubImage(r.deck).(*image.RGBA)
	cx, cy := r.px(deckCX), r.px(deckCY)
	lr := r.px(labelR)
	if fs.DragText != "" {
		fillCircle(dst, cx, cy, lr, rgba(0, 0, 0, 0.66))
		w := r.fBig.measure(fs.DragText, 0)
		r.fBig.draw(dst, cx-w/2, cy+r.px(1), fs.DragText, uni(rgba(255, 255, 255, 1)), 0)
		if fs.DragSub != "" {
			w2 := r.fSm.measure(fs.DragSub, 0)
			r.fSm.draw(dst, cx-w2/2, cy+r.px(17), fs.DragSub, uni(r.accent(1)), 0)
		}
		return
	}
	a := math.Min(1, fs.FlashA)
	fillCircle(dst, cx, cy, lr*0.62, rgba(0, 0, 0, 0.6*a))
	u := r.px(1)
	z := newRaster(r.deck)
	ox, oy := cx-float64(r.deck.Min.X), cy-float64(r.deck.Min.Y)
	if fs.Flash == "play" {
		z.MoveTo(float32(ox-5*u), float32(oy-8*u))
		z.LineTo(float32(ox+9*u), float32(oy))
		z.LineTo(float32(ox-5*u), float32(oy+8*u))
		z.ClosePath()
	} else {
		for _, bx := range []float64{-5, 2} {
			z.MoveTo(float32(ox+bx*u), float32(oy-8*u))
			z.LineTo(float32(ox+(bx+3.5)*u), float32(oy-8*u))
			z.LineTo(float32(ox+(bx+3.5)*u), float32(oy+8*u))
			z.LineTo(float32(ox+bx*u), float32(oy+8*u))
			z.ClosePath()
		}
	}
	z.Draw(dst, r.deck, uni(rgba(255, 255, 255, a)), image.Point{})
}

// 마우스가 올라간 곳: "disc" | "prev" | "next" | "" (캔버스 픽셀 좌표)
func (r *Renderer) HitTest(x, y float64) string {
	if !r.ready {
		return ""
	}
	for _, which := range []string{"prev", "next"} {
		bx, by := r.btnCenter(which)
		if math.Hypot(x-bx, y-by) <= r.px(btnR+3) {
			return which
		}
	}
	if math.Hypot(x-r.px(deckCX), y-r.px(deckCY)) <= r.px(progR+progStroke+2) {
		return "disc"
	}
	return ""
}

// 판 중심 (캔버스 픽셀)
func (r *Renderer) DiscCenter() (float64, float64) { return r.px(deckCX), r.px(deckCY) }

// 판 가운데 구멍 근처 반지름 (각도 계산을 멈추는 범위)
func (r *Renderer) DeadZone() float64 { return r.px(16) }
