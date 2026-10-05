package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/rengotaku/myroutine/internal/render"
	"github.com/rengotaku/myroutine/internal/textwidth"
)

func opt() render.Options {
	return render.Options{Width: 100, Color: false}
}

// TestGolden は付属のサンプル JSONL（壊れた行・未知の ev を含む）を食わせた
// 出力を testdata/golden.txt と比較する。挙動を変えたら
// `go test ./cmd/myroutine-render/... -update` で再生成すること。
func TestGolden(t *testing.T) {
	in, err := os.Open("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("open sample: %v", err)
	}
	defer in.Close()

	var out bytes.Buffer
	process(in, &out, false, opt())

	goldenPath := "testdata/golden.txt"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, out.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if out.String() != string(want) {
		t.Fatalf("output mismatch.\n--- got ---\n%s\n--- want ---\n%s", out.String(), string(want))
	}
}

// TestMalformedLinesDoNotPanic は壊れた JSON 行・未知の ev を含む入力を
// 食わせても panic せず読み進めることを確認する。
func TestMalformedLinesDoNotPanic(t *testing.T) {
	input := strings.Join([]string{
		`not json at all`,
		`{"ev":"step"`, // 途中で切れた JSON
		`{"ev":"totally-unknown-event","x":1}`,
		`{"ev":"step","n":1,"name":"ok step","status":"ok","rc":0,"duration_ms":100,"summary":"","note":"","tail":[]}`,
		``,
		`{"ev":"summary","wall_ms":100,"actions":[],"todos":[],"infos":[],"resolved":[],"disabled":[]}`,
	}, "\n")

	var out bytes.Buffer
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("process panicked: %v", r)
			}
		}()
		process(strings.NewReader(input), &out, false, opt())
	}()

	if !strings.Contains(out.String(), "ok step") {
		t.Fatalf("expected the valid step to still be rendered, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "要対応なし") {
		t.Fatalf("expected summary block to be rendered, got:\n%s", out.String())
	}
}

// TestNoSummaryFlushesPendingSkips は summary イベントが来ないまま入力が
// 閉じても異常終了せず、溜めていた skip だけを出して正常終了することを
// 確認する。
func TestNoSummaryFlushesPendingSkips(t *testing.T) {
	input := strings.Join([]string{
		`{"ev":"run","mode":"all","started_at":"2026-09-18T00:00:00+09:00","total":3,"log":"/tmp/x.txt"}`,
		`{"ev":"step","n":1,"name":"a","status":"ok","rc":0,"duration_ms":100,"summary":"","note":"","tail":[]}`,
		`{"ev":"step","n":2,"name":"b","status":"skip","rc":0,"duration_ms":0,"summary":"","note":"cadence: weekly","tail":[]}`,
	}, "\n")

	var out bytes.Buffer
	process(strings.NewReader(input), &out, false, opt())

	got := out.String()
	if !strings.Contains(got, "skip 1 件") {
		t.Fatalf("expected pending skip to be flushed, got:\n%s", got)
	}
	if strings.Contains(got, "要対応") {
		t.Fatalf("summary block should not be rendered when summary event is missing, got:\n%s", got)
	}
}

// TestSkipDeferredUntilSummaryOrEOF は skip ステップが後続の ok/fail ステップ
// より前には出力されず（溜められ）、summary（または EOF）まで遅延されることを
// 確認する。
func TestSkipDeferredUntilSummaryOrEOF(t *testing.T) {
	input := strings.Join([]string{
		`{"ev":"step","n":1,"name":"a","status":"skip","rc":0,"duration_ms":0,"summary":"","note":"cadence: weekly","tail":[]}`,
		`{"ev":"step","n":2,"name":"b","status":"ok","rc":0,"duration_ms":100,"summary":"","note":"","tail":[]}`,
	}, "\n")

	var out bytes.Buffer
	process(strings.NewReader(input), &out, false, opt())
	got := out.String()

	skipIdx := strings.Index(got, "skip 1 件")
	okIdx := strings.Index(got, "[2/0] b")
	if skipIdx < 0 || okIdx < 0 {
		t.Fatalf("expected both skip summary and step b in output, got:\n%s", got)
	}
	if skipIdx < okIdx {
		t.Fatalf("skip block should come after step b (deferred), got:\n%s", got)
	}
}

// TestNoColorVsColorOutputDiffers は指摘4（--no-color が効かない）の
// 回帰テスト。Options.Color の有無で process の出力が実際に変わる
// （色が付く）ことをエンドツーエンドで確認する。
func TestNoColorVsColorOutputDiffers(t *testing.T) {
	in, err := os.Open("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("open sample: %v", err)
	}
	defer in.Close()

	var plain bytes.Buffer
	process(in, &plain, false, render.Options{Width: 100, Color: false})

	in2, err := os.Open("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("open sample: %v", err)
	}
	defer in2.Close()

	var colored bytes.Buffer
	process(in2, &colored, false, render.Options{Width: 100, Color: true})

	if plain.String() == colored.String() {
		t.Fatalf("--no-color と色付きの出力が同一になっている（色が実装されていない）")
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatalf("Color: true の出力に ANSI エスケープが含まれていない")
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("Color: false の出力に ANSI エスケープが混ざっている")
	}
}

// TestColoredOutputVisibleWidthMatchesWidthOption は色付き出力の各行の
// 表示幅（ANSI を除いた実表示幅）が --width の指定を超えないことを確認する
// （指摘4: 桁揃えが色付きでも崩れないこと）。
func TestColoredOutputVisibleWidthMatchesWidthOption(t *testing.T) {
	const width = 100
	in, err := os.Open("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("open sample: %v", err)
	}
	defer in.Close()

	var out bytes.Buffer
	process(in, &out, false, render.Options{Width: width, Color: true})

	for i, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if w := textwidth.Width(line); w > width {
			t.Fatalf("行 %d の実表示幅 (%d) が --width (%d) を超えている: %q", i, w, width, line)
		}
	}
}

// TestStartLineRenderedBeforeStep は start イベントが開始行として即座に
// 出力され、終了行に終了時刻が付くことを確認する。
func TestStartLineRenderedBeforeStep(t *testing.T) {
	input := strings.Join([]string{
		`{"ev":"run","mode":"all","started_at":"2026-10-05T08:30:00+09:00","total":2,"log":""}`,
		`{"ev":"start","n":5,"pos":2,"name":"Brew Update","slug":"brew","at":"08:38:00"}`,
		`{"ev":"step","n":5,"pos":2,"name":"Brew Update","slug":"brew","status":"ok","rc":0,"duration_ms":420000,"ended_at":"08:45:00","tail":[]}`,
	}, "\n")

	var out bytes.Buffer
	process(strings.NewReader(input), &out, false, opt())
	got := out.String()

	startIdx := strings.Index(got, "▶ [2/2] Brew Update  開始 08:38")
	endIdx := strings.Index(got, "✓ [2/2] Brew Update")
	if startIdx < 0 || endIdx < 0 || startIdx > endIdx {
		t.Fatalf("開始行 → 終了行の順で出るべき。got:\n%s", got)
	}
	if !strings.Contains(got, "（終了 08:45）") {
		t.Fatalf("終了行に終了時刻が必要。got:\n%s", got)
	}
}
