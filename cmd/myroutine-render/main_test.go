package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/rengotaku/myroutine/internal/render"
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
