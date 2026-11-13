package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rengotaku/myroutine/internal/night"
)

// nightRunner は夜間実行の入力を一時ディレクトリに置いた Runner を返す。
func nightRunner(t *testing.T, jobs string) (*Runner, *strings.Builder, string) {
	t.Helper()
	nightDir := t.TempDir()
	reportDir := filepath.Join(nightDir, "report")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"generated_at":"2026-11-13T09:03:00+09:00","counts":{"stopped":2},"jobs":[` + jobs + `]}`
	if err := os.WriteFile(filepath.Join(reportDir, "latest.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(night.EnvDir, nightDir)

	var buf strings.Builder
	d := &diag{w: &buf}
	epoch := time.Date(2026, 11, 13, 9, 3, 0, 0, time.FixedZone("", 9*3600)).Unix()
	r := &Runner{
		cfg:  Config{LogDir: t.TempDir(), NowEpoch: &epoch},
		opt:  Options{},
		diag: d,
	}
	return r, &buf, night.AckPath(r.cfg.LogDir)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func TestNightDone(t *testing.T) {
	jobs := `{"id":"a1","category":"stopped"},{"id":"a2","category":"error"}`
	r, out, ackPath := nightRunner(t, jobs)

	// 全部が結果にあれば 0 で、まとめて 1 回で追記する
	if code := r.runNightDone([]string{"a1", "a2"}); code != 0 {
		t.Fatalf("code = %d: %s", code, out.String())
	}
	lines := readLines(t, ackPath)
	if len(lines) != 2 {
		t.Fatalf("ack = %q", lines)
	}
	acked, skipped := night.LoadAcks(ackPath)
	if len(acked) != 2 || skipped != 0 {
		t.Errorf("読み戻し = %v, %d", acked, skipped)
	}
	if _, ok := acked["a1"]; !ok {
		t.Error("a1 が記録されていない")
	}

	// 結果に無い id が 1 つでもあれば、何も書かずに 1（RF-5）
	var buf strings.Builder
	r.diag = &diag{w: &buf}
	if code := r.runNightDone([]string{"a1", "zz"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "結果に無い id: zz") {
		t.Errorf("理由 = %q", buf.String())
	}
	if got := readLines(t, ackPath); len(got) != 2 {
		t.Errorf("結果に無い id があるのに追記した: %q", got)
	}

	// 引数なしは usage で 1
	buf.Reset()
	if code := r.runNightDone(nil); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "Usage: myroutine night-done") {
		t.Errorf("usage = %q", buf.String())
	}

	// 結果が読めないときも 1
	t.Setenv(night.EnvDir, t.TempDir())
	buf.Reset()
	if code := r.runNightDone([]string{"a1"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "夜間実行の結果を読めません") {
		t.Errorf("理由 = %q", buf.String())
	}
}

// night-done は done と同じ位置づけのサブコマンドとして解釈され、
// view の経路に載せ替えて runNightDone に渡る。
func TestParseArgsNightDone(t *testing.T) {
	o, err := parseArgs([]string{"night-done", "a1", "a2"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.NightDone {
		t.Error("NightDone が立っていない")
	}
	if len(o.Args) != 3 || o.Args[0] != "view" || o.Args[1] != "a1" || o.Args[2] != "a2" {
		t.Fatalf("args = %v", o.Args)
	}
	// 通常の view はそのまま
	o, err = parseArgs([]string{"view", "--no-open"})
	if err != nil {
		t.Fatal(err)
	}
	if o.NightDone || len(o.Args) != 1 || o.Args[0] != "view" {
		t.Fatalf("options = %+v", o)
	}
}
