package main

import "testing"

func TestCleanYouTube(t *testing.T) {
	cases := []struct{ title, ch, wantT, wantA string }{
		{"[MV] IU(아이유) _ Love wins all", "1theK (원더케이)", "Love wins all", "IU(아이유)"},
		{"BTS (방탄소년단) 'Dynamite' Official MV", "HYBE LABELS", "Dynamite", "BTS (방탄소년단)"},
		{"Rick Astley - Never Gonna Give You Up (Official Music Video)", "Rick Astley", "Never Gonna Give You Up", "Rick Astley"},
		{"Supernova", "aespa - Topic", "Supernova", "aespa"},
		{"NewJeans (뉴진스) 'Hype Boy' Official MV (Performance ver.1)", "HYBE LABELS", "Hype Boy", "NewJeans (뉴진스)"},
		{"lofi hip hop radio 📚 beats to relax/study to", "Lofi Girl", "lofi hip hop radio 📚 beats to relax/study to", "Lofi Girl"},
		{"아이유 - 밤편지 (가사/Lyrics)", "어떤채널", "밤편지", "아이유"},
		{"Ed Sheeran - Shape of You [Official Video]", "Ed Sheeran", "Shape of You", "Ed Sheeran"},
		{"[MV] 볼빨간사춘기(BOL4) _ 우주를 줄게(Galaxy)", "1theK (원더케이)", "우주를 줄게(Galaxy)", "볼빨간사춘기(BOL4)"},
		{"Official髭男dism - Pretender［Official Video］", "Official髭男dism", "Pretender", "Official髭男dism"},
		{"YOASOBI「アイドル」 Official Music Video", "Ayase / YOASOBI", "アイドル", "YOASOBI"},
	}
	for _, c := range cases {
		gt, ga := CleanYouTube(c.title, c.ch)
		if gt != c.wantT || ga != c.wantA {
			t.Errorf("%q / %q → %q / %q (want %q / %q)", c.title, c.ch, gt, ga, c.wantT, c.wantA)
		}
	}
}

func TestYouTubeTabInStore(t *testing.T) {
	s := NewStore()
	s.Update(TabsPayload{Tabs: []TabState{
		{TabID: 1, WindowID: 1, Index: 0, Source: "youtube"}, // 유튜브 홈 (영상 없음)
		{TabID: 2, WindowID: 1, Index: 1, Source: "youtube", Title: "[MV] IU(아이유) _ Love wins all", Artist: "1theK (원더케이)", VideoID: "JleoAppaxi0", Playing: true},
	}})
	np := s.Now(RuleFirstTab)
	if !np.HasTrack || np.Title != "Love wins all" || np.Artist != "IU(아이유)" || np.LinkText != "youtu.be/JleoAppaxi0" ||
		np.Artwork != "https://i.ytimg.com/vi/JleoAppaxi0/mqdefault.jpg" || np.Source != "youtube" {
		t.Fatalf("%+v", np)
	}
}
