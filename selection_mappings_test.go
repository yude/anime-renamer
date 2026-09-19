package main

import (
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/parser"
)

func TestMapVerifiedSelectionEpisode(t *testing.T) {
	date := func(value string) time.Time {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	for _, tt := range []struct {
		title     string
		selection int
		date      string
		work      string
		episode   int
		subtitle  string
	}{
		{"SAO SELECTION", 1, "2022-07-10", "ソードアート・オンラインII", 12, "幻の銃弾"},
		{"SAO SELECTION", 5, "2022-08-07", "ソードアート・オンライン アリシゼーション War of Underworld ‐THE LAST SEASON‐", 20, "夜空の剣"},
		{"SAO SELECTION", 12, "2022-09-25", "ソードアート・オンライン", 2, "ビーター"},
		{"かぐや様は告らせたい シリーズセレクション", 1, "2022-07-14", "かぐや様は告らせたい〜天才たちの恋愛頭脳戦〜", 1, "映画に誘わせたい／かぐや様は止められたい／かぐや様はいただきたい"},
		{"かぐや様は告らせたい シリーズセレクション", 8, "2022-09-01", "かぐや様は告らせたい？～天才たちの恋愛頭脳戦～", 12, "生徒会は撮られたい／生徒会は撮らせたい／藤原千花は膨らませたい"},
		{"かぐや様は告らせたい シリーズセレクション", 12, "2022-09-29", "かぐや様は告らせたい-ウルトラロマンティック-", 13, "「二つの告白」後編／秀知院は後夜祭"},
		{"ゆるゆり せれくしょん", 1, "2022-08-23", "ゆるゆり", 12, "みんなでポカポカ合宿へ"},
		{"ゆるゆり せれくしょん", 2, "2022-08-23", "ゆるゆり さん☆ハイ！", 2, "さぁおびえるがいい"},
		{"ゆるゆり せれくしょん", 9, "2022-09-20", "ゆるゆり♪♪", 11, "時をかけるあかり"},
		{"ゆるゆり せれくしょん", 12, "2022-09-27", "ゆるゆり", 5, "あかりとかミンミンゼミとかなく頃に"},
	} {
		meta := &parser.RecordingMetadata{WorkTitle: tt.title, EpisodeNumber: tt.selection, RecordedDate: date(tt.date)}
		mapped, ok := mapVerifiedSelectionEpisode(meta)
		if !ok {
			t.Fatalf("mapVerifiedSelectionEpisode(%+v) did not match", meta)
		}
		if mapped.WorkTitle != tt.work || mapped.EpisodeNumber != tt.episode || mapped.Subtitle != tt.subtitle {
			t.Errorf("mapVerifiedSelectionEpisode(%+v) = %+v, want work=%q episode=%d subtitle=%q", meta, mapped, tt.work, tt.episode, tt.subtitle)
		}
		if meta.WorkTitle != tt.title || meta.EpisodeNumber != tt.selection || meta.Subtitle != "" {
			t.Errorf("mapVerifiedSelectionEpisode mutated input: %+v", meta)
		}
	}
}

func TestMapVerifiedSelectionEpisodeRequiresExactIdentity(t *testing.T) {
	date, err := time.Parse("2006-01-02", "2022-07-10")
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range []*parser.RecordingMetadata{
		{WorkTitle: "SAO SELECTION", EpisodeNumber: 1, RecordedDate: date.AddDate(0, 0, 1)},
		{WorkTitle: "SAO SELECTION", EpisodeNumber: 2, RecordedDate: date},
		{WorkTitle: "SAO SELECTION 特別版", EpisodeNumber: 1, RecordedDate: date},
		{WorkTitle: "別のセレクション", EpisodeNumber: 1, RecordedDate: date},
	} {
		if mapped, ok := mapVerifiedSelectionEpisode(meta); ok || mapped != meta {
			t.Errorf("mapVerifiedSelectionEpisode(%+v) = %+v, %v; want unchanged rejection", meta, mapped, ok)
		}
	}
}
