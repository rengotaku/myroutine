package runner

import (
	"fmt"
	"strings"
	"time"

	"github.com/rengotaku/myroutine/internal/ledger"
)

// ledgerStamp は履歴に書く時刻。MYROUTINE_NOW_EPOCH の擬似時計に従う。
func (r *Runner) ledgerStamp() string { return r.now().Format(time.RFC3339) }

// loadLedger は actions.jsonl を畳んで読む。壊れた行は飛ばしたことを 1 行出す。
func (r *Runner) loadLedger() ([]ledger.Entry, error) {
	entries, skipped, err := ledger.Load(ledger.Path(r.cfg.LogDir))
	if skipped > 0 {
		r.diag.warn(fmt.Sprintf("%s の壊れた行を %d 行読み飛ばしました", ledger.FileName, skipped))
	}
	return entries, err
}

// stepNameOf は要対応を出したステップの表示名。見つからなければ slug を返す。
func (r *Runner) stepNameOf(n int, slug string) string {
	for _, s := range r.steps {
		if s.N == n {
			return s.Name
		}
	}
	return slug
}

// appendLedger は今回の要対応を seen として、出なくなったものを cleared として追記する。
// cleared は今回「評価できた」ステップ（r.evaluated）の未対応だけが対象で、
// 間隔で skip・失敗・未実行のステップの未対応は閉じない。
func (r *Runner) appendLedger() {
	r.ledgerWritten = true
	at := r.ledgerStamp()
	var evs []ledger.Event
	pos := map[string]int{} // id → evs 内の位置
	for _, a := range r.actions {
		slug := slugOfID(a.id)
		ev := ledger.Event{Ev: ledger.EvSeen, At: at, ID: a.id, Slug: slug, StepName: r.stepNameOf(a.step, slug), Text: a.text}
		if i, dup := pos[a.id]; dup {
			// 同じ id が 1 回の実行で複数出たら 1 件に畳む（最後の本文を残す）
			evs[i] = ev
			continue
		}
		pos[a.id] = len(evs)
		evs = append(evs, ev)
	}

	entries, err := r.loadLedger()
	if err != nil {
		r.diag.err("要対応の履歴を読めないため、解消の記録を省きます: " + err.Error())
	} else {
		for _, e := range ledger.Open(entries) {
			if _, now := pos[e.ID]; !now && contains(r.evaluated, e.Slug) {
				evs = append(evs, ledger.Event{Ev: ledger.EvCleared, At: at, ID: e.ID})
			}
		}
	}
	if err := ledger.Append(ledger.Path(r.cfg.LogDir), evs); err != nil {
		r.diag.err("要対応の履歴を書き込めません: " + err.Error())
	}
}

// runDone は `myroutine done <id>...`。全部が未対応のときだけ done を書く。
func (r *Runner) runDone(ids []string) int {
	if len(ids) == 0 {
		r.diag.line("Usage: myroutine done <id> [<id>...]")
		return 1
	}
	entries, err := r.loadLedger()
	if err != nil {
		r.diag.line("ERROR: 要対応の履歴を読めません: " + err.Error())
		return 1
	}
	open := map[string]bool{}
	for _, e := range ledger.Open(entries) {
		open[e.ID] = true
	}
	var bad []string
	for _, id := range ids {
		if !open[id] {
			bad = append(bad, id)
		}
	}
	if len(bad) > 0 {
		r.diag.line("ERROR: 未対応の要対応ではありません: " + strings.Join(bad, ", "))
		r.diag.line("       何も記録していません")
		return 1
	}
	if r.opt.DryRun {
		for _, id := range ids {
			r.diag.info("[dry-run] done を書く予定: " + id)
		}
		return 0
	}
	at := r.ledgerStamp()
	var evs []ledger.Event
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		evs = append(evs, ledger.Event{Ev: ledger.EvDone, At: at, ID: id})
	}
	if err := ledger.Append(ledger.Path(r.cfg.LogDir), evs); err != nil {
		r.diag.line("ERROR: 履歴を書き込めません: " + err.Error())
		return 1
	}
	for _, e := range evs {
		r.diag.info("done: " + e.ID)
	}
	return 0
}
