// Package runner は myroutine の Go 版 runner（ADR 0006）。ステップ定義は zsh のまま
// で、lib/prelude.zsh を挟んで実行する。境界は「環境変数で渡し、fd 3 の行で返す」だけ。
//
// CLI・出力・終了コード・環境変数・状態ファイルの形式は zsh 版（bin/myroutine）と互換。
// 人が読む出力（既定）はレンダラ（internal/format・internal/render）の形にする。
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rengotaku/myroutine/internal/format"
	"github.com/rengotaku/myroutine/lib"
)

// forceExitAfter はシグナルを受けてから、通常の後片付けを待つ上限。
const forceExitAfter = 10 * time.Second

type result struct {
	status string // ok / skip / fail / timeout / disabled
	rc     int
	ms     int64
	note   string
	ended  string
}

// Runner は 1 回の起動分の状態。
type Runner struct {
	opt Options
	cfg Config

	zsh     string
	prelude string
	tmpDir  string
	lockDir string

	tty        *terminal
	diag       *diag
	stdout     *errWriter
	displayErr io.Writer
	em         *emitter
	log        *dailyLog
	procs      *running

	ctx    context.Context
	cancel context.CancelFunc

	intrMu  sync.Mutex
	intrSig syscall.Signal
	exitMu  sync.Mutex
	exited  bool

	steps   []*Step
	results map[int]*result
	actions []item
	todos   []item
	infos   []item
	summary map[int]string
	tails   map[int][]string

	evaluated []string
	streak    map[string]int
	newIDs    map[string]bool
	resolved  []string

	fullRun bool
	lastRC  int
	posOf   map[int]int
	total   int
	runT0   time.Time
}

// Main は CLI の入口。終了コードを返す。
func Main(args []string) int {
	opt, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		if ue, ok := err.(*usageError); ok && ue.showUsage {
			fmt.Fprintln(os.Stderr, usageLine)
		}
		return 1
	}
	if opt.Help {
		fmt.Println(usageLine)
		return 0
	}
	cfg, err := loadConfig(opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if opt.JSON || opt.JSONL {
		cfg.NoColor = true
	}

	r := &Runner{
		opt:     opt,
		cfg:     cfg,
		procs:   newRunning(),
		results: map[int]*result{},
		summary: map[int]string{},
		tails:   map[int][]string{},
		streak:  map[string]int{},
		newIDs:  map[string]bool{},
		posOf:   map[int]int{},
		runT0:   time.Now(),
	}
	r.stdout = &errWriter{w: os.Stdout}
	r.displayErr = &lossyWriter{w: os.Stderr}
	r.diag = &diag{w: r.displayErr, color: !cfg.NoColor && isTTY(os.Stderr)}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.watchSignals()

	code := r.main()
	r.finalCleanup()
	return code
}

func (r *Runner) main() int {
	if err := os.MkdirAll(r.cfg.LogDir, 0o755); err != nil {
		r.diag.line("ERROR: 状態ディレクトリを作成できません: " + r.cfg.LogDir)
		return 1
	}

	sub := ""
	if len(r.opt.Args) > 0 {
		sub = r.opt.Args[0]
	}
	if sub == "last" {
		return r.showLast()
	}

	if err := r.setupExec(); err != nil {
		r.diag.line(err.Error())
		return 1
	}
	if !r.discover() {
		return r.exitOr(1)
	}

	var seq []int
	label := "all"
	switch sub {
	case "ls":
		r.showList()
		return 0
	case "":
		for _, s := range r.steps {
			seq = append(seq, s.N)
		}
		r.fullRun = true
	default:
		var ok bool
		seq, ok = r.parseSteps(r.opt.Args)
		if !ok {
			fmt.Fprintln(os.Stderr, usageLine)
			return 1
		}
		label = joinInts(seq, ",")
	}

	if !r.acquireLock() {
		return 1
	}
	r.tty = openTerminal()
	if err := r.setupRunOutputs(label, seq); err != nil {
		r.diag.line(err.Error())
		return 1
	}
	code := r.runSequence(seq)
	return r.exitOr(code)
}

// exitOr は中断されていれば 128+シグナル番号を、そうでなければ code を返す。
func (r *Runner) exitOr(code int) int {
	if sig := r.interruptSignal(); sig != 0 {
		return 128 + int(sig)
	}
	return code
}

// setupExec はステップを動かす準備（zsh の場所、一時ディレクトリ、prelude の書き出し）。
func (r *Runner) setupExec() error {
	r.zsh = findZsh()
	if r.zsh == "" {
		return fmt.Errorf("ERROR: zsh が見つかりません")
	}
	dir, err := os.MkdirTemp("", "myroutine-")
	if err != nil {
		return fmt.Errorf("ERROR: 一時ディレクトリを作成できません")
	}
	r.tmpDir = dir
	r.prelude = filepath.Join(dir, "prelude.zsh")
	if err := os.WriteFile(r.prelude, []byte(lib.Prelude), 0o600); err != nil {
		return fmt.Errorf("ERROR: prelude を書き出せません: %v", err)
	}
	return nil
}

// findZsh は zsh 版の shebang と同じ /bin/zsh を優先する。
func findZsh() string {
	if st, err := os.Stat("/bin/zsh"); err == nil && !st.IsDir() {
		return "/bin/zsh"
	}
	if p, err := exec.LookPath("zsh"); err == nil {
		return p
	}
	return ""
}

func (r *Runner) tmpPath(name string) string { return filepath.Join(r.tmpDir, name) }

// discover は order を読み、各ステップの宣言を検証する。
func (r *Runner) discover() bool {
	if st, err := os.Stat(r.cfg.StepsDir); err != nil || !st.IsDir() {
		r.diag.line("ERROR: ステップディレクトリがありません: " + r.cfg.StepsDir)
		return false
	}
	slugs, ok := readOrder(r.cfg.StepsDir, r.diag)
	if !ok {
		return false
	}
	for i, slug := range slugs {
		if r.ctx.Err() != nil {
			return false
		}
		s, ok := r.readMeta(i+1, slug, filepath.Join(r.cfg.StepsDir, slug+".sh"))
		if !ok {
			return false
		}
		r.steps = append(r.steps, s)
	}
	return true
}

func (r *Runner) step(n int) *Step { return r.steps[n-1] }

// ─────────────────────────────────────────────────────────────────
// 多重起動ロック
// ─────────────────────────────────────────────────────────────────

// acquireLock は zsh 版と同じ mkdir lock.d ＋ pid ＋ stale 回収の手順で排他する
// （zsh 版と共存する間、新旧が互いを排他できるようにするため）。取れなければ中止する。
func (r *Runner) acquireLock() bool {
	if r.opt.NoLock {
		return true
	}
	lock := filepath.Join(r.cfg.LogDir, "lock.d")
	for attempt := 0; attempt < 2; attempt++ {
		if err := os.Mkdir(lock, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(lock, "pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)
			r.lockDir = lock
			return true
		}
		other := ""
		if b, err := os.ReadFile(filepath.Join(lock, "pid")); err == nil {
			other = trimSpace(string(b))
		}
		if other != "" && pidAlive(other) {
			r.diag.line(fmt.Sprintf("ERROR: myroutine は既に実行中です (pid=%s)", other))
			return false
		}
		// 単に削除すると、2 プロセスが同時に残留を検知したとき、片方が取り直した
		// 新しいロックをもう片方が消してしまう。rename は原子的なので、退避に成功した
		// 1 プロセスだけが回収する。
		shown := other
		if shown == "" {
			shown = "unknown"
		}
		r.diag.line(fmt.Sprintf("警告: 残留ロックを回収します (pid=%s)", shown))
		tomb := fmt.Sprintf("%s.stale.%d", lock, os.Getpid())
		if err := os.Rename(lock, tomb); err == nil {
			_ = os.RemoveAll(tomb)
		}
	}
	r.diag.line("ERROR: ロックを取得できません: " + lock)
	return false
}

func pidAlive(s string) bool {
	var pid int
	if _, err := fmt.Sscanf(s, "%d", &pid); err != nil || pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	// EPERM は「存在するが権限が無い」。生きている側（中止）に倒す。
	return err == nil || err == syscall.EPERM
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

// ─────────────────────────────────────────────────────────────────
// シグナルと後片付け
// ─────────────────────────────────────────────────────────────────

// watchSignals は INT / TERM / HUP を受けて実行中のステップを止め、zsh 版と同じ
// 終了コード（130 / 143 / 129）で終わらせる。SIGPIPE は通知で受けて捨て、書き込みの
// 失敗として扱う（ハンドラを置いているので、子には既定の扱いのまま渡る）。
func (r *Runner) watchSignals() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	go func() {
		for s := range ch {
			sig, _ := s.(syscall.Signal)
			if sig == syscall.SIGPIPE {
				continue
			}
			if r.interruptSignal() != 0 {
				// 2 回目は待たずに打ち切る
				r.forceExit()
			}
			r.noteInterrupt(sig)
			go func() {
				time.Sleep(forceExitAfter)
				r.forceExit()
			}()
		}
	}()
}

func (r *Runner) noteInterrupt(sig syscall.Signal) {
	r.intrMu.Lock()
	if r.intrSig == 0 {
		r.intrSig = sig
	}
	r.intrMu.Unlock()
	r.cancel()
}

func (r *Runner) interruptSignal() syscall.Signal {
	r.intrMu.Lock()
	defer r.intrMu.Unlock()
	return r.intrSig
}

func (r *Runner) interrupted() bool { return r.interruptSignal() != 0 }

// forceExit は通常の流れを待たずに、実行中のステップを止めて後片付けして終わる。
func (r *Runner) forceExit() {
	code := r.exitOr(1)
	r.procs.killAll()
	r.finalCleanup()
	os.Exit(code)
}

// readerGone は stdout の読み手が消えたときの終わり方。stdout には何も届かないので
// stderr に 1 行だけ残し、実行中のステップを畳んで終わる。
func (r *Runner) readerGone() {
	fmt.Fprintln(r.displayErr, "myroutine: 出力先が閉じられました。実行を打ち切ります。")
	r.procs.killAll()
	r.finalCleanup()
	os.Exit(141)
}

// finalCleanup はロックと一時ディレクトリを消す。何度呼んでもよい。
func (r *Runner) finalCleanup() {
	r.exitMu.Lock()
	defer r.exitMu.Unlock()
	if r.exited {
		return
	}
	r.exited = true
	if r.em != nil && r.em.events != nil {
		_ = r.em.events.Close()
	}
	r.log.close()
	if r.lockDir != "" {
		_ = os.RemoveAll(r.lockDir)
	}
	if r.tmpDir != "" {
		_ = os.RemoveAll(r.tmpDir)
	}
}

// ─────────────────────────────────────────────────────────────────
// 実行の出力先
// ─────────────────────────────────────────────────────────────────

// setupRunOutputs は日次ログ・イベントファイル・stdout の出し分けを用意し、
// run イベントを出す。
//
//	既定     stdout = レンダラ（人向け）、stderr = runner の進捗
//	--jsonl  stdout = JSONL イベント、stderr = runner の進捗
//	--json   stdout = 終了後に JSON を 1 個、stderr = レンダラと runner の進捗
func (r *Runner) setupRunOutputs(label string, seq []int) error {
	now := time.Now()
	r.log = openDailyLog(filepath.Join(r.cfg.LogDir, now.Format("2006-01-02")+".txt"))
	_, _ = r.log.Write([]byte(fmt.Sprintf("=== myroutine %s %s ===\n", label, now.Format("2006-01-02 15:04:05"))))
	r.diag.log = r.log

	events, err := os.OpenFile(filepath.Join(r.cfg.LogDir, "last-events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		events = nil
	}
	r.em = &emitter{events: events, logSt: format.NewState(false), log: r.log}
	switch {
	case r.opt.JSONL:
		r.em.jsonl = r.stdout
	case r.opt.JSON:
		r.em.rend = newRenderSink(r.displayErr, os.Stderr, r.cfg.NoColor || !isTTY(os.Stderr))
	default:
		r.em.rend = newRenderSink(r.stdout, os.Stdout, r.cfg.NoColor)
	}

	// pos は今回の実行対象のうち無効化されていないステップに、実行順ではなく
	// 「選択されたシーケンス上の位置」を先に割り当てたもの。並列グループは
	// ゲート判定を先に済ませるため、発行順で振ると skip が先に番号を取ってしまう。
	for _, n := range seq {
		if r.step(n).Disabled != "" {
			continue
		}
		r.total++
		r.posOf[n] = r.total
	}
	mode := "partial"
	if r.fullRun {
		mode = "all"
	}
	r.emit("run", runEvent(mode, r.now(), r.total, r.log.path))
	return nil
}

// emit はイベントを出し、stdout の読み手が消えていたら打ち切る。
func (r *Runner) emit(ev string, payload any) {
	if err := r.em.emit(ev, payload); err != nil || r.stdout.failed() {
		r.readerGone()
	}
}
