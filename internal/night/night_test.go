package night

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeReport は dir/report/latest.json を書く。
func writeReport(t *testing.T, dir, body string) {
	t.Helper()
	reportDir := filepath.Join(dir, "report")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "latest.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReport(t *testing.T) {
	dir := t.TempDir()
	writeReport(t, dir, `{
  "generated_at": "2026-11-13T09:03:00+09:00",
  "since": "2026-11-12T18:00:00+09:00",
  "counts": {"done": 1, "stopped": 1, "error": 1, "timeout": 0, "queued": 2},
  "jobs": [
    {"id": "n1", "repo": "owner/name", "issue": 57, "state": "waiting-human",
     "category": "stopped", "reason": "承認境界: ホストを変える: ssh",
     "room": "night-name-57", "pr": 12, "merged_sha": null,
     "resume_cmd": "room resume night-name-57",
     "started_at": "2026-11-12T20:00:00+09:00",
     "finished_at": "2026-11-12T21:00:00+09:00",
     "progress": "引き継ぎの先頭の文"}
  ]
}`)
	rep, err := LoadReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.GeneratedAt != "2026-11-13T09:03:00+09:00" || rep.Since != "2026-11-12T18:00:00+09:00" {
		t.Errorf("report = %+v", rep)
	}
	if rep.Counts["done"] != 1 || rep.Counts["queued"] != 2 {
		t.Errorf("counts = %v", rep.Counts)
	}
	if len(rep.Jobs) != 1 {
		t.Fatalf("jobs = %d 件", len(rep.Jobs))
	}
	j := rep.Jobs[0]
	if j.ID != "n1" || j.Repo != "owner/name" || j.Issue != 57 || j.Category != "stopped" {
		t.Errorf("job = %+v", j)
	}
	if j.Reason == nil || *j.Reason != "承認境界: ホストを変える: ssh" {
		t.Errorf("reason = %v", j.Reason)
	}
	if j.Progress == nil || *j.Progress != "引き継ぎの先頭の文" {
		t.Errorf("progress = %v", j.Progress)
	}
	if j.Room == nil || *j.Room != "night-name-57" {
		t.Errorf("room = %v", j.Room)
	}
	if j.ResumeCmd == nil || *j.ResumeCmd != "room resume night-name-57" {
		t.Errorf("resume_cmd = %v", j.ResumeCmd)
	}
	if j.PR == nil || *j.PR != 12 {
		t.Errorf("pr = %v", j.PR)
	}
	if j.MergedSHA != nil {
		t.Errorf("merged_sha = %v（null は nil になるはず）", *j.MergedSHA)
	}
	if j.StartedAt == nil || *j.StartedAt != "2026-11-12T20:00:00+09:00" {
		t.Errorf("started_at = %v", j.StartedAt)
	}

	t.Run("無い", func(t *testing.T) {
		if _, err := LoadReport(t.TempDir()); err == nil {
			t.Fatal("report/latest.json が無いのにエラーにならない")
		}
	})
	t.Run("空", func(t *testing.T) {
		empty := t.TempDir()
		writeReport(t, empty, "  \n")
		if _, err := LoadReport(empty); err == nil {
			t.Fatal("空のファイルなのにエラーにならない")
		}
	})
	t.Run("壊れた", func(t *testing.T) {
		broken := t.TempDir()
		writeReport(t, broken, `{"generated_at": `)
		if _, err := LoadReport(broken); err == nil {
			t.Fatal("壊れた JSON なのにエラーにならない")
		}
	})
}

// 結果の null / 欠けた項目は nil で受け、落ちない（画面に "null" を出さない元）。
func TestLoadReportNulls(t *testing.T) {
	dir := t.TempDir()
	writeReport(t, dir, `{
  "generated_at": "2026-11-13T09:03:00+09:00",
  "counts": {"error": 1},
  "jobs": [
    {"id": "n1", "category": "error", "reason": null, "progress": null,
     "resume_cmd": null, "room": null, "pr": null, "merged_sha": null,
     "started_at": null, "finished_at": null}
  ]
}`)
	rep, err := LoadReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	j := rep.Jobs[0]
	if j.Reason != nil || j.Progress != nil || j.ResumeCmd != nil || j.Room != nil ||
		j.PR != nil || j.MergedSHA != nil || j.StartedAt != nil || j.FinishedAt != nil {
		t.Fatalf("null が nil になっていない: %+v", j)
	}
	if j.State != "" || j.Repo != "" || j.Issue != 0 {
		t.Errorf("欠けた項目 = %+v", j)
	}
}

// RunStatus は偽の night-run を渡して、5 通りの失敗の理由を error の文言で返す（RF-4）。
func TestRunStatus(t *testing.T) {
	dir := t.TempDir()
	script := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ok := script("ok.sh", `#!/bin/sh
cat <<'EOF'
{"generated_at":"2026-11-14T00:40:00+09:00","executor_alive":true,
 "running":[{"id":"r1","repo":"o/n","issue":57,"started_at":"2026-11-14T00:20:00+09:00","room":"night-n-57"}],
 "queued":[{"id":"q1","repo":"o/n","issue":58,"queued_at":"2026-11-13T22:00:00+09:00"},
           {"id":"q2","repo":"o/m","issue":59,"queued_at":null}],
 "stale_running":[],"needs_attention":1}
EOF
`)
	st, err := RunStatus(ok, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ExecutorAlive || st.NeedsAttention != 1 || st.GeneratedAt != "2026-11-14T00:40:00+09:00" {
		t.Errorf("status = %+v", st)
	}
	if len(st.Running) != 1 || st.Running[0].Room == nil || *st.Running[0].Room != "night-n-57" {
		t.Errorf("running = %+v", st.Running)
	}
	if len(st.Queued) != 2 || st.Queued[0].QueuedAt == nil || st.Queued[1].QueuedAt != nil {
		t.Errorf("queued = %+v", st.Queued)
	}

	fail := script("fail.sh", "#!/bin/sh\necho 'night-run: 設定がありません' >&2\nexit 1\n")
	if _, err := RunStatus(fail, time.Second); err == nil || !strings.Contains(err.Error(), "終了コード 1") {
		t.Errorf("終了コード 1 の理由が出ない: %v", err)
	}

	slow := script("slow.sh", "#!/bin/sh\nsleep 5\n")
	start := time.Now()
	_, err = RunStatus(slow, 150*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "終わりません") {
		t.Errorf("時間切れの理由が出ない: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("時間切れで打ち切っていない: %s", d)
	}

	broken := script("broken.sh", "#!/bin/sh\necho 'not json'\n")
	if _, err := RunStatus(broken, time.Second); err == nil || !strings.Contains(err.Error(), "読めません") {
		t.Errorf("壊れた JSON の理由が出ない: %v", err)
	}

	if _, err := RunStatus(filepath.Join(dir, "no-such-night-run"), time.Second); err == nil ||
		!strings.Contains(err.Error(), "見つかりません") {
		t.Errorf("コマンドが無いときの理由が出ない: %v", err)
	}
}

// 壊れた行は数えて飛ばす（RF-5）。
func TestLoadAcks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "night-ack.jsonl")
	body := `{"at":"2026-11-13T09:00:00+09:00","id":"a1"}
not json
{"at":"bad","id":"a2"}
{"at":"2026-11-13T09:00:00+09:00","id":""}
{"at":"2026-11-12T09:00:00+09:00","id":"a3"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	acked, skipped := LoadAcks(path)
	if len(acked) != 2 {
		t.Fatalf("acked = %v", acked)
	}
	if _, ok := acked["a1"]; !ok {
		t.Error("a1 が読めていない")
	}
	if at := acked["a3"]; !at.Equal(time.Date(2026, 11, 12, 9, 0, 0, 0, time.FixedZone("", 9*3600))) {
		t.Errorf("a3 の時刻 = %s", at)
	}
	if skipped != 3 {
		t.Errorf("skipped = %d, want 3", skipped)
	}

	empty, skipped := LoadAcks(filepath.Join(t.TempDir(), "nothing.jsonl"))
	if len(empty) != 0 || skipped != 0 {
		t.Errorf("ファイルが無いとき = %v, %d", empty, skipped)
	}
	if AckPath("/log") != filepath.Join("/log", "night-ack.jsonl") {
		t.Errorf("AckPath = %s", AckPath("/log"))
	}
}

// 前回が改行なしで途切れていても、先に改行を足して新しい行を壊さない。
func TestAppendAckRepairsTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "night-ack.jsonl")
	if err := os.WriteFile(path, []byte(`{"at":"2026-11-12T09:00:00+09:00","id":"a1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 13, 9, 3, 0, 0, time.FixedZone("", 9*3600))
	if err := AppendAck(path, []string{"a2", "a3", "a2"}, at); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d: %q", len(lines), string(b))
	}
	if !strings.Contains(lines[0], `"id":"a1"`) {
		t.Errorf("途切れた行が壊れた: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"id":"a2"`) || !strings.Contains(lines[2], `"id":"a3"`) {
		t.Errorf("追記 = %q", lines[1:])
	}
	if strings.Count(string(b), `"id":"a2"`) != 1 {
		t.Error("同じ id を 2 回書いている")
	}
	acked, skipped := LoadAcks(path)
	if len(acked) != 3 || skipped != 0 {
		t.Errorf("読み戻し = %v, %d", acked, skipped)
	}
}
