package parser

import (
	"errors"
	"testing"
)

func TestWorkSpecificEPGLabels(t *testing.T) {
	for _, tt := range []struct {
		name, title, subtitle string
		number                int
		final                 bool
	}{
		{"[字]アニメA・メダリスト score06「初戦の夜」 (20261108).mp4", "メダリスト", "初戦の夜", 6, false},
		{"メダリスト score ６「初戦の夜」.mp4", "メダリスト", "初戦の夜", 6, false},
		{"[字]アニメ 夜桜さんちの大作戦 第2期 作戦41 金級会議 (20261028).mp4", "夜桜さんちの大作戦 第2期", "金級会議", 41, false},
		{"[新][字]アニメ 夜桜さんちの大作戦 第2期 作戦40 ファミレス父子会談 (20261021).mp4", "夜桜さんちの大作戦 第2期", "ファミレス父子会談", 40, false},
		{"夜桜さんちの大作戦 第2期 第2クール 作戦４２「狙い」.mp4", "夜桜さんちの大作戦 第2期 第2クール", "狙い", 42, false},
		{"夜桜さんちの大作戦 作戦40 父と「秘密」の会談.mp4", "夜桜さんちの大作戦", "父と「秘密」の会談", 40, false},
		{"アニメA・ワールド イズ ダンシング[終] 留〆「能」 (20260925).mp4", "ワールド イズ ダンシング", "能", 0, true},
		{"ワールド イズ ダンシング 留〆「能」.mp4", "ワールド イズ ダンシング 留〆", "能", 0, false},
		{"別作品 留〆[終]「能」.mp4", "別作品 留〆", "能", 0, true},
		{"別作品 score06「初戦の夜」.mp4", "別作品 score06", "初戦の夜", 0, false},
		{"別作品 作戦41 金級会議.mp4", "別作品 作戦41 金級会議", "", 0, false},
		{"死亡遊戯で飯を食う。44：CLOUDY BEACH (20260928).mp4", "死亡遊戯で飯を食う。44：CLOUDY BEACH", "", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFilename(tt.name)
			if err != nil || got.WorkTitle != tt.title || got.Subtitle != tt.subtitle || got.EpisodeNumber != tt.number || got.FinalEpisode != tt.final {
				t.Fatalf("ParseFilename() = %+v, %v; want title=%q subtitle=%q number=%d final=%v", got, err, tt.title, tt.subtitle, tt.number, tt.final)
			}
		})
	}
}

func TestWorkSpecificLabelsRejectNonSingleEpisodes(t *testing.T) {
	for _, name := range []string{
		"メダリスト score06.5「総集編」.mp4",
		"メダリスト score06，07.mp4",
		"メダリスト score06～score07.mp4",
		"メダリスト score06「前半」 score07「後半」.mp4",
		"メダリスト score06「前半」／#07「後半」.mp4",
		"メダリスト score06「前半」／第7話「後半」.mp4",
		"夜桜さんちの大作戦 作戦40．5.mp4",
		"夜桜さんちの大作戦 作戦40／41.mp4",
		"夜桜さんちの大作戦 作戦40 作戦41.mp4",
		"夜桜さんちの大作戦 作戦40 #41.mp4",
	} {
		if _, err := ParseFilename(name); !errors.Is(err, ErrAmbiguousEpisode) {
			t.Errorf("ParseFilename(%q) = %v, want ambiguous episode", name, err)
		}
	}
	for _, name := range []string{"メダリスト score00.mp4", "夜桜さんちの大作戦 作戦0.mp4"} {
		if _, err := ParseFilename(name); !errors.Is(err, ErrUnsupportedEpisode) {
			t.Errorf("ParseFilename(%q) = %v, want unsupported episode", name, err)
		}
	}
}

func TestDatedNumberlessSelections(t *testing.T) {
	for _, tt := range []struct{ name, title string }{
		{"[新]「きみが死ぬまで恋をしたい」セレクション放送 (20261108).mp4", "きみが死ぬまで恋をしたい"},
		{"「私を喰べたい、ひとでなし」セレクション放送 (20261011).mp4", "私を喰べたい、ひとでなし"},
		{"魔法少女リリカルなのは EXCEEDS Gun Blaze Vengeance セレクション再放送 (20260927).mp4", "魔法少女リリカルなのは EXCEEDS Gun Blaze Vengeance"},
		{"「作品」セレクション放送.mp4", "「作品」セレクション放送"},
		{"作品 セレクション放送 #1 (20261108).mp4", "作品 セレクション放送"},
		{"作品 セレクション放送「字幕」 (20261108).mp4", "作品 セレクション放送"},
		{"作品 セレクション放送 特番 (20261108).mp4", "作品 セレクション放送 特番"},
	} {
		got, err := ParseFilename(tt.name)
		if err != nil || got.WorkTitle != tt.title {
			t.Errorf("ParseFilename(%q) = %+v, %v; want title %q", tt.name, got, err, tt.title)
		}
	}
}
