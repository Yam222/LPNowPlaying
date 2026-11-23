package main

import (
	"regexp"
	"strings"
)

// 일반 YouTube 영상 제목에서 곡 제목/아티스트 뽑기
//
//	"[MV] IU(아이유) _ Love wins all"            → IU(아이유) / Love wins all
//	"BTS (방탄소년단) 'Dynamite' Official MV"     → BTS (방탄소년단) / Dynamite
//	"Rick Astley - Never Gonna Give You Up (Official Music Video)" → Rick Astley / Never Gonna Give You Up
//	채널 "aespa - Topic"                          → 아티스트 aespa
var (
	reBracket  = regexp.MustCompile(`\s*[\[(（【「『〈<［〔｛]([^\])）】」』〉>］〕｝]*)[\])）】」』〉>］〕｝]`)
	reTagWords = regexp.MustCompile(`(?i)\b(official|music\s*video|video|m\s*/?\s*v|audio|lyrics?|가사|visuali[sz]er|performance|live|4k|hd|hq|color\s*coded|eng\s*sub|special\s*clip|teaser|clip|ver\.?|version|자막|한글)\b|^\s*(mv|m/v)\s*$`)
	reTailTags = regexp.MustCompile(`(?i)\s*(official\s*(music\s*)?(video|audio|mv|m/v)|official|m\s*/\s*v|\bmv\b|lyric\s*video|lyrics?|audio)\s*$`)
	reSplit    = regexp.MustCompile(`^(.+?)\s+[-–—_|]\s+(.+)$`)
	reQuoted   = regexp.MustCompile(`^(.+?)\s*['‘"“「『](.+?)['’"”」』]\s*(.*)$`)
)

func CleanYouTube(title, channel string) (string, string) {
	artist := strings.TrimSpace(channel)
	artist = strings.TrimSuffix(artist, " - Topic")
	artist = strings.TrimSpace(strings.TrimSuffix(artist, "VEVO"))

	t := strings.TrimSpace(title)
	// 괄호 안이 "Official MV" 같은 꼬리표면 괄호째 지움
	t = reBracket.ReplaceAllStringFunc(t, func(m string) string {
		inner := reBracket.FindStringSubmatch(m)[1]
		if reTagWords.MatchString(inner) {
			return ""
		}
		return m
	})
	t = strings.TrimSpace(t)

	if m := reSplit.FindStringSubmatch(t); m != nil {
		a, b := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		if a != "" && b != "" {
			artist, t = a, b
		}
	} else if m := reQuoted.FindStringSubmatch(t); m != nil && strings.TrimSpace(m[1]) != "" {
		artist, t = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	}
	for i := 0; i < 3; i++ {
		t = strings.TrimSpace(reTailTags.ReplaceAllString(t, ""))
	}
	t = strings.Trim(t, `'"‘’“” `)
	if t == "" {
		t = strings.TrimSpace(title)
	}
	return t, artist
}
