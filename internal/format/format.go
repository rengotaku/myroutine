// Package format はイベントを render.Block へ組み立てる。実際の書き出しは
// 行わない（それは internal/render の責務）。
package format

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rengotaku/myroutine/internal/eventlog"
	"github.com/rengotaku/myroutine/internal/render"
	"github.com/rengotaku/myroutine/internal/textwidth"
)

const (
	iconOK   = "✓"
	iconFail = "✗"
	iconSkip = "⊘"
	iconRun  = "▶"

	nameCol = 32 // ステップ名 + summary を揃える表示幅
	colSep  = "  " // 名前列と次の語（一言・所要時間・開始時刻）の区切り

	// iconRuneLen は iconOK / iconFail / iconSkip がいずれも 1 rune であることに
	// 依存した定数。着色対象（先頭何 rune か）を render 側に伝えるのに使う。
	iconRuneLen = 1
)

// styleForIcon は見出しに使う icon 文字列（iconOK / iconFail / iconSkip）を
// render.Style に対応付ける。整形（どの記号を使うか）は format 側の責務のまま、
// 実際の色コードは render パッケージに閉じ込める。
func styleForIcon(icon string) render.Style {
	switch icon {
	case iconOK:
		return render.StyleOK
	case iconFail:
		return render.StyleFail
	case iconSkip:
		return render.StyleSkip
	default:
		return render.StyleNone
	}
}

// State は 1 回の実行分の処理状態を持つ。
type State struct {
	Verbose bool

	total       int
	log         string
	pendingSkip []eventlog.StepEvent
	counts      stepCounts
	failed      []string // 失敗・timeout したステップ名（実行順）
}

// stepCounts は末尾の件数行に出す、状態ごとのステップ数。
type stepCounts struct {
	ok, fail, skip, cadence int
}

// NewState は State を初期化する。
func NewState(verbose bool) *State {
	return &State{Verbose: verbose}
}

// Run は run イベントを処理する。以後の step イベントで total を使う。
func (s *State) Run(r eventlog.RunEvent) {
	s.total = r.Total
	s.log = r.Log
}

// Start は start イベントを処理し、開始行の Block を返す。開始行は追記のみで
// 後から書き換えない（ログの再描画でも崩れないようにするため）。
func (s *State) Start(st eventlog.StartEvent) render.Block {
	pos := st.Pos
	if pos == 0 {
		pos = st.N
	}
	// 結果行と同じ名前列を使い、「開始」を結果行の一言と同じカラムから始める
	head, col := nameColumn(iconRun, pos, s.total, st.Name)
	parts := []string{col}
	if t := hhmm(st.At); t != "" {
		parts = append(parts, "開始 "+t)
	}
	label := strings.TrimRight(strings.Join(parts, colSep), " ")
	// 名前列を広げたぶん狭い端末で開始時刻が切り捨てられないよう、結果行と同じく折り返す
	return render.Block{Lines: []render.Line{{Kind: render.KindText, Indent: 2, Text: label, Wrap: true, HangIndent: textwidth.Width(head)}}}
}

// nameColumn は開始行と結果行に共通の「記号 [位置/全件] 名前」を、名前を
// nameCol に揃えた形で返す。head は名前より前の部分（折り返しのぶら下げ幅に使う）。
// Pad は列幅より長い名前をそのまま返すので、詰める分が 0 になり名前と
// 次の語がくっつく。名前側を切り詰めて、区切りの空白を必ず残す。
func nameColumn(icon string, pos, total int, name string) (head, col string) {
	if textwidth.Width(name) > nameCol-1 {
		name = textwidth.Truncate(name, nameCol-1)
	}
	head = fmt.Sprintf("%s [%d/%d] ", icon, pos, total)
	return head, head + textwidth.Pad(name, nameCol)
}

// hhmm は "HH:MM:SS" の先頭 "HH:MM" を返す。形式が違えば空を返す。
func hhmm(at string) string {
	if len(at) < 5 || at[2] != ':' {
		return ""
	}
	return at[:5]
}

// Step は step イベントを処理し、即座に描画すべき Block を返す。
// status が skip の場合は描画せず内部に溜め、空の Block を返す
// （呼び出し側は Lines が空なら Render しなくてよい）。
// isFailStatus は tail 展開・rc 表示・全文ログ導線を出すべき失敗系ステータスか
// を判定する。timeout は fail と別ステータスだが、読み手にとっては同じく
// 「失敗として調べる対象」なので同列に扱う。
func isFailStatus(status string) bool {
	return status == "fail" || status == "timeout"
}

func (s *State) Step(st eventlog.StepEvent) render.Block {
	s.count(st)
	switch st.Status {
	case "skip":
		// 実行間隔による skip は途中に出さず、末尾の「今日は対象外」にまとめる。
		// それ以外（前提不足・見送り）はその場で結果行として出す。
		if isCadenceSkip(st) {
			s.pendingSkip = append(s.pendingSkip, st)
			return render.Block{}
		}
	case "disabled":
		// disabled は summary イベントの Disabled 一覧（「⊘ 無効」ブロック）
		// だけで出す。ここで即時描画すると同じステップが二重に出る。
		return render.Block{}
	}
	return render.Block{Lines: s.stepLines(st)}
}

// count は末尾の件数行と失敗の再掲のために、ステップの状態を数える。
// disabled は「⊘ 無効」ブロックに件数が出るので数えない。
func (s *State) count(st eventlog.StepEvent) {
	switch {
	case isFailStatus(st.Status):
		s.counts.fail++
		s.failed = append(s.failed, st.Name)
	case st.Status == "skip" && isCadenceSkip(st):
		s.counts.cadence++
	case st.Status == "skip":
		s.counts.skip++
	case st.Status == "ok":
		s.counts.ok++
	}
}

func isCadenceSkip(st eventlog.StepEvent) bool {
	return strings.HasPrefix(st.Note, "cadence: ")
}

// stepLines は結果行を組み立てる。書式は状態によらず
// 「記号 [位置/全件] 名前  一言  所要時間」の 1 つに揃える。一言は成功なら
// ステップの emit_summary、失敗と skip なら runner の記録（status / rc / note）から作る。
func (s *State) stepLines(st eventlog.StepEvent) []render.Line {
	icon, iconStyle := iconOK, render.StyleOK
	switch {
	case isFailStatus(st.Status):
		icon, iconStyle = iconFail, render.StyleFail
	case st.Status == "skip":
		icon, iconStyle = iconSkip, render.StyleSkip
	}

	// pos は run イベントの total と同じ集合で数えた通し番号。
	// pos を持たない古い入力（0 のまま）では n にフォールバックする。
	pos := st.Pos
	if pos == 0 {
		pos = st.N
	}

	head, col := nameColumn(icon, pos, s.total, st.Name)
	parts := []string{col}
	if w := resultWord(st); w != "" {
		parts = append(parts, w)
	}
	// skip は実行していないので所要時間を出さない
	if st.Status != "skip" {
		parts = append(parts, formatDuration(st.DurationMs))
	}
	label := strings.TrimRight(strings.Join(parts, colSep), " ")
	// 端末幅を超える行は切り捨てず折り返す（一言・skip の理由・所要時間を落とさない）
	lines := []render.Line{{Kind: render.KindText, Indent: 2, Text: label, IconLen: iconRuneLen, IconStyle: iconStyle, Wrap: true, HangIndent: textwidth.Width(head)}}

	showTail := isFailStatus(st.Status) || (s.Verbose && len(st.Tail) > 0)
	if showTail && len(st.Tail) > 0 {
		tail := st.Tail
		if len(tail) > 4 {
			tail = tail[len(tail)-4:]
		}
		for _, t := range tail {
			lines = append(lines, render.Line{Kind: render.KindText, Indent: 6, Text: "│ " + t, Dim: true})
		}
	}
	if isFailStatus(st.Status) && s.log != "" {
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 6, Text: "└ 全文: " + shortenPath(s.log), Dim: true})
	}
	return lines
}

// resultWord は結果行の一言を返す。
func resultWord(st eventlog.StepEvent) string {
	switch st.Status {
	case "fail":
		if st.RC != 0 {
			return "失敗（終了コード " + strconv.Itoa(st.RC) + "）"
		}
		return "失敗"
	case "timeout":
		// note は "${limit}s" の形で入っている
		if sec, ok := strings.CutSuffix(st.Note, "s"); ok && sec != "" {
			return "timeout（" + sec + " 秒）"
		}
		return "timeout"
	case "skip":
		return "skip（" + skipReason(st.Note) + "）"
	}
	// 一言を出さない成功ステップも、名前と所要時間だけの行にしない
	if st.Summary == "" {
		return "要約なし"
	}
	return st.Summary
}

// skipReason は skip の note（runner の記録）を画面向けの理由に直す。
//
//	"コマンド: tmux, パス: /x"  → "tmux・/x が無い"
//	"declined (destructive)"    → "確認で N"
//	"dry-run (destructive)"     → "dry-run"
func skipReason(note string) string {
	switch note {
	case "declined (destructive)":
		return "確認で N"
	case "dry-run (destructive)":
		return "dry-run"
	}
	var missing []string
	for _, f := range strings.Split(note, ", ") {
		v, ok := strings.CutPrefix(f, "コマンド: ")
		if !ok {
			v, ok = strings.CutPrefix(f, "パス: ")
			v = shortenPath(v)
		}
		if !ok {
			return note
		}
		missing = append(missing, v)
	}
	return strings.Join(missing, "・") + " が無い"
}

// FlushSkips は溜めていた実行間隔の skip を「今日は対象外」の Block として返す。
// summary イベントを受け取ったとき、または stdin がそれ以前に閉じたときに呼ぶ。
// それ以外の skip（前提不足・見送り）は Step がその場で結果行として出している。
func (s *State) FlushSkips() render.Block {
	if len(s.pendingSkip) == 0 {
		return render.Block{}
	}
	cadence := s.pendingSkip
	s.pendingSkip = nil

	lines := []render.Line{{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("%s 今日は対象外 %d 件（実行間隔による skip）", iconSkip, len(cadence)), IconLen: iconRuneLen, IconStyle: styleForIcon(iconSkip)}}
	lines = append(lines, skipRows(cadence, cadenceColumns)...)
	return render.Block{Lines: lines}
}

// cadenceColumns は実行間隔 skip の行を「間隔 / 次回（前回）」の列に割る。
// cadence を持たない古い入力では note をそのまま 1 列で出す。
func cadenceColumns(st eventlog.StepEvent) []string {
	if st.Cadence == "" {
		return []string{strings.TrimPrefix(st.Note, "cadence: ")}
	}
	next := ""
	if st.NextDue != "" {
		next = "次回 " + st.NextDue
	}
	if st.LastRun != "" {
		next += "（前回 " + st.LastRun + "）"
	}
	return []string{st.Cadence, next}
}

// skipRows は skip 一覧の行を組み立てる。番号・名前・各列の幅は一覧の中の
// 最長に合わせる（固定幅だと長い名前が次の列とくっつく）。
func skipRows(items []eventlog.StepEvent, cols func(eventlog.StepEvent) []string) []render.Line {
	nums := make([]string, len(items))
	cells := make([][]string, len(items))
	numW, nameW := 0, 0
	var colW []int
	for i, st := range items {
		nums[i] = fmt.Sprintf("[%d]", st.N)
		numW = max(numW, textwidth.Width(nums[i]))
		nameW = max(nameW, textwidth.Width(st.Name))
		cells[i] = cols(st)
		for j, c := range cells[i] {
			if j >= len(colW) {
				colW = append(colW, 0)
			}
			colW[j] = max(colW[j], textwidth.Width(c))
		}
	}
	var lines []render.Line
	for i, st := range items {
		parts := []string{textwidth.Pad(nums[i], numW), textwidth.Pad(st.Name, nameW)}
		for j, c := range cells[i] {
			if j < len(cells[i])-1 {
				c = textwidth.Pad(c, colW[j])
			}
			parts = append(parts, c)
		}
		label := strings.TrimRight(strings.Join(parts, "  "), " ")
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 6, Text: render.FirstLine(label), Dim: true, Wrap: true})
		lines = append(lines, render.WrapText(st.Note, 10)...)
	}
	return lines
}

// Summary は summary イベントを処理し、集約ブロック群を返す。
func (s *State) Summary(sum eventlog.SummaryEvent) []render.Block {
	var blocks []render.Block

	if skipBlock := s.FlushSkips(); len(skipBlock.Lines) > 0 {
		blocks = append(blocks, skipBlock)
	}

	if disabled := disabledBlock(sum.Disabled); len(disabled.Lines) > 0 {
		blocks = append(blocks, disabled)
	}

	blocks = append(blocks, render.Block{Lines: []render.Line{
		{Kind: render.KindBlank},
		{Kind: render.KindRule},
	}})

	blocks = append(blocks, actionBlock(sum.Actions))

	if todos := listBlock(iconSkip, "きょう回すもの", len(sum.Todos), toItems(sum.Todos)); len(todos.Lines) > 0 {
		blocks = append(blocks, render.Block{Lines: []render.Line{{Kind: render.KindBlank}}})
		blocks = append(blocks, todos)
	}

	if infos := listBlock(iconOK, "情報", len(sum.Infos), toInfoItems(sum.Infos)); len(infos.Lines) > 0 {
		blocks = append(blocks, render.Block{Lines: []render.Line{{Kind: render.KindBlank}}})
		blocks = append(blocks, infos)
	}
	if len(sum.Infos) > 0 {
		// 「対応不要」の注記は見出しに付ける。listBlock 側では表現しないので上書きする。
		lastBlock := &blocks[len(blocks)-1]
		lastBlock.Lines[0].Text = fmt.Sprintf("%s 情報 %d 件（対応不要）", iconOK, len(sum.Infos))
	}

	if len(sum.Resolved) > 0 {
		blocks = append(blocks, render.Block{Lines: []render.Line{
			{Kind: render.KindBlank},
			{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("%s 解消 %d 件: %s", iconOK, len(sum.Resolved), strings.Join(sum.Resolved, ", ")), IconLen: iconRuneLen, IconStyle: styleForIcon(iconOK)},
		}})
	}

	blocks = append(blocks, s.tallyBlock())

	blocks = append(blocks, render.Block{Lines: []render.Line{
		{Kind: render.KindRule},
		{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("所要 %s   全量ログ: %s", formatDuration(sum.WallMs), shortenPath(s.log)), Dim: true},
		{Kind: render.KindText, Indent: 2, Text: "再表示: myroutine last", Dim: true},
	}})

	return blocks
}

func disabledBlock(items []eventlog.DisabledItem) render.Block {
	if len(items) == 0 {
		return render.Block{}
	}
	lines := []render.Line{
		{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("%s 無効 %d 件", iconSkip, len(items)), IconLen: iconRuneLen, IconStyle: styleForIcon(iconSkip)},
	}
	for _, it := range items {
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 6, Text: fmt.Sprintf("[%d] %s", it.N, it.Name), Wrap: true})
		if it.Reason != "" {
			lines = append(lines, render.Line{Kind: render.KindText, Indent: 10, Text: it.Reason, Dim: true, Wrap: true})
		}
	}
	return render.Block{Lines: lines}
}

type item struct {
	Step       int
	ID         string
	Text       string
	StreakDays int
	New        bool
}

func toItems(ts []eventlog.TodoItem) []item {
	out := make([]item, len(ts))
	for i, t := range ts {
		out[i] = item{Step: t.Step, ID: t.ID, Text: t.Text}
	}
	return out
}

func toInfoItems(ts []eventlog.InfoItem) []item {
	out := make([]item, len(ts))
	for i, t := range ts {
		out[i] = item{Step: t.Step, ID: t.ID, Text: t.Text}
	}
	return out
}

func actionBlock(actions []eventlog.ActionItem) render.Block {
	if len(actions) == 0 {
		return render.Block{Lines: []render.Line{
			{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("%s 要対応なし", iconOK), IconLen: iconRuneLen, IconStyle: styleForIcon(iconOK)},
		}}
	}
	lines := []render.Line{
		{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("%s 要対応 %d 件", iconFail, len(actions)), IconLen: iconRuneLen, IconStyle: styleForIcon(iconFail)},
	}
	for _, a := range actions {
		first := render.FirstLine(a.Text)
		var tags []string
		if a.New {
			tags = append(tags, "NEW")
		}
		if a.StreakDays >= 2 {
			tags = append(tags, fmt.Sprintf("%d日連続", a.StreakDays))
		}
		if len(tags) > 0 {
			first = first + "  " + strings.Join(tags, " ")
		}
		lines = append(lines, render.Line{Kind: render.KindBlank})
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("▸ [%d] %s", a.Step, first), Wrap: true, HangIndent: markerWidth(a.Step)})
		lines = append(lines, render.WrapText(a.Text, 8)...)
	}
	return render.Block{Lines: lines}
}

func listBlock(icon, label string, n int, items []item) render.Block {
	if n == 0 {
		return render.Block{}
	}
	lines := []render.Line{
		{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("%s %s %d 件", icon, label, n), IconLen: iconRuneLen, IconStyle: styleForIcon(icon)},
	}
	for _, it := range items {
		lines = append(lines, render.Line{Kind: render.KindBlank})
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("▸ [%d] %s", it.Step, render.FirstLine(it.Text)), Wrap: true, HangIndent: markerWidth(it.Step)})
		lines = append(lines, render.WrapText(it.Text, 8)...)
	}
	return render.Block{Lines: lines}
}

func formatDuration(ms int64) string {
	sec := float64(ms) / 1000
	if sec < 60 {
		return fmt.Sprintf("%.1fs", sec)
	}
	total := int64(sec)
	m := total / 60
	s := total % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

func shortenPath(p string) string {
	if p == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// markerWidth は "▸ [3] " のような行頭マーカーの表示幅を返す。折り返しの
// 継続行をここまで下げると、本文がマーカーの下に潜らず読み始めが揃う。
func markerWidth(step int) int {
	return textwidth.Width(fmt.Sprintf("▸ [%d] ", step))
}

// tallyBlock は末尾に、失敗したステップ名（critical かどうかを問わない）と
// 状態ごとの件数を出す。途中の結果行を読み飛ばしても、最後に失敗が見えるようにする。
func (s *State) tallyBlock() render.Block {
	c := s.counts
	lines := []render.Line{{Kind: render.KindBlank}}
	if len(s.failed) > 0 {
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("%s 失敗 %d 件: %s", iconFail, len(s.failed), strings.Join(s.failed, ", ")), IconLen: iconRuneLen, IconStyle: styleForIcon(iconFail), Wrap: true, HangIndent: 2})
	}
	lines = append(lines, render.Line{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("成功 %d 件 / 失敗 %d 件 / skip %d 件 / 対象外 %d 件", c.ok, c.fail, c.skip, c.cadence), Dim: true})
	return render.Block{Lines: lines}
}
