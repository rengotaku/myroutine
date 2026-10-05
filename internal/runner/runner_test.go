package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newDiag() (*diag, *bytes.Buffer) {
	var buf bytes.Buffer
	return &diag{w: &buf}, &buf
}

func TestParseArgs(t *testing.T) {
	o, err := parseArgs([]string{"-n", "brew,omz", "--timeout", "30", "-y", "git"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.DryRun || !o.Yes || o.Timeout == nil || *o.Timeout != 30 {
		t.Fatalf("unexpected options: %+v", o)
	}
	if !reflect.DeepEqual(o.Args, []string{"brew,omz", "git"}) {
		t.Fatalf("args = %v", o.Args)
	}

	cases := map[string][]string{
		"--timeout には秒数":            {"--timeout", "abc"},
		"不明なオプション: --bogus":         {"--bogus"},
		"--json と --jsonl は併用できません": {"--json", "--jsonl"},
	}
	for want, args := range cases {
		if _, err := parseArgs(args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: err = %v, want %q", args, err, want)
		}
	}
	if o, _ := parseArgs([]string{"--", "-x"}); !reflect.DeepEqual(o.Args, []string{"-x"}) {
		t.Errorf("-- の後はそのまま引数: %v", o.Args)
	}
}

func TestUnescapeRoundTrip(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		"a%0Ab":            "a\nb",
		"a%09b":            "a\tb",
		"100%25":           "100%",
		`literal\nend`:     `literal\nend`,
		"%250A は %0A ではない": "%0A は \n ではない",
		"末尾の %":            "末尾の %",
	}
	for in, want := range cases {
		if got := unescape(in); got != want {
			t.Errorf("unescape(%q) = %q, want %q", in, got, want)
		}
	}
}

func meta(t *testing.T, recs string) (*Step, string) {
	t.Helper()
	d, buf := newDiag()
	vars, src, done := parseMetaRecords(recs)
	if src || !done {
		t.Fatalf("records: src=%v done=%v", src, done)
	}
	s, _ := validateMeta(3, "brew", "/x/brew.sh", vars, d)
	return s, buf.String()
}

func TestValidateMeta(t *testing.T) {
	s, msg := meta(t, "M\tstep_name\tscalar\tBrew%09Update\n"+
		"M\tstep_critical\tscalar\ttrue\n"+
		"M\tstep_requires\tarray\tbrew\t\tgit\n"+
		"M\tstep_requires_path\tarray\n"+
		"M\tstep_timeout\tscalar\t300\n"+
		"M\tstep_cadence\tscalar\tweekly\n"+
		"M\tstep_cadence_weekday\tscalar\tFri\n"+
		"D\n")
	if s == nil {
		t.Fatalf("unexpected error: %s", msg)
	}
	if s.Name != "Brew\tUpdate" || !s.Critical || s.Cadence != "weekly" || s.Weekday != 5 {
		t.Errorf("step = %+v", s)
	}
	if !reflect.DeepEqual(s.Requires, []string{"brew", "git"}) || len(s.RequiresPath) != 0 {
		t.Errorf("requires = %v / %v（空の要素は落ちる）", s.Requires, s.RequiresPath)
	}
	if s.Timeout == nil || *s.Timeout != 300 {
		t.Errorf("timeout = %v", s.Timeout)
	}

	errCases := map[string]string{
		"M\tstep_name\tscalar\tA\nM\tstep_critcal\tscalar\ttrue\nD\n":                                       "未知の宣言があります: step_critcal",
		"M\tstep_name\tscalar\tA\nM\tstep_critical\tscalar\tyes\nD\n":                                       "step_critical に使えるのは true / false だけです",
		"M\tstep_name\tscalar\tA\nM\tstep_destructive\tscalar\t\nD\n":                                       "step_destructive に使えるのは true / false だけです",
		"M\tstep_name\tscalar\tA\nM\tstep_timeout\tscalar\t-1\nD\n":                                         "step_timeout には 0 以上の整数",
		"M\tstep_name\tscalar\tA\nM\tstep_timeout\tscalar\t5m\nD\n":                                         "step_timeout には 0 以上の整数",
		"M\tstep_critical\tscalar\ttrue\nD\n":                                                               "step_name が定義されていません",
		"M\tstep_name\tscalar\tA\nM\tstep_cadence\tscalar\tfortnightly\nD\n":                                "step_cadence に使えるのは",
		"M\tstep_name\tscalar\tA\nM\tstep_parallel\tscalar\ttrue\nM\tstep_destructive\tscalar\ttrue\nD\n":   "step_parallel と step_destructive は併用できません",
		"M\tstep_name\tscalar\tA\nM\tstep_parallel\tscalar\ttrue\nM\tstep_abort_on_fail\tscalar\ttrue\nD\n": "step_parallel と step_abort_on_fail は併用できません",
		"M\tstep_name\tscalar\tA\nM\tstep_parallel\tscalar\ttrue\nM\tstep_interactive\tscalar\ttrue\nD\n":   "step_parallel と step_interactive は併用できません",
		"M\tstep_name\tscalar\tA\nM\tstep_disabled\tscalar\tFALSE\nD\n":                                     "step_disabled には無効化の理由を書いてください",
		"M\tstep_name\tscalar\tA\nM\tstep_cadence_weekday\tscalar\tfunday\nD\n":                             "step_cadence_weekday に使えるのは mon..sun / 1..7 だけです: funday (brew.sh)",
		"M\tstep_name\tscalar\tA\nM\tstep_requires\tassoc\nD\n":                                             "step_requires に連想配列は使えません",
	}
	for recs, want := range errCases {
		s, msg := meta(t, recs)
		if s != nil || !strings.Contains(msg, want) {
			t.Errorf("records %q: step=%v msg=%q, want %q", recs, s, msg, want)
		}
	}
}

func TestParseMetaRecordsIncomplete(t *testing.T) {
	if _, src, _ := parseMetaRecords("E\t0\tsource_failed\n"); !src {
		t.Error("source_failed を拾えていない")
	}
	if _, _, done := parseMetaRecords("M\tstep_name\tscalar\tA\n"); done {
		t.Error("D が無いのに完了扱いになった")
	}
}

func writeSteps(t *testing.T, order string, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "order"), []byte(order), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("step_name=x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadOrder(t *testing.T) {
	dir := writeSteps(t, "# 実行順\nalpha   # 先頭\n\n  bravo\n", "alpha.sh", "bravo.sh", ".hidden.sh")
	d, buf := newDiag()
	got, ok := readOrder(dir, d)
	if !ok || !reflect.DeepEqual(got, []string{"alpha", "bravo"}) {
		t.Fatalf("got %v ok=%v msg=%s", got, ok, buf.String())
	}

	dir = writeSteps(t, "a\nghost\na\nls\n42\nb.c\n", "a.sh", "50-delta.sh")
	d, buf = newDiag()
	if _, ok := readOrder(dir, d); ok {
		t.Fatal("不整合があるのに成功した")
	}
	for _, want := range []string{
		"order で slug が重複しています: a (order:3)",
		"サブコマンドと衝突しています: ls",
		"slug を数字だけにはできません: 42",
		"英数字と _ - だけです: b.c",
		"ファイルがありません: ghost.sh",
		"order に書かれていないステップがあります: 50-delta.sh",
		"delta.sh に名前を変え、order に delta を足してください",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("「%s」が報告されない:\n%s", want, buf.String())
		}
	}
}

func historyRunner(t *testing.T, lines ...string) *Runner {
	t.Helper()
	dir := t.TempDir()
	if len(lines) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "history.tsv"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := newDiag()
	return &Runner{
		cfg: Config{LogDir: dir}, diag: d, fullRun: true,
		streak: map[string]int{}, newIDs: map[string]bool{}, results: map[int]*result{},
	}
}

func TestAnalyzeHistory(t *testing.T) {
	r := historyRunner(t,
		"2026-08-09\t09:00:00\tstale.stuck,stale.gone",
		"2026-08-10\t08:00:00\t",
		"2026-08-10\t18:00:00\tstale.stuck,stale.gone\tstale",
		"2026-08-11\t09:00:00\t\tother",
		"2026-08-12\t09:00:00\tstale.stuck,stale.gone\tstale",
	)
	r.actions = []item{{1, "stale.stuck", "x"}, {1, "stale.fresh", "y"}}
	r.evaluated = []string{"stale"}
	r.analyzeHistory()
	// 08-11 は stale を評価していないので連続を切らない。同じ日の 2 行は 1 日として数える。
	if r.streak["stale.stuck"] != 4 {
		t.Errorf("streak = %d, want 4", r.streak["stale.stuck"])
	}
	if !r.newIDs["stale.fresh"] || r.newIDs["stale.stuck"] {
		t.Errorf("new = %v", r.newIDs)
	}
	if !reflect.DeepEqual(r.resolved, []string{"stale.gone"}) {
		t.Errorf("resolved = %v", r.resolved)
	}

	// 今回評価していないステップの要対応は解消にしない
	r = historyRunner(t, "2026-08-12\t09:00:00\tgone.stuck\tgone")
	r.analyzeHistory()
	if len(r.resolved) != 0 {
		t.Errorf("評価していないのに解消: %v", r.resolved)
	}

	// 部分実行は比較しない
	r = historyRunner(t, "2026-08-12\t09:00:00\ta.x\ta")
	r.fullRun = false
	r.evaluated = []string{"a"}
	r.analyzeHistory()
	if len(r.resolved) != 0 || len(r.streak) != 0 {
		t.Errorf("部分実行で比較した: %v %v", r.resolved, r.streak)
	}
}

func TestCadenceDue(t *testing.T) {
	tue := time.Date(2026, 8, 19, 12, 0, 0, 0, time.Local).Unix()
	r := historyRunner(t)
	r.cfg.NowEpoch = &tue
	stamp := func(slug, day string) {
		_ = os.MkdirAll(filepath.Join(r.cfg.LogDir, "last-run"), 0o755)
		_ = os.WriteFile(r.stampFile(slug), []byte(day+"\n"), 0o644)
	}
	weekly := &Step{Slug: "w", Cadence: "weekly"}
	if ok, _ := r.cadenceDue(weekly); !ok {
		t.Error("前回が無ければ実行する")
	}
	stamp("w", "2026-08-12")
	if ok, _ := r.cadenceDue(weekly); !ok {
		t.Error("weekly は 7 日で実行する")
	}
	stamp("w", "2026-08-14")
	if ok, why := r.cadenceDue(weekly); ok || why != "weekly（前回 2026-08-14）" {
		t.Errorf("weekly は 7 日未満なら skip: ok=%v why=%q", ok, why)
	}
	fri := &Step{Slug: "f", Cadence: "biweekly", Weekday: 5}
	if ok, why := r.cadenceDue(fri); ok || why != "biweekly（金曜のみ）" {
		t.Errorf("曜日違い: ok=%v why=%q", ok, why)
	}
	monthly := &Step{Slug: "m", Cadence: "monthly"}
	stamp("m", "2026-08-01")
	if ok, _ := r.cadenceDue(monthly); ok {
		t.Error("monthly は同じ月なら skip")
	}
	stamp("m", "2026-07-31")
	if ok, _ := r.cadenceDue(monthly); !ok {
		t.Error("monthly は月が変われば実行")
	}
	r.opt.ForceCadence = true
	stamp("w", "2026-08-19")
	if ok, _ := r.cadenceDue(weekly); !ok {
		t.Error("--force-cadence で間隔を無視する")
	}
}

func TestTailLines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 25; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString("\n")
		if i == 24 {
			b.WriteString("\n\x1b[31mred\x1b[0m\n")
		}
	}
	got := tailLines(b.String(), 20)
	if got[len(got)-2] != "red" {
		t.Errorf("ANSI を除いていない: %q", got)
	}
	for _, l := range got {
		if l == "" {
			t.Errorf("空行を落としていない: %q", got)
		}
	}
	if tailLines("", 20) != nil {
		t.Error("空の出力は空")
	}
}

func TestDescendants(t *testing.T) {
	procs := []procEntry{{10, 1, 10}, {11, 10, 10}, {12, 11, 99}, {13, 1, 13}, {14, 12, 99}}
	got := descendants(procs, 10)
	want := map[int]bool{11: true, 12: true, 14: true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("想定外の子孫 %d", p)
		}
	}
}

// blockingWriter は release されるまで Write を返さない（読まれない stderr の代わり）。
type blockingWriter struct{ release chan struct{} }

func (b *blockingWriter) Write(p []byte) (int, error) {
	<-b.release
	return len(p), nil
}

func TestDisplayQueueDoesNotBlock(t *testing.T) {
	bw := &blockingWriter{release: make(chan struct{})}
	q := newDisplayQueue(bw)
	done := make(chan struct{})
	go func() {
		for i := 0; i < displayQueueCap*3; i++ {
			_, _ = q.Write([]byte("line\n"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("表示先が詰まっていると Write が戻らない")
	}
	start := time.Now()
	q.flush()
	if time.Since(start) > displayFlushWait+500*time.Millisecond {
		t.Errorf("flush が上限を超えて待った: %v", time.Since(start))
	}
	close(bw.release)
}

// 残留ロックがある状態で並行に取りに行っても、取れるのは高々 1 つ。
func TestAcquireLockConcurrentReclaim(t *testing.T) {
	for round := 0; round < 20; round++ {
		dir := t.TempDir()
		lock := filepath.Join(dir, "lock.d")
		if err := os.MkdirAll(lock, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(lock, "pid"), []byte("999999\n"), 0o644)
		const workers = 8
		got := make(chan bool, workers)
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			go func() {
				d, _ := newDiag()
				<-start
				_, ok := acquireLockDir(lock, d)
				got <- ok
			}()
		}
		close(start)
		n := 0
		for i := 0; i < workers; i++ {
			if <-got {
				n++
			}
		}
		if n > 1 {
			t.Fatalf("round %d: %d 個が同時にロックを取った", round, n)
		}
		if _, err := os.Stat(lock + ".reclaim"); err == nil {
			t.Fatalf("round %d: lock.d.reclaim が残った", round)
		}
	}
}

func TestLockFreshWithoutPidIsKept(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "lock.d")
	_ = os.Mkdir(lock, 0o755)
	d, buf := newDiag()
	if _, ok := acquireLockDir(lock, d); ok {
		t.Fatal("pid の無い新しいロックを回収した")
	}
	if !strings.Contains(buf.String(), "pid=unknown") {
		t.Errorf("msg = %s", buf.String())
	}
	if _, err := os.Stat(lock); err != nil {
		t.Error("作成途中のロックを消した")
	}
}

func TestStaleReclaimDirIsCleared(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "lock.d")
	_ = os.Mkdir(lock, 0o755)
	_ = os.WriteFile(filepath.Join(lock, "pid"), []byte("999999\n"), 0o644)
	rec := lock + ".reclaim"
	_ = os.Mkdir(rec, 0o755)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(rec, old, old)
	d, buf := newDiag()
	got, ok := acquireLockDir(lock, d)
	if !ok || got != lock {
		t.Fatalf("古い lock.d.reclaim が残っていると取れない: %s", buf.String())
	}
	if _, err := os.Stat(rec); err == nil {
		t.Error("lock.d.reclaim が残った")
	}
	releaseLockDir(lock)
	if _, err := os.Stat(lock); err == nil {
		t.Error("自分のロックを消せていない")
	}
}

func TestReleaseLockKeepsOthers(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "lock.d")
	_ = os.Mkdir(lock, 0o755)
	_ = os.WriteFile(filepath.Join(lock, "pid"), []byte("999998\n"), 0o644)
	releaseLockDir(lock)
	if _, err := os.Stat(lock); err != nil {
		t.Error("他人のロックを消した")
	}
}
