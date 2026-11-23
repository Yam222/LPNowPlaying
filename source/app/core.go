package main

import (
	"crypto/sha256"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// 크롬 확장 프로그램이 보내 주는 YouTube Music 탭 한 개의 상태
type TabState struct {
	TabID    int     `json:"tabId"`
	WindowID int     `json:"windowId"`
	Index    int     `json:"index"`
	Audible  bool    `json:"audible"`
	Playing  bool    `json:"playing"`
	IsAd     bool    `json:"isAd"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album"`
	Artwork  string  `json:"artwork"`
	URL      string  `json:"url"`
	VideoID  string  `json:"videoId"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	TS       int64   `json:"ts"` // 확장이 상태를 읽은 시각 (ms)

	Source   string `json:"source,omitempty"`   // ytmusic(기본) | youtube
	PV       string `json:"pv,omitempty"`       // 탭 안 스크립트 버전
	Ack      int64  `json:"ack,omitempty"`      // 탭이 마지막으로 실행한 조작 명령 번호
	LinkText string `json:"linkText,omitempty"` // 화면에 보여 줄 링크 글자 (비어 있으면 URL에서 만듦)
}

type TabsPayload struct {
	Tabs      []TabState `json:"tabs"`
	Extension string     `json:"ext"`
}

// 오버레이/위젯이 받아 가는 응답
type NowPlaying struct {
	Running   bool    `json:"running"`   // 프로그램이 "시작" 상태인지
	Enabled   bool    `json:"enabled"`   // 요청한 출력(OBS/위젯)이 켜져 있는지
	Connected bool    `json:"connected"` // 확장 프로그램에서 최근에 데이터가 왔는지
	HasTrack  bool    `json:"hasTrack"`
	Playing   bool    `json:"playing"`
	IsAd      bool    `json:"isAd"`
	Title     string  `json:"title"`
	Artist    string  `json:"artist"`
	Album     string  `json:"album"`
	Artwork   string  `json:"artwork"`
	URL       string  `json:"url"`
	LinkText  string  `json:"linkText"`
	VideoID   string  `json:"videoId"`
	Key       string  `json:"key"`
	Position  float64 `json:"position"`
	Duration  float64 `json:"duration"`
	TabCount  int     `json:"tabCount"`
	TabID     int     `json:"tabId"`   // 표시 중인 탭 (조작 명령 대상)
	PageVer   string  `json:"pageVer"` // 그 탭 안 스크립트 버전 (예전이면 탭 새로고침 필요)
	ShowLink  bool    `json:"showLink"`

	Accent      string  `json:"accent"`      // 앨범아트 대표 색 (#rrggbb, 없으면 "")
	Transparent bool    `json:"transparent"` // 카드 배경이 거의 투명 (글자 그림자 사용)
	BgAlpha     float64 `json:"bgAlpha"`     // 카드 배경 진하기 0~1 (1 = 기본 카드)
	Glow        float64 `json:"glow"`        // 앨범 색 빛 세기 0~1.5
	SpinSeconds float64 `json:"spinSeconds"` // LP판 한 바퀴 도는 데 걸리는 시간(초)
	Build       string  `json:"build"`       // 프로그램 버전 (OBS 화면 자동 새로고침용)
	Source      string  `json:"source"`      // ytmusic | youtube
}

const (
	RulePlayingFirst = "playingFirst" // 재생 중인 탭 우선, 없으면 첫 번째 탭
	RuleFirstTab     = "firstTab"     // 항상 첫 번째 탭
	staleAfter       = 6 * time.Second
)

type Store struct {
	mu         sync.Mutex
	tabs       []TabState
	receivedAt time.Time
	extVersion string
	now        func() time.Time
	sources    map[string]sourceEntry // 확장 프로그램 외의 음악 소스 (예비)
}

type sourceEntry struct {
	tab   TabState
	found bool
	at    time.Time
}

func NewStore() *Store { return &Store{now: time.Now, sources: map[string]sourceEntry{}} }

// 확장 프로그램 외 소스(Spotify 등)의 현재 상태 갱신. found=false면 "실행 중이지만 재생 중인 곡 없음"
func (s *Store) SetSource(name string, t TabState, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Source = name
	s.sources[name] = sourceEntry{tab: t, found: found, at: s.now()}
}

func (s *Store) ClearSource(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sources, name)
}

func (s *Store) Update(p TabsPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tabs = append([]TabState(nil), p.Tabs...)
	s.receivedAt = s.now()
	s.extVersion = p.Extension
}

func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tabs = nil
	s.receivedAt = time.Time{}
	s.sources = map[string]sourceEntry{}
}

func (s *Store) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.receivedAt.IsZero() && s.now().Sub(s.receivedAt) < staleAfter
}

// 탭들이 실행했다고 알려 온 명령 번호 중 가장 큰 값
func (s *Store) MaxAck() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var m int64
	for _, t := range s.tabs {
		if t.Ack > m {
			m = t.Ack
		}
	}
	return m
}

func (s *Store) ExtVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.receivedAt.IsZero() || s.now().Sub(s.receivedAt) >= staleAfter {
		return ""
	}
	return s.extVersion
}

func (s *Store) LastReceived() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.receivedAt
}

// 정렬 기준: 먼저 연 창(windowId 작은 순) → 창 안에서 왼쪽 탭(index 작은 순)
func orderedTabs(tabs []TabState) []TabState {
	out := append([]TabState(nil), tabs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].WindowID != out[j].WindowID {
			return out[i].WindowID < out[j].WindowID
		}
		return out[i].Index < out[j].Index
	})
	return out
}

func SelectTab(tabs []TabState, rule string) (TabState, bool) {
	if len(tabs) == 0 {
		return TabState{}, false
	}
	ordered := orderedTabs(tabs)
	if rule != RuleFirstTab {
		for _, t := range ordered {
			if t.Playing {
				return t, true
			}
		}
		for _, t := range ordered {
			if t.Audible {
				return t, true
			}
		}
	}
	return ordered[0], true
}

func (s *Store) Now(rule string) NowPlaying {
	s.mu.Lock()
	now := s.now()
	var tabs []TabState
	extFresh := !s.receivedAt.IsZero() && now.Sub(s.receivedAt) < staleAfter
	if extFresh {
		for _, t := range s.tabs {
			if strings.TrimSpace(t.Title) != "" || t.VideoID != "" { // 영상이 없는 탭(유튜브 홈 화면 등)은 제외
				tabs = append(tabs, t)
			}
		}
	}
	srcFresh := false
	for _, e := range s.sources {
		if now.Sub(e.at) < staleAfter {
			srcFresh = true
			if e.found {
				t := e.tab
				t.WindowID = 1 << 30 // 정렬 시 브라우저 탭들 뒤에 오도록
				tabs = append(tabs, t)
			}
		}
	}
	s.mu.Unlock()

	np := NowPlaying{TabCount: len(tabs)}
	np.Connected = extFresh || srcFresh
	if !np.Connected || len(tabs) == 0 {
		return np
	}
	t, ok := SelectTab(tabs, rule)
	if !ok {
		return np
	}
	np.Playing = t.Playing
	np.TabID = t.TabID
	np.PageVer = t.PV
	np.IsAd = t.IsAd
	if t.IsAd || strings.TrimSpace(t.Title) == "" {
		return np
	}
	np.HasTrack = true
	np.Title = cleanText(t.Title)
	np.Artist = cleanText(t.Artist)
	np.Album = cleanText(t.Album)
	np.Artwork = t.Artwork
	np.VideoID = t.VideoID
	np.URL = t.URL
	if t.Source == "youtube" {
		// 일반 YouTube: 영상 제목에서 곡/아티스트 정리, 썸네일은 여백 없는 16:9 사용, 짧은 링크
		np.Title, np.Artist = CleanYouTube(np.Title, np.Artist)
		np.Album = ""
		if t.VideoID != "" {
			np.Artwork = "https://i.ytimg.com/vi/" + t.VideoID + "/mqdefault.jpg"
			np.URL = "https://www.youtube.com/watch?v=" + t.VideoID
			t.LinkText = "youtu.be/" + t.VideoID
		}
	}
	if np.URL == "" && t.VideoID != "" {
		np.URL = "https://music.youtube.com/watch?v=" + t.VideoID
	}
	np.LinkText = t.LinkText
	if np.LinkText == "" {
		np.LinkText = strings.TrimPrefix(strings.TrimPrefix(np.URL, "https://"), "http://")
	}
	np.Source = t.Source
	if np.Source == "" {
		np.Source = "ytmusic"
	}
	np.Duration = t.Duration
	np.Position = t.Position
	if t.Playing && t.TS > 0 {
		// 확장이 읽은 뒤 지난 시간만큼 재생 위치 보정
		el := float64(now.UnixMilli()-t.TS) / 1000
		if el > 0 && el < 10 {
			np.Position += el
		}
	}
	if np.Duration > 0 && np.Position > np.Duration {
		np.Position = np.Duration
	}
	np.Key = t.VideoID
	if np.Key == "" {
		np.Key = np.Title + "|" + np.Artist
	}
	return np
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// MSIX 패키지 패밀리 이름(PFN) 계산: Name + "_" + PublisherId
// PublisherId = SHA-256(UTF-16LE(publisher)) 앞 8바이트를 Crockford 계열 base32(13자)로 인코딩
func PackageFamilyName(name, publisher string) string {
	u := utf16.Encode([]rune(publisher))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[i*2] = byte(c)
		b[i*2+1] = byte(c >> 8)
	}
	h := sha256.Sum256(b)
	var bits strings.Builder
	for _, x := range h[:8] {
		for i := 7; i >= 0; i-- {
			if x&(1<<uint(i)) != 0 {
				bits.WriteByte('1')
			} else {
				bits.WriteByte('0')
			}
		}
	}
	bits.WriteByte('0') // 65비트로 맞춤
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	bs := bits.String()
	var id strings.Builder
	for i := 0; i < 65; i += 5 {
		v := 0
		for _, c := range bs[i : i+5] {
			v = v*2 + int(c-'0')
		}
		id.WriteByte(alphabet[v])
	}
	return name + "_" + id.String()
}
