package runner

import (
	"fmt"

	"github.com/rengotaku/myroutine/internal/render"
)

// 要対応は「人が次に打つもの」を出す場所なので、本文にコマンド行（「→ $ 」か「$ 」で
// 始まる行）を必ず持たせる。コマンド行だけが色付け・折り返し除外の対象になり、
// これが無い要対応は読んでも何を打てばよいか分からない（#58）。
// ステップは add_action の --cmd でコマンドを渡す。自由文の書き方に頼らず、
// 守られていないものを実行のたびにここで拾う（一言の検査 checkSummary と同じ扱い）。

// checkActionCommands は、ステップが出した要対応のうちコマンド行の無いものを要対応に積む。
// dry-run はステップが実際には動かないので見ない。
func (r *Runner) checkActionCommands(s *Step, actions []item) {
	if r.opt.DryRun || !r.cfg.ActionCheck {
		return
	}
	for _, a := range actions {
		if render.HasCommandLine(a.text) {
			continue
		}
		r.actions = append(r.actions, item{s.N, a.id + ".__cmd",
			fmt.Sprintf("要対応 %s にコマンド行が無い — %s.sh の add_action に --cmd を付ける", a.id, s.Slug)})
	}
}
