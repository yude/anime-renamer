package parser

import (
	"testing"
	"time"
)

func TestKanjiToInt(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  int
		ok    bool
	}{
		{input: "十七", want: 17, ok: true},
		{input: "百二十三", want: 123, ok: true},
		{input: "一〇〇", want: 100, ok: true},
		{input: "二千二十四", want: 2024, ok: true},
		{input: "十百", ok: false},
		{input: "二三十", ok: false},
		{input: "", ok: false},
	} {
		got, ok := kanjiToInt(tt.input)
		if got != tt.want || ok != tt.ok {
			t.Errorf("kanjiToInt(%q) = %d, %v; want %d, %v", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseFilename(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)

	tests := []struct {
		name      string
		input     string
		wantTitle string
		wantEp    int
		wantSub   string
		wantDate  time.Time
		wantErr   bool
	}{
		// === Standard formats ===
		{
			name:      "standard format with full-width period",
			input:     "花ざかりの君たちへ 第2期 ep．7「ずっとそばにいたいから」 (20260813).mp4",
			wantTitle: "花ざかりの君たちへ 第2期",
			wantEp:    7,
			wantSub:   "ずっとそばにいたいから",
			wantDate:  time.Date(2026, 8, 13, 0, 0, 0, 0, jst),
		},
		{
			name:      "half-width period",
			input:     "作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
			wantDate:  time.Date(2026, 8, 1, 0, 0, 0, 0, jst),
		},
		{
			name:      "EP uppercase",
			input:     "作品 EP.10「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    10,
			wantSub:   "タイトル",
			wantDate:  time.Date(2026, 8, 1, 0, 0, 0, 0, jst),
		},
		{
			name:      "hash notation",
			input:     "作品 #3「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    3,
			wantSub:   "タイトル",
			wantDate:  time.Date(2026, 8, 1, 0, 0, 0, 0, jst),
		},
		{
			name:      "dan notation",
			input:     "作品 第7話「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
			wantDate:  time.Date(2026, 8, 1, 0, 0, 0, 0, jst),
		},

		// === Episode number edge cases ===
		{
			name:      "single digit no zero pad",
			input:     "作品 ep.1「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    1,
			wantSub:   "タイトル",
		},
		{
			name:      "two digit episode",
			input:     "作品 ep.10「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    10,
			wantSub:   "タイトル",
		},
		{
			name:      "three digit episode",
			input:     "作品 ep.100「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    100,
			wantSub:   "タイトル",
		},
		{
			name:      "ep with space after",
			input:     "作品 ep 7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "ep with multiple spaces",
			input:     "作品 ep  7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "ep with full-width space",
			input:     "作品 ep\u30007「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "ep no separator (ep7)",
			input:     "作品 ep7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "EP uppercase with full-width period",
			input:     "作品 EP．5「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    5,
			wantSub:   "タイトル",
		},
		{
			name:      "hash without space",
			input:     "作品#3「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    3,
			wantSub:   "タイトル",
		},
		{
			name:      "dan notation without suffix becomes part of title",
			input:     "作品 第7「タイトル」 (20260801).mp4",
			wantTitle: "作品 第7「タイトル」",
			wantEp:    0,
			wantSub:   "",
		},
		{
			name:      "dan with zero-padded number in input",
			input:     "作品 第07話「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7, // parsed as 7, not 07
			wantSub:   "タイトル",
		},
		{
			name:      "kanji episode 三",
			input:     "鬼の花嫁 第三話「テスト」 (20260726).mp4",
			wantTitle: "鬼の花嫁",
			wantEp:    3,
			wantSub:   "テスト",
		},
		{
			name:      "kanji episode 五",
			input:     "鬼の花嫁 第五話「テスト」 (20260726).mp4",
			wantTitle: "鬼の花嫁",
			wantEp:    5,
			wantSub:   "テスト",
		},
		{
			name:      "kanji episode 十七",
			input:     "黄泉のツガイ 第十七話「泣く子と悪い子」 (20260801).mp4",
			wantTitle: "黄泉のツガイ",
			wantEp:    17,
			wantSub:   "泣く子と悪い子",
		},
		{
			name:      "kanji episode with hundred unit",
			input:     "長期作品 第百二十三話「百話超え」 (20260801).mp4",
			wantTitle: "長期作品",
			wantEp:    123,
			wantSub:   "百話超え",
		},
		{
			name:      "kanji episode written digit by digit",
			input:     "長期作品 第一〇〇話「百話」 (20260801).mp4",
			wantTitle: "長期作品",
			wantEp:    100,
			wantSub:   "百話",
		},
		{
			name:      "kanji episode with thousand unit",
			input:     "長期作品 第千百一話「千話超え」 (20260801).mp4",
			wantTitle: "長期作品",
			wantEp:    1101,
			wantSub:   "千話超え",
		},
		{
			name:      "kanji episode with 幕 suffix",
			input:     "天幕のジャードゥーガル 第六幕「メルゲンの民」 (20260802).mp4",
			wantTitle: "天幕のジャードゥーガル",
			wantEp:    6,
			wantSub:   "メルゲンの民",
		},
		{
			name:      "arabic episode with 幕 suffix",
			input:     "天幕のジャードゥーガル 第6幕「メルゲンの民」 (20260802).mp4",
			wantTitle: "天幕のジャードゥーガル",
			wantEp:    6,
			wantSub:   "メルゲンの民",
		},
		{
			name:      "arabic episode with 番 suffix",
			input:     "ワールド イズ ダンシング 第四番「萌え出る鼓動」 (20260731).mp4",
			wantTitle: "ワールド イズ ダンシング",
			wantEp:    4,
			wantSub:   "萌え出る鼓動",
		},
		{
			name:      "kanji episode with 怪 suffix",
			input:     "レッツゴー怪奇組 第三怪 (20260724).mp4",
			wantTitle: "レッツゴー怪奇組",
			wantEp:    3,
			wantSub:   "",
		},
		{
			// Regression: arabicEpisodePattern was missing 怪 from its
			// suffix class even though kanjiEpisodePattern included it,
			// so this form was previously left entirely unrecognized
			// (swallowed into the work title, episode number 0).
			name:      "arabic episode with 怪 suffix",
			input:     "レッツゴー怪奇組 第3怪 (20260724).mp4",
			wantTitle: "レッツゴー怪奇組",
			wantEp:    3,
			wantSub:   "",
		},
		{
			name:      "season indicator not matched as episode",
			input:     "ウマ娘 シンデレラグレイ(第2クール) 第21話「有マ記念」 (20260306).mp4",
			wantTitle: "ウマ娘 シンデレラグレイ(第2クール)",
			wantEp:    21,
			wantSub:   "有マ記念",
		},
		{
			name:      "season indicator 第4期 not matched as episode",
			input:     "転生したらスライムだった件 第4期 #88「夜明けの勇者グラン」 (20260725).mp4",
			wantTitle: "転生したらスライムだった件 第4期",
			wantEp:    88,
			wantSub:   "夜明けの勇者グラン",
		},

		// === Subtitle edge cases ===
		{
			name:      "「」 in title before episode marker not extracted as subtitle",
			input:     "[字]アニメA「きみを愛する気はない」と言った次期公爵様がなぜか溺愛してきます #4 (20260727).mp4",
			wantTitle: "「きみを愛する気はない」と言った次期公爵様がなぜか溺愛してきます",
			wantEp:    4,
			wantSub:   "",
		},
		{
			name:      "「」 after episode marker is subtitle",
			input:     "作品 #4「サブタイトル」 (20260727).mp4",
			wantTitle: "作品",
			wantEp:    4,
			wantSub:   "サブタイトル",
		},
		{
			name:      "no subtitle",
			input:     "作品 ep.7 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "",
			wantDate:  time.Date(2026, 8, 1, 0, 0, 0, 0, jst),
		},
		{
			name:      "subtitle with multiple lines",
			input:     "作品 ep.7「第一行\\n第二行」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "第一行\\n第二行",
		},
		{
			name:      "subtitle with special chars",
			input:     "作品 ep.7「title: ~!@#$%」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "title: ~!@#$%",
		},
		{
			name:      "multiple brackets - last one is subtitle",
			input:     "作品【日本語】ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品【日本語】",
			wantEp:    7,
			wantSub:   "タイトル",
		},

		// === Metadata tag stripping (rp1 equivalent) ===
		{
			name:      "strip [字] tag",
			input:     "[字]作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip [新] tag",
			input:     "[新]作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip [再] tag",
			input:     "[再]作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip [無] tag",
			input:     "[無]作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip [多] tag",
			input:     "[多]作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip [SS] tag",
			input:     "[SS]アニメ作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "アニメ作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip full-width bracket tag",
			input:     "【ANiMAZiNG!!!】作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip full-width bracket tag Japanese",
			input:     "【字幕】作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "preserve bracketed work title",
			input:     "【推しの子】 第1話「Mother and Children」 (20260801).mp4",
			wantTitle: "【推しの子】",
			wantEp:    1,
			wantSub:   "Mother and Children",
		},
		{
			name:      "preserve bracketed work title before season qualifier",
			input:     "【推しの子】 第2期 ep.1「東京ブレイド」 (20260801).mp4",
			wantTitle: "【推しの子】 第2期",
			wantEp:    1,
			wantSub:   "東京ブレイド",
		},
		{
			name:      "strip tag after leading full-width space",
			input:     "　【字幕】作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip angle bracket tag",
			input:     "＜アニメギルド＞作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip multiple tags",
			input:     "[字]【ANiMAZiNG!!!】作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "strip free marker",
			input:     "無料≫作品 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},

		// === Date edge cases ===
		{
			name:      "no date",
			input:     "作品 ep.7「タイトル」.mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "date without parentheses stays in work title",
			input:     "20260801 作品 ep.7「タイトル」.mp4",
			wantTitle: "20260801 作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},

		// === Title edge cases ===
		{
			name:      "title with numbers",
			input:     "24時間テレビ ep.7「タイトル」 (20260801).mp4",
			wantTitle: "24時間テレビ",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "title with season info",
			input:     "物語シリーズ 第2期 ep.7「タイトル」 (20260801).mp4",
			wantTitle: "物語シリーズ 第2期",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "title with full-width space",
			input:     "作品\u3000ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "title with trailing space before ep",
			input:     "作品  ep.7「タイトル」 (20260801).mp4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "long title",
			input:     "とても長い作品のタイトルがここに入ります ep.7「タイトル」 (20260801).mp4",
			wantTitle: "とても長い作品のタイトルがここに入ります",
			wantEp:    7,
			wantSub:   "タイトル",
		},

		// === Extension edge cases ===
		{
			name:      "mkv extension",
			input:     "作品 ep.7「タイトル」 (20260801).mkv",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "ts extension",
			input:     "作品 ep.7「タイトル」 (20260801).ts",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},
		{
			name:      "uppercase extension",
			input:     "作品 ep.7「タイトル」 (20260801).MP4",
			wantTitle: "作品",
			wantEp:    7,
			wantSub:   "タイトル",
		},

		// === Error cases ===
		{
			name:    "empty filename",
			input:   ".mp4",
			wantErr: true,
		},
		{
			name:    "only extension",
			input:   ".",
			wantErr: true,
		},
		{
			name:    "no recognizable content",
			input:   "......mp4",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFilename(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseFilename(%q) succeeded, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFilename(%q) error: %v", tt.input, err)
			}
			if got.WorkTitle != tt.wantTitle {
				t.Errorf("WorkTitle = %q, want %q", got.WorkTitle, tt.wantTitle)
			}
			if got.EpisodeNumber != tt.wantEp {
				t.Errorf("EpisodeNumber = %d, want %d", got.EpisodeNumber, tt.wantEp)
			}
			if got.Subtitle != tt.wantSub {
				t.Errorf("Subtitle = %q, want %q", got.Subtitle, tt.wantSub)
			}
			if !tt.wantDate.IsZero() && !got.RecordedDate.Equal(tt.wantDate) {
				t.Errorf("RecordedDate = %v, want %v", got.RecordedDate, tt.wantDate)
			}
		})
	}
}

func TestStripMetadataTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"strip [字]", "[字]作品名", "作品名"},
		{"strip [新]", "[新]作品名", "作品名"},
		{"strip [再]", "[再]作品名", "作品名"},
		{"strip [無]", "[無]作品名", "作品名"},
		{"strip [多]", "[多]作品名", "作品名"},
		{"strip [SS]", "[SS]作品名", "作品名"},
		{"strip [解]", "[解]作品名", "作品名"},
		{"strip [終]", "[終]作品名", "作品名"},
		{"strip アニメA prefix", "アニメA作品名", "作品名"},
		{"strip アニメB prefix", "アニメB作品名", "作品名"},
		{"strip アニメA・ prefix", "アニメA・天幕のジャードゥーガル", "天幕のジャードゥーガル"},
		{"strip [字]+アニメA", "[字]アニメA作品名", "作品名"},
		{"strip 【tag】", "【ANiMAZiNG!!!】作品名", "作品名"},
		{"strip 【日本語tag】", "【字幕】作品名", "作品名"},
		{"strip ＜tag＞", "＜アニメギルド＞作品名", "作品名"},
		{"strip 無料≫", "無料≫作品名", "作品名"},
		{"strip multiple tags", "[字]【tag】作品名", "作品名"},
		{"preserve bracketed work title before episode", "【推しの子】 第1話", "【推しの子】 第1話"},
		{"strip after full-width leading space", "　【字幕】作品名", "作品名"},
		{"no tags to strip", "作品名", "作品名"},
		{"empty string", "", ""},
		{"tags in middle preserved", "作品【中間】名", "作品【中間】名"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMetadataTags(tt.input)
			if got != tt.expected {
				t.Errorf("StripMetadataTags(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
