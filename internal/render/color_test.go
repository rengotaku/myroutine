package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rengotaku/myroutine/internal/textwidth"
)

// TestNoColorProducesNoANSI は指摘4（--no-color が効かない）の回帰テスト。
// Options.Color が false のときは ANSI エスケープを一切出さないことを
// 確認する。
func TestNoColorProducesNoANSI(t *testing.T) {
	lines := []Line{
		{Kind: KindText, Indent: 2, Text: "✓ [1/1] ok step", Right: "1.0s", IconLen: 1, IconStyle: StyleOK},
	}
	var out bytes.Buffer
	Render(&out, []Block{{Lines: lines}}, Options{Width: 40, Color: false})

	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("Color: false のときに ANSI エスケープが出力に含まれてはいけない。got: %q", out.String())
	}
}

// TestColorProducesANSIOnIconAndDim は Color: true のとき、アイコンに色が
// 付き、Right（所要時間）が dim で描画されることを確認する。
func TestColorProducesANSIOnIconAndDim(t *testing.T) {
	lines := []Line{
		{Kind: KindText, Indent: 2, Text: "✓ [1/1] ok step", Right: "1.0s", IconLen: 1, IconStyle: StyleOK},
	}
	var out bytes.Buffer
	Render(&out, []Block{{Lines: lines}}, Options{Width: 40, Color: true})
	got := out.String()

	if !strings.Contains(got, ansiGreen) {
		t.Fatalf("StyleOK のアイコンは緑で着色されるべき。got: %q", got)
	}
	if !strings.Contains(got, ansiDim) {
		t.Fatalf("Right（所要時間）は dim で描画されるべき。got: %q", got)
	}
	// 本文（"ok step"）は色で埋めない。
	if !strings.Contains(got, "ok step") {
		t.Fatalf("本文がそのまま含まれているべき。got: %q", got)
	}
}

// TestColorDoesNotChangeVisibleWidth は色付き出力でも、ANSI を除いた実表示幅
// が --width の指定どおりになる（桁揃えが崩れない）ことを確認する。
func TestColorDoesNotChangeVisibleWidth(t *testing.T) {
	const width = 60
	lines := []Line{
		{Kind: KindText, Indent: 2, Text: "✓ [1/1] 全角混じりのステップ名", Right: "12.3s", IconLen: 1, IconStyle: StyleOK},
		{Kind: KindText, Indent: 6, Text: "│ tail line", Dim: true},
		{Kind: KindText, Indent: 2, Text: "⊘ skip 3 件", IconLen: 1, IconStyle: StyleSkip},
	}

	var plain, colored bytes.Buffer
	Render(&plain, []Block{{Lines: lines}}, Options{Width: width, Color: false})
	Render(&colored, []Block{{Lines: lines}}, Options{Width: width, Color: true})

	plainLines := strings.Split(strings.TrimRight(plain.String(), "\n"), "\n")
	coloredLines := strings.Split(strings.TrimRight(colored.String(), "\n"), "\n")

	if len(plainLines) != len(coloredLines) {
		t.Fatalf("行数が変わった: plain=%d colored=%d", len(plainLines), len(coloredLines))
	}
	for i := range plainLines {
		pw := textwidth.Width(plainLines[i])
		cw := textwidth.Width(coloredLines[i])
		if pw != cw {
			t.Fatalf("行 %d の実表示幅が色付きで変わった: plain=%d colored=%d\nplain:   %q\ncolored: %q", i, pw, cw, plainLines[i], coloredLines[i])
		}
		if strings.Contains(coloredLines[i], "\x1b[") && !strings.Contains(colored.String(), ansiReset) {
			t.Fatalf("色付き行に reset が無い: %q", coloredLines[i])
		}
	}
}

// TestDimStyleTakesPrecedenceOverIcon は Dim: true のとき、IconLen/IconStyle
// による着色より dim を優先することを確認する（仕様: 排他で Dim 優先）。
func TestDimStyleTakesPrecedenceOverIcon(t *testing.T) {
	line := Line{Kind: KindText, Text: "✗ detail", IconLen: 1, IconStyle: StyleFail, Dim: true}
	got := colorizeText(line, true)
	if strings.Contains(got, ansiRed) {
		t.Fatalf("Dim が優先されるべきで、IconStyle の色が出てはいけない。got: %q", got)
	}
	if !strings.Contains(got, ansiDim) {
		t.Fatalf("Dim: true のときは dim で描画されるべき。got: %q", got)
	}
}
