package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// execResult は 1 ステップを実行した結果。
type execResult struct {
	rc       int
	ms       int64
	timedOut bool
	// interrupted は実行全体の中断（runner へのシグナル、または前景を渡した
	// ステップが端末からの Ctrl-C で落ちたこと）を表す。
	interrupted bool
	limit       int
	ended       string // HH:MM:SS
}

// stepEnv はステップに渡す環境変数。入力はこれだけで、出力は fd 3 の行で返る。
func (r *Runner) stepEnv(n int, slug string, inParallel bool) []string {
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	env := os.Environ()
	env = append(env,
		"MR_DRY_RUN="+b(r.opt.DryRun),
		"MR_YES="+b(r.opt.Yes),
		"MR_IN_PARALLEL="+b(inParallel),
		"MR_STEP_NUM="+strconv.Itoa(n),
		"MR_STEP_SLUG="+slug,
	)
	if r.opt.JSON || r.opt.JSONL {
		env = append(env, "MR_NO_COLOR=1")
	} else {
		env = append(env, "MR_NO_COLOR=")
	}
	return env
}

func (r *Runner) limitOf(s *Step) int {
	if s.Timeout != nil {
		return *s.Timeout
	}
	return r.cfg.Timeout
}

func (r *Runner) chanPath(n int) string { return r.tmpPath("chan." + strconv.Itoa(n)) }
func (r *Runner) outPath(n int) string  { return r.tmpPath("out." + strconv.Itoa(n)) }

// execStep はステップを prelude の exec モードで実行し、終わるまで待つ。
//
// ステップは必ず独自のプロセスグループで起動する（タイムアウトや Ctrl-C のときに
// runner 自身を巻き込まないため）。逐次ステップで runner が端末の前景にいるときは、
// そのグループに前景を渡し、どの終わり方でも runner へ戻す。
// 出力はファイルに直接つなぐ。表示（step_interactive）は別の goroutine がファイルを
// 追うので、表示先の障害がステップの書き込みを止めない。
func (r *Runner) execStep(ctx context.Context, s *Step, inParallel bool) execResult {
	limit := r.limitOf(s)
	res := execResult{limit: limit}
	t0 := time.Now()

	fail := func(msg string) execResult {
		r.diag.err(msg)
		res.rc = 1
		res.ms = time.Since(t0).Milliseconds()
		res.ended = hmsNow()
		return res
	}

	chanFile, err := os.OpenFile(r.chanPath(s.N), os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_APPEND, 0o600)
	if err != nil {
		return fail("チャネルを作成できません: " + err.Error())
	}
	defer chanFile.Close()
	outFile, err := os.OpenFile(r.outPath(s.N), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fail("出力ファイルを作成できません: " + err.Error())
	}
	defer outFile.Close()

	cmd := exec.Command(r.zsh, r.prelude, "exec", s.File)
	cmd.Env = r.stepEnv(s.N, s.Slug, inParallel)
	cmd.Stdout = outFile
	cmd.Stderr = outFile
	cmd.ExtraFiles = []*os.File{chanFile}
	// 出力はファイル直結なので通常はコピー用の goroutine を持たないが、
	// パイプを掴んだまま残る孤児がいても Wait が戻るよう上限を置く。
	cmd.WaitDelay = killGrace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var ho *handover
	if !inParallel {
		cmd.Stdin = os.Stdin
		ho = r.tty.prepareHandover()
		if ho != nil {
			cmd.SysProcAttr.Foreground = true
			cmd.SysProcAttr.Ctty = int(r.tty.fd())
		}
	}

	var follow *follower
	if s.Interactive {
		follow = startFollower(r.outPath(s.N), r.displayErr)
	}

	if err := cmd.Start(); err != nil {
		ho.restore()
		follow.stop()
		return fail("ステップを起動できません: " + err.Error())
	}
	pid := cmd.Process.Pid
	pgid := pid
	r.procs.add(pid, pgid)
	defer r.procs.remove(pid)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// タイムアウトは実行全体の ctx から派生させる。親が取り消されたとき（中断）と
	// 期限が来たとき（タイムアウト）を、どちらが起きたかで区別する。
	// 打ち切りが起きたかどうかは終了コードではなくここで決める（ステップ自身が
	// 124 や 143 を返すこともあるため、値で区別してはいけない）。
	stepCtx, cancel := ctx, context.CancelFunc(func() {})
	if limit > 0 {
		stepCtx, cancel = context.WithTimeout(ctx, time.Duration(limit)*time.Second)
	}
	defer cancel()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-stepCtx.Done():
		if ctx.Err() != nil {
			res.interrupted = true
		} else {
			res.timedOut = true
		}
		killTree(pid, pgid)
		waitErr = waitReaped(done)
	}
	ho.restore()
	follow.stop()

	res.ms = time.Since(t0).Milliseconds()
	res.ended = hmsNow()
	if errors.Is(waitErr, errReapTimeout) {
		// 回収を待ちきれなかった。ProcessState は Wait 側の goroutine がまだ
		// 書きうるので読まない。
		res.rc = 128 + int(syscall.SIGKILL)
		return res
	}
	res.rc = exitCodeOf(cmd.ProcessState, waitErr)
	// 前景を渡したステップが端末からの Ctrl-C で落ちたとき、SIGINT はステップの
	// グループにしか届いていない。runner に届いたのと同じく実行全体を中断する。
	if ho != nil && signaledBy(cmd.ProcessState, syscall.SIGINT) {
		res.interrupted = true
		r.noteInterrupt(syscall.SIGINT)
		// 同じグループに残った子（SIGINT を無視したもの）も落とす
		killTree(pid, pgid)
	}
	return res
}

var errReapTimeout = errors.New("reap timeout")

// waitReaped は止めたステップが回収されるのを、上限つきで待つ。
func waitReaped(done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(reapGrace):
		return errReapTimeout
	}
}

func hmsNow() string { return time.Now().Format("15:04:05") }

// chanResult はステップがチャネル（fd 3）に書いた内容。
type chanResult struct {
	actions, todos, infos []item
	summary               string
	hasVerify             bool
	runRC, verifyRC       int
	sourceFailed          bool
	runUndefined          bool
}

type item struct {
	step int
	id   string
	text string
}

// unescape は %25 / %0A / %09 を元に戻す。% を先に退避した符号化なので、
// 左から 1 つずつ読めば一意に戻る。
func unescape(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			switch s[i+1 : i+3] {
			case "25":
				b.WriteByte('%')
				i += 2
				continue
			case "0A":
				b.WriteByte('\n')
				i += 2
				continue
			case "09":
				b.WriteByte('\t')
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// drainChan はチャネルファイルを読む。壊れた行は読み飛ばす。
func drainChan(path string) chanResult {
	var cr chanResult
	data, err := os.ReadFile(path)
	if err != nil {
		return cr
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 4)
		get := func(i int) string {
			if i < len(f) {
				return f[i]
			}
			return ""
		}
		num, _ := strconv.Atoi(get(1))
		switch f[0] {
		case "A":
			cr.actions = append(cr.actions, item{num, get(2), unescape(get(3))})
		case "T":
			cr.todos = append(cr.todos, item{num, get(2), unescape(get(3))})
		case "I":
			cr.infos = append(cr.infos, item{num, get(2), unescape(get(3))})
		case "S":
			cr.summary = unescape(get(3))
		case "V":
			cr.hasVerify = true
			cr.runRC, _ = strconv.Atoi(get(2))
			cr.verifyRC, _ = strconv.Atoi(get(3))
		case "E":
			switch get(2) {
			case "source_failed":
				cr.sourceFailed = true
			case "step_run_undefined":
				cr.runUndefined = true
			}
		}
	}
	return cr
}
