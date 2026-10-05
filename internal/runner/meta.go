package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Step はステップ 1 本の宣言を検証した結果。
type Step struct {
	N    int // order 上の位置（1 始まり）。要対応の [n] と --json の n に使う
	Slug string
	File string
	Name string

	Critical    bool
	Abort       bool
	Destructive bool
	Parallel    bool
	Interactive bool

	Cadence string
	Weekday int // 0 = 指定なし、1..7 = 月..日
	// Disabled は無効化の理由。空なら有効。
	Disabled string

	Requires     []string
	RequiresPath []string
	// Timeout は step_timeout。nil なら既定（--timeout / MYROUTINE_TIMEOUT）。
	Timeout *int
}

type metaVar struct {
	kind   string // scalar / array / assoc
	scalar string
	array  []string
}

// knownVars は宣言として受け付ける step_* の一覧。これ以外はエラーにする
// （step_critcal=true と綴りを間違えたとき、黙って critical でなくなるのを防ぐ）。
var knownVars = []string{
	"step_name", "step_critical", "step_abort_on_fail", "step_destructive",
	"step_parallel", "step_interactive", "step_requires", "step_requires_path",
	"step_timeout", "step_cadence", "step_cadence_weekday", "step_disabled",
}

var boolVars = []string{"step_critical", "step_parallel", "step_interactive", "step_abort_on_fail", "step_destructive"}

var weekdayNames = map[string]int{"mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6, "sun": 7}
var weekdayByNum = []string{"", "mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// readMeta は prelude の meta モードでステップを 1 回 source し、宣言を取り出す。
func (r *Runner) readMeta(n int, slug, file string) (*Step, bool) {
	out, err := os.CreateTemp(r.tmpDir, "meta-*")
	if err != nil {
		r.diag.line("ERROR: 一時ファイルを作成できません: " + err.Error())
		return nil, false
	}
	defer out.Close()

	cmd := exec.CommandContext(r.ctx, r.zsh, r.prelude, "meta", file)
	cmd.Env = r.stepEnv(n, slug, false)
	// meta もステップのコードを動かすので、独自のプロセスグループで起動し、
	// 中断されたら子孫ごと止める。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		killTree(cmd.Process.Pid, cmd.Process.Pid)
		return nil
	}
	// 契約違反（トップレベルの出力）が結果を汚さないよう stdout は捨てる。
	// stderr は zsh 版と同じく見せる（構文エラーの位置が出る）。
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{out}
	cmd.WaitDelay = 2 * time.Second
	runErr := cmd.Run()

	recs, rerr := os.ReadFile(out.Name())
	if rerr != nil {
		r.diag.line("ERROR: メタデータの取得に失敗しました: " + file)
		return nil, false
	}
	vars, srcFailed, done := parseMetaRecords(string(recs))
	if srcFailed {
		r.diag.line("ERROR: ステップの読み込みに失敗しました: " + file)
		return nil, false
	}
	if runErr != nil || !done {
		r.diag.line(fmt.Sprintf("ERROR: メタデータの取得に失敗しました (rc=%d): %s", exitCodeOf(cmd.ProcessState, runErr), file))
		return nil, false
	}
	return validateMeta(n, slug, file, vars, r.diag)
}

func parseMetaRecords(s string) (map[string]metaVar, bool, bool) {
	vars := map[string]metaVar{}
	srcFailed, done := false, false
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		switch f[0] {
		case "D":
			done = true
		case "E":
			if len(f) >= 3 && f[2] == "source_failed" {
				srcFailed = true
			}
		case "M":
			if len(f) < 3 {
				continue
			}
			v := metaVar{kind: f[2]}
			switch f[2] {
			case "scalar":
				if len(f) >= 4 {
					v.scalar = unescape(f[3])
				}
			case "array":
				for _, e := range f[3:] {
					v.array = append(v.array, unescape(e))
				}
			}
			vars[f[1]] = v
		}
	}
	return vars, srcFailed, done
}

// scalarOf は zsh で "${var}" と書いたときの値を返す（配列は空白で連結される）。
func (v metaVar) scalarOf() string {
	if v.kind == "array" {
		return strings.Join(v.array, " ")
	}
	return v.scalar
}

// listOf は zsh で for x in $var と書いたときの要素を返す（空の要素は落ちる）。
func (v metaVar) listOf() []string {
	src := v.array
	if v.kind == "scalar" {
		src = []string{v.scalar}
	}
	var out []string
	for _, e := range src {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// validateMeta は宣言を検証する。zsh 版の 7 種のエラーに、未知の step_*・bool の値・
// step_timeout の値の検証を加えたもの。最初に見つけた誤りで止まる。
func validateMeta(n int, slug, file string, vars map[string]metaVar, d *diag) (*Step, bool) {
	fail := func(lines ...string) (*Step, bool) {
		for _, l := range lines {
			d.line(l)
		}
		return nil, false
	}
	base := filepath.Base(file)

	var unknown []string
	known := map[string]bool{}
	for _, k := range knownVars {
		known[k] = true
	}
	for name := range vars {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fail(
			fmt.Sprintf("ERROR: 未知の宣言があります: %s: %s", strings.Join(unknown, ", "), file),
			"       使える宣言: "+strings.Join(knownVars, " "),
		)
	}
	for name, v := range vars {
		if v.kind == "assoc" {
			return fail(fmt.Sprintf("ERROR: %s に連想配列は使えません: %s", name, file))
		}
	}

	get := func(name string) (string, bool) {
		v, ok := vars[name]
		if !ok {
			return "", false
		}
		return v.scalarOf(), true
	}

	s := &Step{N: n, Slug: slug, File: file}
	s.Name, _ = get("step_name")
	if s.Name == "" {
		return fail("ERROR: step_name が定義されていません: " + file)
	}

	bools := map[string]*bool{
		"step_critical": &s.Critical, "step_parallel": &s.Parallel, "step_interactive": &s.Interactive,
		"step_abort_on_fail": &s.Abort, "step_destructive": &s.Destructive,
	}
	for _, name := range boolVars {
		v, ok := get(name)
		if !ok {
			continue
		}
		switch v {
		case "true":
			*bools[name] = true
		case "false":
		default:
			return fail(fmt.Sprintf("ERROR: %s に使えるのは true / false だけです: %q: %s", name, v, file))
		}
	}

	cad, _ := get("step_cadence")
	if cad == "" {
		cad = "daily"
	}
	switch cad {
	case "daily", "weekly", "biweekly", "monthly":
		s.Cadence = cad
	default:
		return fail("ERROR: step_cadence に使えるのは daily / weekly / biweekly / monthly だけです: " + file)
	}

	if v, ok := get("step_timeout"); ok && v != "" {
		if !isDigits(v) {
			return fail(fmt.Sprintf("ERROR: step_timeout には 0 以上の整数（秒）を指定してください: %q: %s", v, file))
		}
		t, err := strconv.Atoi(v)
		if err != nil {
			return fail(fmt.Sprintf("ERROR: step_timeout には 0 以上の整数（秒）を指定してください: %q: %s", v, file))
		}
		s.Timeout = &t
	}

	// 併用を実行時に弾くと「走らせてみるまで分からない」。宣言の時点で落とす。
	if s.Parallel && s.Destructive {
		return fail("ERROR: step_parallel と step_destructive は併用できません: "+file,
			"       破壊的ステップの確認は stdin を読むため、並列では混線します")
	}
	if s.Parallel && s.Abort {
		return fail("ERROR: step_parallel と step_abort_on_fail は併用できません: "+file,
			"       後続が既に走っている状況では「続行するか」を尋ねる意味がありません")
	}

	// step_disabled は bool ではなく「無効化の理由」の文字列。true/false を渡す誤用は
	// 「有効/無効を切り替えるフラグ」との混同なので、読み込み時点で弾く。
	s.Disabled, _ = get("step_disabled")
	if l := strings.ToLower(s.Disabled); l == "true" || l == "false" {
		return fail("ERROR: step_disabled には無効化の理由を書いてください（true/false は不可）: " + file)
	}
	if s.Parallel && s.Interactive {
		return fail("ERROR: step_parallel と step_interactive は併用できません: "+file,
			"       並列実行中は複数ステップの出力が混ざり、読める形になりません")
	}

	if wd, _ := get("step_cadence_weekday"); wd != "" {
		num, ok := weekdayNames[strings.ToLower(wd)]
		if !ok && len(wd) == 1 && wd[0] >= '1' && wd[0] <= '7' {
			num, ok = int(wd[0]-'0'), true
		}
		if !ok {
			return fail(fmt.Sprintf("ERROR: step_cadence_weekday に使えるのは mon..sun / 1..7 だけです: %s (%s)", wd, base))
		}
		s.Weekday = num
	}

	if v, ok := vars["step_requires"]; ok {
		s.Requires = v.listOf()
	}
	if v, ok := vars["step_requires_path"]; ok {
		s.RequiresPath = v.listOf()
	}
	return s, true
}
