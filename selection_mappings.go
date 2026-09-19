package main

import (
	"strings"

	"github.com/yude/anime-renamer/internal/normalize"
	"github.com/yude/anime-renamer/internal/parser"
)

type selectionEpisodeKey struct {
	title           string
	selectionNumber int
	recordedDate    string
}

type selectionEpisodeTarget struct {
	workTitle     string
	episodeNumber int
	subtitle      string
}

// verifiedSelectionEpisodes records historical selection broadcasts whose
// selection ordinal is unrelated to the underlying story episode number.
// Each row was verified against Shoboi Calendar's TID, Count, STSubTitle, and
// start date. Annict still has to independently match the target work, number,
// and subtitle before a rename can reach the confidence threshold.
var verifiedSelectionEpisodes = map[selectionEpisodeKey]selectionEpisodeTarget{
	// SAO SELECTION: Shoboi TIDs 2588, 3416, 5049, and 5603.
	selectionKey("SAO SELECTION", 1, "2022-07-10"):  {"ソードアート・オンラインII", 12, "幻の銃弾"},
	selectionKey("SAO SELECTION", 2, "2022-07-17"):  {"ソードアート・オンライン アリシゼーション War of Underworld ‐THE LAST SEASON‐", 18, "記憶"},
	selectionKey("SAO SELECTION", 3, "2022-07-24"):  {"ソードアート・オンライン", 8, "黒と白の剣舞"},
	selectionKey("SAO SELECTION", 4, "2022-07-31"):  {"ソードアート・オンライン", 11, "朝露の少女"},
	selectionKey("SAO SELECTION", 5, "2022-08-07"):  {"ソードアート・オンライン アリシゼーション War of Underworld ‐THE LAST SEASON‐", 20, "夜空の剣"},
	selectionKey("SAO SELECTION", 6, "2022-08-14"):  {"ソードアート・オンライン", 14, "世界の終焉"},
	selectionKey("SAO SELECTION", 7, "2022-08-21"):  {"ソードアート・オンライン アリシゼーション War of Underworld ‐THE LAST SEASON‐", 19, "覚醒"},
	selectionKey("SAO SELECTION", 8, "2022-08-28"):  {"ソードアート・オンライン", 9, "青眼の悪魔"},
	selectionKey("SAO SELECTION", 9, "2022-09-04"):  {"ソードアート・オンライン アリシゼーション", 24, "ぼくの英雄"},
	selectionKey("SAO SELECTION", 10, "2022-09-11"): {"ソードアート・オンラインII", 24, "マザーズ・ロザリオ"},
	selectionKey("SAO SELECTION", 11, "2022-09-18"): {"ソードアート・オンライン", 1, "剣の世界"},
	selectionKey("SAO SELECTION", 12, "2022-09-25"): {"ソードアート・オンライン", 2, "ビーター"},

	// かぐや様は告らせたい シリーズセレクション: Shoboi TIDs 5138,
	// 5596, and 6320.
	selectionKey("かぐや様は告らせたい シリーズセレクション", 1, "2022-07-14"):  {"かぐや様は告らせたい〜天才たちの恋愛頭脳戦〜", 1, "映画に誘わせたい／かぐや様は止められたい／かぐや様はいただきたい"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 2, "2022-07-21"):  {"かぐや様は告らせたい〜天才たちの恋愛頭脳戦〜", 6, "石上優は生き延びたい／藤原千花はテストしたい／かぐや様は気づかれたい"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 3, "2022-07-28"):  {"かぐや様は告らせたい〜天才たちの恋愛頭脳戦〜", 11, "早坂愛は浸かりたい／藤原千花は超食べたい／白銀御行は出会いたい／花火の音は聞こえない 前編"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 4, "2022-08-04"):  {"かぐや様は告らせたい〜天才たちの恋愛頭脳戦〜", 12, "花火の音は聞こえない 後編／かぐや様は避けたくない"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 5, "2022-08-11"):  {"かぐや様は告らせたい？～天才たちの恋愛頭脳戦～", 3, "白銀御行は見上げたい／第67期生徒会／かぐや様は呼びたくない"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 6, "2022-08-18"):  {"かぐや様は告らせたい？～天才たちの恋愛頭脳戦～", 6, "伊井野ミコを笑わせない／伊井野ミコを笑わせたい／かぐや様は呼ばれない"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 7, "2022-08-25"):  {"かぐや様は告らせたい？～天才たちの恋愛頭脳戦～", 11, "そして石上優は目を閉じた③／白銀御行と石上優／大友京子は気づかない"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 8, "2022-09-01"):  {"かぐや様は告らせたい？～天才たちの恋愛頭脳戦～", 12, "生徒会は撮られたい／生徒会は撮らせたい／藤原千花は膨らませたい"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 9, "2022-09-08"):  {"かぐや様は告らせたい-ウルトラロマンティック-", 4, "四宮かぐやの無理難題「燕の子安貝」編①／石上優はこたえたい／藤原千花は泊まりたい"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 10, "2022-09-15"): {"かぐや様は告らせたい-ウルトラロマンティック-", 6, "生徒会は進みたい／白銀御行は告らせたい②／白銀御行は告らせたい③"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 11, "2022-09-22"): {"かぐや様は告らせたい-ウルトラロマンティック-", 12, "かぐや様は告りたい②／かぐや様は告りたい③／「二つの告白」前編"},
	selectionKey("かぐや様は告らせたい シリーズセレクション", 12, "2022-09-29"): {"かぐや様は告らせたい-ウルトラロマンティック-", 13, "「二つの告白」後編／秀知院は後夜祭"},
}

func selectionKey(title string, number int, date string) selectionEpisodeKey {
	return selectionEpisodeKey{
		title:           normalize.Normalize(strings.TrimSpace(title)),
		selectionNumber: number,
		recordedDate:    date,
	}
}

func mapVerifiedSelectionEpisode(meta *parser.RecordingMetadata) (*parser.RecordingMetadata, bool) {
	if meta == nil || meta.EpisodeNumber <= 0 || meta.RecordedDate.IsZero() {
		return meta, false
	}
	key := selectionKey(meta.WorkTitle, meta.EpisodeNumber, meta.RecordedDate.Format("2006-01-02"))
	target, ok := verifiedSelectionEpisodes[key]
	if !ok {
		return meta, false
	}
	mapped := *meta
	mapped.WorkTitle = target.workTitle
	mapped.EpisodeNumber = target.episodeNumber
	mapped.Subtitle = target.subtitle
	return &mapped, true
}
