package runner

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rengotaku/myroutine/internal/eventlog"
)

type gateResult int

const (
	gateRun  gateResult = iota // 実行してよい
	gateSkip                   // skip として記録した
	gateErr                    // エラーとして記録した
)

// runSequence は指定された順にステップを実行する。step_parallel の連続範囲は 1 グループ
// として同時に実行する（ADR 0004）。戻り値は全体の終了コード（0 / 1）。
func (r *Runner) runSequence(seq []int) int {
	overall := 0
	for i := 0; i < len(seq); {
		if r.interrupted() {
			return 1
		}
		s := r.step(seq[i])
		if s.Disabled != "" {
			r.handleDisabled(s)
			i++
			continue
		}
		if s.Parallel {
			group := []*Step{s}
			j := i + 1
			for ; j < len(seq); j++ {
				m := r.step(seq[j])
				if m.Disabled != "" {
					// 無効ステップは「居ない」ものとして飛ばす。両隣が parallel なら
					// グループの連続性はこれで壊れない。
					r.handleDisabled(m)
					continue
				}
				if !m.Parallel {
					break
				}
				group = append(group, m)
			}
			// 1 つだけなら並列にする意味がない。逐次と同じ経路で流す。
			var failed bool
			if len(group) == 1 {
				failed = r.runStep(s)
			} else {
				failed = r.runParallelGroup(group)
			}
			if failed {
				overall = 1
			}
			// parallel ステップは step_abort_on_fail を宣言できない（宣言の検証で弾く）
			i = j
			continue
		}
		if r.runStep(s) {
			overall = 1
		}
		if r.interrupted() {
			return 1
		}
		if !r.maybeAbort(s) {
			// 後続ステップを実行していないため要対応の集合が欠けている。
			// このまま記録すると次回に既存の要対応が誤って「解消」扱いになる。
			r.fullRun = false
			r.finish()
			return 1
		}
		i++
	}
	if r.interrupted() {
		return 1
	}
	r.finish()
	return overall
}

// handleDisabled は無効ステップを実行せず、理由と disabled を記録する。
// 全体実行では「居ない」ものとして何も出さない。指定実行は明示指定なので、
// なぜ実行しなかったかと戻し方を出す。
func (r *Runner) handleDisabled(s *Step) {
	if !r.fullRun {
		r.diag.dim("⊘ 無効: " + s.Disabled)
		r.diag.line("    戻すには steps.d/" + s.Slug + ".sh の step_disabled を消す")
	}
	r.record(s, "disabled", 0, 0, s.Disabled, "")
}

// interactiveHeader は --jsonl で step_interactive のステップの見出しを stderr に出す。
// 生出力が stderr に流れるので、どのステップの出力なのかを示す。既定と --json では
// レンダラの開始行がこの役をする。
func (r *Runner) interactiveHeader(s *Step) {
	if !r.opt.JSONL || !s.Interactive {
		return
	}
	r.diag.line("")
	r.diag.line(r.diag.paint(cCyan+cBold, fmt.Sprintf("━━━━━ [%d/%d] %s ━━━━━━━━━━━━━━━━━━━━━", r.posOf[s.N], r.total, s.Name)))
}

// runStep は逐次のステップを 1 つ実行する。戻り値は critical の失敗かどうか。
func (r *Runner) runStep(s *Step) bool {
	r.interactiveHeader(s)
	switch r.gate(s) {
	case gateErr:
		r.lastRC = 1
		return true
	case gateSkip:
		r.lastRC = 0
		return false
	}
	r.emit("start", r.startEvent(s))
	_ = os.WriteFile(r.outPath(s.N), nil, 0o600)
	res := r.execStep(r.ctx, s, false)
	r.lastRC = res.rc
	if res.interrupted || r.interrupted() {
		// 中断でも、そこまでの出力は日次ログに残す（一時ディレクトリは消える）。
		// 結果の記録と履歴の更新はしない。
		r.logStepOutput(s, true)
		return false
	}
	return r.finalize(s, res)
}

// gate は実行前のふるい。前提・実行間隔・破壊的ステップの確認を 1 か所に集める。
// 逐次と並列で判定を二重に書かないための分離で、skip / エラーの記録はここで済ませる。
func (r *Runner) gate(s *Step) gateResult {
	missing, err := r.checkRequires(s)
	if err != nil {
		r.diag.err("前提条件の確認に失敗しました")
		r.record(s, "fail", 1, 0, "requires check failed", "")
		return gateErr
	}
	if missing != "" {
		// 画面にはレンダラが結果行「⊘ [位置/全件] 名前  skip（〇〇 が無い）」として出す
		r.record(s, "skip", 0, 0, missing, "")
		return gateSkip
	}
	// 実行間隔は全体実行のときだけ見る（指定実行は明示指定なので判定しない）
	if r.fullRun {
		if due, why := r.cadenceDue(s); !due {
			// 途中には出さない。末尾の「今日は対象外」一覧に名前と次回の日付つきで出る
			r.cadence[s.N] = r.cadenceSkipView(s)
			r.record(s, "skip", 0, 0, "cadence: "+why, "")
			return gateSkip
		}
	}
	if s.Destructive {
		if r.opt.DryRun {
			r.record(s, "skip", 0, 0, "dry-run (destructive)", "")
			return gateSkip
		}
		// ステップ側が confirm を呼び忘れても本体が埋める
		if !r.confirm("「" + s.Name + "」は破壊的な操作を含みます。実行しますか?") {
			r.record(s, "skip", 0, 0, "declined (destructive)", "")
			return gateSkip
		}
	}
	return gateRun
}

// checkRequires は前提のうち欠けているものを「コマンド: x, パス: y」の形で返す。
// ゲートの時点で prelude の requires モードがステップを source してから確かめる
// （zsh 版と同じく、ステップ内で定義した関数・トップレベルで足した PATH・前の
// ステップが入れたコマンドが見える）。結果は fd 3 で返る。
func (r *Runner) checkRequires(s *Step) (string, error) {
	if !s.HasRequires {
		return "", nil
	}
	out, err := os.CreateTemp(r.tmpDir, "requires-*")
	if err != nil {
		return "", err
	}
	defer out.Close()
	cmd := exec.CommandContext(r.ctx, r.zsh, r.prelude, "requires", s.File)
	cmd.Env = r.stepEnv(s.N, s.Slug, false)
	cmd.Stderr = r.displayErr
	cmd.ExtraFiles = []*os.File{out}
	cmd.WaitDelay = killGrace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		killTree(cmd.Process.Pid, cmd.Process.Pid)
		return nil
	}
	if err := cmd.Run(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		return "", err
	}
	var cmds, paths []string
	done := false
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, "\t")
		switch f[0] {
		case "D":
			done = true
		case "E":
			return "", fmt.Errorf("source failed")
		case "Q":
			if len(f) < 3 {
				continue
			}
			if f[1] == "cmd" {
				cmds = append(cmds, "コマンド: "+unescape(f[2]))
			} else {
				paths = append(paths, "パス: "+unescape(f[2]))
			}
		}
	}
	if !done {
		return "", fmt.Errorf("requires check incomplete")
	}
	return strings.Join(append(cmds, paths...), ", "), nil
}

// confirm は runner 自身の確認（破壊的ステップ・abort_on_fail）。判定順は zsh 版と同じ
// （dry-run は中止 → -y なら続行 → 端末で y/N）。端末が無ければ中止側に倒す。
// stdin ではなく /dev/tty を読む（--jsonl | myroutine-render の構成でも読めるように）。
func (r *Runner) confirm(msg string) bool {
	if r.opt.DryRun {
		r.diag.dim("[dry-run] 確認を求める: " + msg)
		return false
	}
	if r.opt.Yes {
		return true
	}
	r.diag.write("  " + r.diag.paint(cYellow+cBold, msg+" [y/N]:") + " ")
	b, ok := r.tty.readKey(r.ctx)
	r.diag.write("\n")
	return ok && (b == 'y' || b == 'Y')
}

// finalize は実行結果を分類して記録する。逐次・並列に共通の唯一の出口。
// 戻り値は critical の失敗かどうか（呼び出し側が全体の失敗として拾う）。
func (r *Runner) finalize(s *Step, res execResult) bool {
	cr := drainChan(r.chanPath(s.N))
	r.actions = append(r.actions, cr.actions...)
	r.checkActionCommands(s, cr.actions)
	r.todos = append(r.todos, cr.todos...)
	r.infos = append(r.infos, cr.infos...)
	if cr.summary != "" {
		r.summary[s.N] = cr.summary
	}
	if cr.hasVerify {
		if cr.runRC == 0 && cr.verifyRC != 0 {
			r.diag.err("step_run は成功したが step_verify が失敗した (検証を優先して失敗扱い)")
		} else if cr.runRC != 0 && cr.verifyRC == 0 {
			r.diag.warn(fmt.Sprintf("step_run は rc=%d だが step_verify は通過した (検証を優先して成功扱い)", cr.runRC))
		}
	}

	out := r.logStepOutput(s, false)
	// 成功したステップの出力は保持しない（失敗時だけ末尾を tail として載せる）
	if res.rc != 0 || res.timedOut {
		r.tails[s.N] = tailLines(string(out), 20)
	}

	failed := false
	switch {
	// タイムアウトは終了コードより先に見る。TERM を捕まえて 0 で終わるステップも
	// 打ち切られたのであって、成功ではない（評価済みにも入れず、last-run も進めない）。
	// 終了コードの値で内部事情を判別しない（ステップが同じ値を返しうる）
	case res.timedOut:
		r.diag.err(fmt.Sprintf("%s がタイムアウトしました (%ds)", s.Name, res.limit))
		r.record(s, "timeout", res.rc, res.ms, fmt.Sprintf("%ds", res.limit), res.ended)
		failed = true
	case res.rc == 0:
		r.checkSummary(s)
		r.record(s, "ok", 0, res.ms, "", res.ended)
	case cr.sourceFailed:
		r.diag.err("ステップの読み込みに失敗しました")
		r.record(s, "fail", res.rc, res.ms, "source failed", res.ended)
		failed = true
	case cr.runUndefined:
		r.diag.err("step_run が定義されていません")
		r.record(s, "fail", res.rc, res.ms, "step_run undefined", res.ended)
		failed = true
	default:
		r.diag.err(fmt.Sprintf("%s に失敗しました (rc=%d)", s.Name, res.rc))
		r.record(s, "fail", res.rc, res.ms, "", res.ended)
		failed = true
	}
	// dry-run は実行予定（run が出す [dry-run] の行）を見せるのが目的なので、
	// 成否に関係なくステップの出力をそのまま表示する。レンダラは成功時に出力を畳む。
	if r.opt.DryRun && !s.Interactive {
		r.em.raw(string(out))
		if r.stdout.failed() {
			r.readerGone()
		}
	}

	// critical は「中断する」フラグではなく「続行するが要対応として記録する」フラグ
	if failed && s.Critical {
		r.actions = append(r.actions, item{s.N, s.Slug + ".__critical", s.Name + " が失敗（critical）— 要確認"})
		return true
	}
	return false
}

// logStepOutput はステップの出力（out.N）を見出し付きで日次ログへ書き、中身を返す。
func (r *Runner) logStepOutput(s *Step, interrupted bool) []byte {
	out, _ := os.ReadFile(r.outPath(s.N))
	head := fmt.Sprintf("\n── [%d] %s ──\n", s.N, s.Name)
	if interrupted {
		head = fmt.Sprintf("\n── [%d] %s（中断）──\n", s.N, s.Name)
	}
	_, _ = r.log.Write(append([]byte(head), out...))
	return out
}

// tailLines は末尾 n 行を返す。空行は落とし、ANSI は除く。
func tailLines(out string, n int) []string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	var res []string
	for _, l := range lines {
		if l != "" {
			res = append(res, stripANSI(l))
		}
	}
	return res
}

// record は 1 ステップの結果を記録し、step イベントを出す。全ステップの結果が
// 通る唯一の出口（skip / fail / disabled を含む）。
func (r *Runner) record(s *Step, status string, rc int, ms int64, note, ended string) {
	if ended == "" {
		ended = hmsNow()
	}
	r.results[s.N] = &result{status: status, rc: rc, ms: ms, note: note, ended: ended}
	// 失敗・タイムアウト・skip は要対応の有無を確認しきれていない（「見ていない」側）。
	if status == "ok" {
		r.evaluated = append(r.evaluated, s.Slug)
		// dry-run は実際には実行していないので、次回の間隔判定を進めてはいけない。
		if !r.opt.DryRun {
			r.writeStamp(s.Slug)
		}
	}
	tail := r.tails[s.N]
	if tail == nil {
		tail = []string{}
	}
	ev := &eventlog.StepEvent{
		N: s.N, Name: s.Name, Slug: s.Slug, Status: status, RC: rc, DurationMs: ms,
		Summary: r.summary[s.N], Note: note, Tail: tail, Pos: r.posOf[s.N], EndedAt: ended,
	}
	if c, ok := r.cadence[s.N]; ok && status == "skip" {
		ev.Cadence, ev.LastRun, ev.NextDue = c.label, c.last, c.next
	}
	r.emit("step", ev)
}

func (r *Runner) startEvent(s *Step) *eventlog.StartEvent {
	return &eventlog.StartEvent{N: s.N, Pos: r.posOf[s.N], Name: s.Name, Slug: s.Slug, At: hmsNow()}
}

// maybeAbort は step_abort_on_fail のステップが失敗したら続行可否を尋ねる。
// 戻り値 true = 続行 / false = 中断。
func (r *Runner) maybeAbort(s *Step) bool {
	if !s.Abort || r.lastRC == 0 {
		return true
	}
	if r.confirm("「" + s.Name + "」が失敗しました。続行しますか?") {
		r.diag.info("続行します...")
		return true
	}
	r.diag.line("  中断します。")
	// 中断は「クリーンに終わった」ではない。サマリだけを見る人に伝わるよう残す。
	r.actions = append(r.actions, item{s.N, s.Slug + ".__aborted", s.Name + " の失敗により中断した — 原因を解消して再実行する"})
	return false
}

// runParallelGroup は並列グループを実行する。背景に出すのは実行だけで、ゲートと記録は
// メインの goroutine で行う。出力と記録はステップ番号順（完了順ではない）。
func (r *Runner) runParallelGroup(group []*Step) bool {
	overall := false
	var due []*Step
	for _, s := range group {
		_ = os.WriteFile(r.outPath(s.N), nil, 0o600)
		switch r.gate(s) {
		case gateErr:
			overall = true
		case gateRun:
			due = append(due, s)
		}
	}

	results := map[int]execResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, r.cfg.ParallelMax)
	for _, s := range due {
		select {
		case sem <- struct{}{}:
		case <-r.ctx.Done():
		}
		if r.ctx.Err() != nil {
			break
		}
		// 実行に入る瞬間に流す。start はメインの goroutine から出す。
		r.emit("start", r.startEvent(s))
		wg.Add(1)
		go func(s *Step) {
			defer wg.Done()
			res := r.execStep(r.ctx, s, true)
			mu.Lock()
			results[s.N] = res
			mu.Unlock()
			<-sem
		}(s)
	}
	wg.Wait()
	if r.interrupted() {
		// 中断でも、実行に入ったステップの出力は番号順に日次ログへ残す
		for _, s := range group {
			if _, ok := results[s.N]; ok {
				r.logStepOutput(s, true)
			}
		}
		return overall
	}

	for _, s := range group {
		res, ok := results[s.N]
		if !ok {
			continue // ゲートで skip / エラーとして記録済み
		}
		r.lastRC = res.rc
		if r.finalize(s, res) {
			overall = true
		}
	}
	return overall
}

// finish は履歴との比較・結果の出力・履歴の追記を行う。
func (r *Runner) finish() {
	r.analyzeHistory()
	sum := r.summaryEvent()
	if r.opt.JSON {
		r.writeJSON(sum)
	}
	r.emit("summary", sum)
	// dry-run は実際の状態ではないので履歴を汚さない。
	// 部分実行も要対応の集合が欠けるため記録しない。
	if r.fullRun && !r.opt.DryRun {
		r.appendHistory()
	}
	// 要対応の履歴は部分実行でも残す（cleared は評価できたステップだけが対象）。
	if !r.opt.DryRun {
		r.appendLedger()
	}
}

func (r *Runner) actionIDs() []string {
	ids := make([]string, 0, len(r.actions))
	for _, a := range r.actions {
		ids = append(ids, a.id)
	}
	return ids
}

func (r *Runner) summaryEvent() *eventlog.SummaryEvent {
	sum := &eventlog.SummaryEvent{
		WallMs:   time.Since(r.runT0).Milliseconds(),
		Actions:  []eventlog.ActionItem{},
		Todos:    []eventlog.TodoItem{},
		Infos:    []eventlog.InfoItem{},
		Resolved: append([]string{}, r.resolved...),
		Disabled: []eventlog.DisabledItem{},
	}
	for _, a := range r.actions {
		streak := r.streak[a.id]
		if streak == 0 {
			streak = 1
		}
		sum.Actions = append(sum.Actions, eventlog.ActionItem{Step: a.step, ID: a.id, Text: a.text, StreakDays: streak, New: r.newIDs[a.id]})
	}
	for _, t := range r.todos {
		sum.Todos = append(sum.Todos, eventlog.TodoItem{Step: t.step, ID: t.id, Text: t.text})
	}
	for _, i := range r.infos {
		sum.Infos = append(sum.Infos, eventlog.InfoItem{Step: i.step, ID: i.id, Text: i.text})
	}
	for _, s := range r.steps {
		if s.Disabled != "" {
			sum.Disabled = append(sum.Disabled, eventlog.DisabledItem{N: s.N, Name: s.Name, Slug: s.Slug, Reason: s.Disabled})
		}
	}
	return sum
}

// writeJSON は --json の結果を stdout に 1 個出す。
func (r *Runner) writeJSON(sum *eventlog.SummaryEvent) {
	out := eventlog.JSONResult{
		Steps:    []eventlog.JSONStep{},
		Actions:  sum.Actions,
		Todos:    sum.Todos,
		Infos:    sum.Infos,
		Resolved: sum.Resolved,
		Disabled: sum.Disabled,
	}
	for _, s := range r.steps {
		res, ok := r.results[s.N]
		if !ok {
			continue
		}
		out.Steps = append(out.Steps, eventlog.JSONStep{N: s.N, Name: s.Name, Slug: s.Slug, Status: res.status, RC: res.rc, DurationMs: res.ms, Note: res.note})
	}
	b, err := eventlog.Encode(out)
	if err != nil {
		r.diag.err("JSON を組み立てられません: " + err.Error())
		return
	}
	if _, err := r.stdout.Write(append(b, '\n')); err != nil {
		r.readerGone()
	}
}

func runEvent(mode string, now time.Time, total int, log string) *eventlog.RunEvent {
	return &eventlog.RunEvent{Mode: mode, StartedAt: now.Format("2006-01-02T15:04:05-07:00"), Total: total, Log: log}
}

func joinInts(ns []int, sep string) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, sep)
}
