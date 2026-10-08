package runner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────
// 実行間隔 (cadence)
// ─────────────────────────────────────────────────────────────────
//
// 週次・月次のステップは「毎日走らせて中で日付を見る」のではなく、本体が前回
// 成功日を見て skip する（ADR 0003）。前回成功日は $MYROUTINE_LOG_DIR/last-run/<slug>
// に YYYY-MM-DD で持つ。slug を指定した実行は明示指定なので間隔判定をしない。

var weekdayJa = []string{"", "月", "火", "水", "木", "金", "土", "日"}

// now は「今」。MYROUTINE_NOW_EPOCH（テスト用の擬似時計）があればそれを使う。
func (r *Runner) now() time.Time {
	if r.cfg.NowEpoch != nil {
		return time.Unix(*r.cfg.NowEpoch, 0)
	}
	return time.Now()
}

func (r *Runner) stampFile(slug string) string {
	return filepath.Join(r.cfg.LogDir, "last-run", slug)
}

// writeStamp は前回成功日を記録する。書けなくても実行は止めない（zsh 版と同じ）。
func (r *Runner) writeStamp(slug string) {
	dir := filepath.Join(r.cfg.LogDir, "last-run")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(r.stampFile(slug), []byte(r.now().Format("2006-01-02")+"\n"), 0o644)
}

// isoWeekday は 1=月 .. 7=日。
func isoWeekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

func localMidnight(day string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	return t, err == nil
}

// cadenceDue は実行間隔を満たしているかを返す。満たさないときは理由を返す。
func (r *Runner) cadenceDue(s *Step) (bool, string) {
	if r.opt.ForceCadence {
		return true, ""
	}
	if s.Cadence == "daily" && s.Weekday == 0 {
		return true, ""
	}
	now := r.now()

	// 曜日指定は間隔より先に見る。曜日が違う日は、間隔を満たしていても実行しない。
	if s.Weekday != 0 && isoWeekday(now) != s.Weekday {
		return false, fmt.Sprintf("%s（%s曜のみ）", s.Cadence, weekdayJa[s.Weekday])
	}

	last := ""
	if data, err := os.ReadFile(r.stampFile(s.Slug)); err == nil {
		last = strings.Join(strings.Fields(string(data)), "")
	}
	// 前回成功日が無い / 読めない形なら、初回として実行する
	lastT, ok := localMidnight(last)
	if last == "" || !ok {
		return true, ""
	}
	todayT, _ := localMidnight(now.Format("2006-01-02"))
	elapsed := (todayT.Unix() - lastT.Unix()) / 86400

	switch s.Cadence {
	case "daily":
		if elapsed >= 1 {
			return true, ""
		}
	case "weekly":
		if elapsed >= 7 {
			return true, ""
		}
	case "biweekly":
		if elapsed >= 14 {
			return true, ""
		}
	case "monthly":
		if lastT.Format("2006-01") != now.Format("2006-01") {
			return true, ""
		}
	}
	return false, fmt.Sprintf("%s（前回 %s）", s.Cadence, last)
}

// cadenceView は実行間隔で skip したステップを、人が読む形にしたもの。
type cadenceView struct {
	label string // "weekly" / "weekly・月曜のみ"
	last  string // 前回成功日 MM-DD（無ければ空）
	next  string // 次に実行される日 MM-DD（求められなければ空）
}

// cadenceSkipView は cadenceDue が skip と判定したステップについて、間隔の表記・
// 前回成功日・次回の日付を返す。判定そのものは cadenceDue だけが持つ。
func (r *Runner) cadenceSkipView(s *Step) cadenceView {
	v := cadenceView{label: s.Cadence}
	if s.Weekday != 0 {
		v.label += "・" + weekdayJa[s.Weekday] + "曜のみ"
	}
	now := r.now()
	today, _ := localMidnight(now.Format("2006-01-02"))
	earliest := today.AddDate(0, 0, 1)

	last := ""
	if data, err := os.ReadFile(r.stampFile(s.Slug)); err == nil {
		last = strings.Join(strings.Fields(string(data)), "")
	}
	if lastT, ok := localMidnight(last); last != "" && ok {
		v.last = lastT.Format("01-02")
		var due time.Time
		switch s.Cadence {
		case "daily":
			due = lastT.AddDate(0, 0, 1)
		case "weekly":
			due = lastT.AddDate(0, 0, 7)
		case "biweekly":
			due = lastT.AddDate(0, 0, 14)
		case "monthly":
			due = time.Date(lastT.Year(), lastT.Month()+1, 1, 0, 0, 0, 0, time.Local)
		default:
			return v
		}
		if due.After(earliest) {
			earliest = due
		}
	}
	if s.Weekday != 0 {
		for isoWeekday(earliest) != s.Weekday {
			earliest = earliest.AddDate(0, 0, 1)
		}
	}
	v.next = earliest.Format("01-02")
	return v
}

// ─────────────────────────────────────────────────────────────────
// 履歴と劣化検知
// ─────────────────────────────────────────────────────────────────
//
// 履歴は「実行日 <TAB> 時刻 <TAB> action id のカンマ区切り <TAB> 評価できた slug の
// カンマ区切り <TAB> slug=所要ミリ秒 のカンマ区切り」の TSV で追記する。
// 4 列目が空 or "*" の行は旧形式として「全ステップを評価した」とみなす。
// 部分実行・中断・dry-run の実行は記録しない。

func (r *Runner) historyFile() string { return filepath.Join(r.cfg.LogDir, "history.tsv") }

type historyDay struct {
	date  string
	ids   []string
	evals string // "*" は全ステップを評価した扱い
}

// splitComma は zsh の ${(s:,:)x} と同じく空の要素を落として割る。
func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// historyDays は履歴を実行日単位に畳んで新しい順に返す。同じ日の行は最後の行が勝つ。
func (r *Runner) historyDays() []historyDay {
	f, err := os.Open(r.historyFile())
	if err != nil {
		return nil
	}
	defer f.Close()
	byDay := map[string]historyDay{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		// IFS=$'\t' read は連続タブを畳んで空フィールドを失う。行を丸ごと割る。
		col := strings.Split(line, "\t")
		get := func(i int) string {
			if i < len(col) {
				return col[i]
			}
			return ""
		}
		d := get(0)
		// 値の空判定だと「その日の 1 回目が要対応ゼロ」のときに同じ日を
		// 二重に数え、連続日数が水増しされる。キーの有無で初出を判定する。
		if _, ok := byDay[d]; !ok {
			order = append(order, d)
		}
		ev := get(3)
		if ev == "" {
			ev = "*"
		}
		byDay[d] = historyDay{date: d, ids: splitComma(get(2)), evals: ev}
	}
	out := make([]historyDay, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		out = append(out, byDay[order[i]])
	}
	return out
}

func (d historyDay) evaluated(slug string) bool {
	if d.evals == "" || d.evals == "*" {
		return true
	}
	return contains(splitComma(d.evals), slug)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func slugOfID(id string) string {
	if i := strings.IndexByte(id, '.'); i >= 0 {
		return id[:i]
	}
	return id
}

// analyzeHistory は連続日数・NEW・解消を出す。部分実行・中断は要対応の集合が欠けるため
// 比較の土台にできず、何もしない。
func (r *Runner) analyzeHistory() {
	if !r.fullRun {
		return
	}
	today := time.Now().Format("2006-01-02")
	cur := r.actionIDs()
	days := r.historyDays()

	// 連続「日数」は当日を数えないので、streak の走査からは当日を外す。
	var past []historyDay
	for _, d := range days {
		if d.date != today {
			past = append(past, d)
		}
	}
	for _, id := range cur {
		streak := 1
		slug := slugOfID(id)
		for _, d := range past {
			// そのステップを評価していない日は、連続を切りも伸ばしもしない
			if !d.evaluated(slug) {
				continue
			}
			if !contains(d.ids, id) {
				break
			}
			streak++
		}
		r.streak[id] = streak
	}

	// NEW / 解消 は「前回の実行」との比較なので、当日の先行実行があればそれを使う。
	if len(days) == 0 {
		return
	}
	prev := days[0]
	for _, id := range cur {
		if !prev.evaluated(slugOfID(id)) {
			continue
		}
		if !contains(prev.ids, id) {
			r.newIDs[id] = true
		}
	}
	// 今回評価していないステップの要対応は「見ていない」だけなので触らない。
	for _, id := range prev.ids {
		if !contains(r.evaluated, slugOfID(id)) {
			continue
		}
		if !contains(cur, id) {
			r.resolved = append(r.resolved, id)
		}
	}
}

// durationField はステップ別の所要時間を "slug=ms,slug=ms" にする。
// 実行に入らなかったステップ（skip・前提不足・間隔外・無効）は載せない。
func (r *Runner) durationField() string {
	var out []string
	for _, s := range r.steps {
		res, ok := r.results[s.N]
		if !ok || res.status == "skip" || res.status == "disabled" {
			continue
		}
		out = append(out, s.Slug+"="+strconv.FormatInt(res.ms, 10))
	}
	return strings.Join(out, ",")
}

func (r *Runner) appendHistory() {
	now := time.Now()
	var ev []string
	for _, s := range r.evaluated {
		if !contains(ev, s) {
			ev = append(ev, s)
		}
	}
	line := strings.Join([]string{
		now.Format("2006-01-02"), now.Format("15:04:05"),
		strings.Join(r.actionIDs(), ","), strings.Join(ev, ","), r.durationField(),
	}, "\t") + "\n"
	f, err := os.OpenFile(r.historyFile(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		r.diag.err("履歴を書き込めません: " + err.Error())
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		r.diag.err("履歴を書き込めません: " + err.Error())
	}
}
