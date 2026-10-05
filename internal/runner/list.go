package runner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rengotaku/myroutine/internal/eventlog"
	"github.com/rengotaku/myroutine/internal/format"
	"github.com/rengotaku/myroutine/internal/render"
)

// showList は myroutine ls。番号は order 上の位置（要対応の [n] と同じ番号体系）。
func (r *Runner) showList() {
	color := !r.cfg.NoColor && isTTY(os.Stdout)
	p := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + cReset
	}
	w := r.stdout
	fmt.Fprintln(w, p(cBold, "myroutine ステップ一覧（実行順 / 指定実行は slug: myroutine <slug>）:"))
	fmt.Fprintln(w, p(cDim, "  "+r.cfg.StepsDir))
	for _, s := range r.steps {
		mark := ""
		if s.Critical {
			mark += " " + p(cYellow, "(critical)")
		}
		if s.Destructive {
			mark += " " + p(cRed, "(破壊的)")
		}
		if s.Abort {
			mark += " " + p(cDim, "(失敗時に続行確認)")
		}
		if s.Parallel {
			mark += " " + p(cGreen, "(parallel)")
		}
		if s.Interactive {
			mark += " " + p(cCyan, "(interactive)")
		}
		if s.Cadence != "daily" || s.Weekday != 0 {
			cad := s.Cadence
			if s.Weekday != 0 {
				cad += "/" + weekdayByNum[s.Weekday]
			}
			mark += " " + p(cCyan, "("+cad+")")
		}
		// 指定実行（myroutine <slug>）に使うのは slug なので、一番目立たせる。
		fmt.Fprintf(w, "  %s %s  %s%s\n", p(cCyan, fmt.Sprintf("%d.", s.N)), p(cBold, s.Slug), s.Name, mark)
		fmt.Fprintln(w, "     "+p(cDim, filepath.Base(s.File)))
		if s.Disabled != "" {
			fmt.Fprintln(w, "     "+p(cDim, "⊘ 無効: "+s.Disabled))
		}
	}
}

// showLast は直近の実行のイベント（last-events.jsonl）を再生する。既定はレンダラの形で、
// --jsonl / --json のときは保存されている JSONL をそのまま流す。
func (r *Runner) showLast() int {
	path := filepath.Join(r.cfg.LogDir, "last-events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		r.diag.line("直近の実行記録がありません")
		return 1
	}
	defer f.Close()
	if r.opt.JSONL || r.opt.JSON {
		if _, err := io.Copy(r.stdout, f); err != nil && r.stdout.failed() {
			return 141
		}
		return 0
	}
	sink := newRenderSink(r.stdout, os.Stdout, r.cfg.NoColor)
	st := format.NewState(false)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sawSummary := false
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		ev, err := eventlog.Parse(line)
		if err != nil {
			continue
		}
		switch ev.Ev {
		case "run":
			st.Run(*ev.Run)
		case "start":
			render.Render(sink.w, []render.Block{st.Start(*ev.Start)}, sink.opt)
		case "step":
			if b := st.Step(*ev.Step); len(b.Lines) > 0 {
				render.Render(sink.w, []render.Block{b}, sink.opt)
			}
		case "summary":
			render.Render(sink.w, st.Summary(*ev.Summary), sink.opt)
			sawSummary = true
		}
	}
	if !sawSummary {
		if b := st.FlushSkips(); len(b.Lines) > 0 {
			render.Render(sink.w, []render.Block{b}, sink.opt)
		}
	}
	if r.stdout.failed() {
		return 141
	}
	return 0
}

// parseSteps は「brew,omz」「brew omz」「brew, omz」のいずれも受け、order 上の番号に
// 直して返す。数字・存在しない slug は黙って読み飛ばさずエラーにする。
func (r *Runner) parseSteps(args []string) ([]int, bool) {
	var words []string
	for _, a := range args {
		a = strings.Map(func(c rune) rune {
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
				return ','
			}
			return c
		}, a)
		words = append(words, strings.Split(a, ",")...)
	}
	bySlug := map[string]int{}
	for _, s := range r.steps {
		bySlug[s.Slug] = s.N
	}
	var out []int
	for _, w := range words {
		if w == "" {
			continue
		}
		if isDigits(w) {
			r.diag.line("ERROR: ステップは番号ではなく slug で指定してください: " + w)
			r.diag.line("       slug は myroutine ls で確認できます（例: myroutine brew,omz）")
			return nil, false
		}
		n, ok := bySlug[w]
		if !ok {
			r.diag.line("ERROR: 存在しないステップです: " + w)
			r.diag.line("       slug は myroutine ls で確認できます")
			return nil, false
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		r.diag.line("ERROR: ステップが指定されていません")
		return nil, false
	}
	return out, true
}
