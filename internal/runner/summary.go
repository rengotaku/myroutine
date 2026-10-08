package runner

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rengotaku/myroutine/internal/textwidth"
)

// 成功したステップの一言（emit_summary）の表示ルール。失敗と skip の一言は
// runner が作るのでブレないが、成功の一言だけはステップを書く人に任されている。
// ステップを足したり直したりしたときのブレを、実行のたびにここで拾う（ADR 0007）。

// summaryMaxWidth は一言の上限（表示カラム。全角 20 文字）。
const summaryMaxWidth = 40

// summaryBannedWords は ✓ と同じことを言うだけの語。
var summaryBannedWords = []string{"完了", "成功", "OK", "ok", "Ok"}

// summaryEnglishWord は日本語で書くべき箇所の英単語（3 文字以上）。AWS / SSO の
// ような大文字だけの略語は固有名詞として許す。
var summaryEnglishWord = regexp.MustCompile(`[A-Za-z]{3,}`)

// summaryProblem は一言がルールに反していれば、その内容を返す。問題なければ空。
func summaryProblem(summary string) string {
	if summary == "" {
		return "一言（emit_summary）を出していない"
	}
	for _, w := range summaryBannedWords {
		if strings.Contains(summary, w) {
			return fmt.Sprintf("一言に「%s」が入っている（✓ と同じことを言うだけなので書かない）", w)
		}
	}
	for _, w := range summaryEnglishWord.FindAllString(summary, -1) {
		if strings.ToUpper(w) != w {
			return fmt.Sprintf("一言に英単語「%s」が入っている（日本語で書く）", w)
		}
	}
	if w := textwidth.Width(summary); w > summaryMaxWidth {
		return fmt.Sprintf("一言が長い（%d カラム。上限 %d）", w, summaryMaxWidth)
	}
	return ""
}

// checkSummary は成功したステップの一言を検査し、ルールに反していれば要対応に積む。
// dry-run はステップが実際には動かないので見ない。
func (r *Runner) checkSummary(s *Step) {
	if r.opt.DryRun || !r.cfg.SummaryCheck {
		return
	}
	p := summaryProblem(r.summary[s.N])
	if p == "" {
		return
	}
	r.actions = append(r.actions, item{s.N, s.Slug + ".__summary",
		fmt.Sprintf("%s の%s — %s.sh の emit_summary を直す", s.Name, p, s.Slug)})
}
