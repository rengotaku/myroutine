package runner

import (
	"strings"

	"github.com/rengotaku/myroutine/internal/night"
)

// runNightDone は `myroutine night-done <job id>...`。夜間実行の結果に居る id だけを
// $MYROUTINE_LOG_DIR/night-ack.jsonl に追記する。結果ファイル（report/*.json）は書き換えない。
// 結果に無い id が 1 つでもあれば、何も書かずに終了コード 1（RF-5）。
func (r *Runner) runNightDone(ids []string) int {
	if len(ids) == 0 {
		r.diag.line("Usage: myroutine night-done <job id> [<job id>...]")
		return 1
	}
	rep, err := night.LoadReport(night.Dir())
	if err != nil {
		r.diag.line("ERROR: 夜間実行の結果を読めません: " + err.Error())
		return 1
	}
	known := make(map[string]bool, len(rep.Jobs))
	for _, j := range rep.Jobs {
		known[j.ID] = true
	}
	var bad []string
	for _, id := range ids {
		if !known[id] {
			bad = append(bad, id)
		}
	}
	if len(bad) > 0 {
		r.diag.line("ERROR: 結果に無い id: " + strings.Join(bad, ", "))
		r.diag.line("       何も記録していません")
		return 1
	}
	if r.opt.DryRun {
		for _, id := range ids {
			r.diag.info("[dry-run] night-done を書く予定: " + id)
		}
		return 0
	}
	// 検査が済んでから 1 回で追記する（途中まで書いて残りを書かない状態を作らない）
	if err := night.AppendAck(night.AckPath(r.cfg.LogDir), ids, r.now()); err != nil {
		r.diag.line("ERROR: 対応済みを記録できません: " + err.Error())
		return 1
	}
	for _, id := range ids {
		r.diag.info("night-done: " + id)
	}
	return 0
}
