package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rengotaku/myroutine/internal/textwidth"
)

// 2回目のコードレビュー指摘2: Right が無い行に幅が適用されていない。
// --width（や端末幅）を超えて出力されると、端末側で勝手に折り返されて
// 桁揃えが崩れる。Right を持たない行も含め、出力の全行が指定幅以下に
// 収まることを確認する。

const narrowWidth = 40

func allLinesWithinWidth(t *testing.T, out string, width int) {
	t.Helper()
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := textwidth.Width(line); w > width {
			t.Fatalf("行 %d の表示幅が width(%d) を超えている: w=%d line=%q", i, width, w, line)
		}
	}
}

func TestRenderAllLinesWithinWidth_NoColor(t *testing.T) {
	lines := []Line{
		{Kind: KindText, Indent: 2, Text: "✓ [1/1] とても長いアクション名の説明文がここに続いて幅を超える", Right: "1.0s", IconLen: 1, IconStyle: StyleOK},
		{Kind: KindText, Indent: 6, Text: "│ このテール抜粋の行はとても長くて折り返さず切り詰められるべきです", Dim: true},
		{Kind: KindText, Indent: 6, Text: "└ 全文: /very/long/path/that/exceeds/the/configured/width/of/the/terminal.txt", Dim: true},
		{Kind: KindText, Indent: 6, Text: "無効理由の行がとても長い場合はここで折り返されるべきで切り詰められてはいけません", Dim: true, Wrap: true},
		{Kind: KindText, Indent: 2, Text: "▸ [1] とても長い要対応の説明テキストがここに続いて折り返される想定の行です", Wrap: true},
	}
	var out bytes.Buffer
	Render(&out, []Block{{Lines: lines}}, Options{Width: narrowWidth, Color: false})
	allLinesWithinWidth(t, out.String(), narrowWidth)
}

func TestRenderAllLinesWithinWidth_Color(t *testing.T) {
	lines := []Line{
		{Kind: KindText, Indent: 2, Text: "✓ [1/1] とても長いアクション名の説明文がここに続いて幅を超える", Right: "1.0s", IconLen: 1, IconStyle: StyleOK},
		{Kind: KindText, Indent: 6, Text: "│ このテール抜粋の行はとても長くて折り返さず切り詰められるべきです", Dim: true},
		{Kind: KindText, Indent: 6, Text: "└ 全文: /very/long/path/that/exceeds/the/configured/width/of/the/terminal.txt", Dim: true},
		{Kind: KindText, Indent: 6, Text: "無効理由の行がとても長い場合はここで折り返されるべきで切り詰められてはいけません", Dim: true, Wrap: true},
		{Kind: KindText, Indent: 2, Text: "▸ [1] とても長い要対応の説明テキストがここに続いて折り返される想定の行です", Wrap: true},
	}
	var out bytes.Buffer
	Render(&out, []Block{{Lines: lines}}, Options{Width: narrowWidth, Color: true})
	// textwidth.Width は ANSI エスケープを幅 0 として扱うため、色付きでも
	// このチェックは有効。
	allLinesWithinWidth(t, out.String(), narrowWidth)
}

// 長い説明文は折り返され、2行目以降のインデントが1行目の本文開始位置に
// 揃うことを確認する。
func TestWrapLineIndentAligns(t *testing.T) {
	line := Line{Kind: KindText, Indent: 8, Text: "あいうえおかきくけこさしすせそたちつてとなにぬねのはひふへほ", Wrap: true}
	var out bytes.Buffer
	Render(&out, []Block{{Lines: []Line{line}}}, Options{Width: 20, Color: false})

	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(got) < 2 {
		t.Fatalf("折り返されて複数行になるはずが1行のまま: %q", out.String())
	}
	for i, l := range got {
		if w := textwidth.Width(l); w > 20 {
			t.Fatalf("行 %d の表示幅が width を超えている: w=%d line=%q", i, w, l)
		}
		leading := len(l) - len(strings.TrimLeft(l, " "))
		if leading != 8 {
			t.Fatalf("行 %d の先頭インデントが1行目に揃っていない: got=%d want=8 line=%q", i, leading, l)
		}
	}
}

// 長い tail 行・長いパスは折り返さず、"…" で切り詰められることを確認する。
func TestTailAndPathLinesAreTruncatedNotWrapped(t *testing.T) {
	lines := []Line{
		{Kind: KindText, Indent: 6, Text: "│ " + strings.Repeat("x", 100), Dim: true},
		{Kind: KindText, Indent: 6, Text: "└ 全文: " + strings.Repeat("/segment", 30) + "/file.txt", Dim: true},
	}
	var out bytes.Buffer
	Render(&out, []Block{{Lines: lines}}, Options{Width: narrowWidth, Color: false})

	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(got) != len(lines) {
		t.Fatalf("tail/path 行は折り返さず1行ずつのままのはずが行数が変わった: got=%d want=%d out=%q", len(got), len(lines), out.String())
	}
	for i, l := range got {
		if w := textwidth.Width(l); w > narrowWidth {
			t.Fatalf("行 %d の表示幅が width を超えている: w=%d line=%q", i, w, l)
		}
		if !strings.HasSuffix(l, "…") {
			t.Fatalf("行 %d は切り詰められて末尾が … になるはずが違う: line=%q", i, l)
		}
	}
}

// 折り返しの継続行は HangIndent の分だけ下げ、1 行目の本文開始位置に
// 揃える。ここが揃わないと、継続行がマーカー（"▸ [1] "）の下に潜り込み
// どこから本文が始まるのか読めなくなる。
func TestHangIndentAlignsContinuationLines(t *testing.T) {
	const width = 44
	line := Line{
		Kind:       KindText,
		Indent:     2,
		Text:       "▸ [1] " + strings.Repeat("あ", 40),
		Wrap:       true,
		HangIndent: textwidth.Width("▸ [1] "),
	}

	var buf bytes.Buffer
	Render(&buf, []Block{{Lines: []Line{line}}}, Options{Width: width})
	got := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(got) < 2 {
		t.Fatalf("折り返されていない: %q", got)
	}

	head := textwidth.Width("  ▸ [1] ")
	for i, l := range got {
		if w := textwidth.Width(l); w > width {
			t.Errorf("行 %d が幅を超えた: %d > %d (%q)", i, w, width, l)
		}
		if i == 0 {
			continue
		}
		if indent := len(l) - len(strings.TrimLeft(l, " ")); indent != head {
			t.Errorf("継続行 %d のインデントが本文開始位置と違う: %d != %d (%q)", i, indent, head, l)
		}
	}
}
