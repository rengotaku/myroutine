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

	nameCol = 32 // ステップ名 + summary を揃える表示幅

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
	switch st.Status {
	case "skip":
		s.pendingSkip = append(s.pendingSkip, st)
		return render.Block{}
	case "disabled":
		// disabled は summary イベントの Disabled 一覧（「⊘ 無効」ブロック）
		// だけで出す。ここで即時描画すると同じステップが二重に出る。
		return render.Block{}
	}
	return render.Block{Lines: s.stepLines(st)}
}

func (s *State) stepLines(st eventlog.StepEvent) []render.Line {
	icon := iconOK
	iconStyle := render.StyleOK
	if isFailStatus(st.Status) {
		icon = iconFail
		iconStyle = render.StyleFail
	}

	nameAndSummary := st.Name
	if st.Summary != "" {
		nameAndSummary = textwidth.Pad(nameAndSummary, nameCol) + st.Summary
	}

	// pos は run イベントの total と同じ集合（今回実行した無効化されていない
	// ステップ）で数えた通し番号。n（ステップ固有番号）は要対応サマリの
	// 「[3] ...」との対応付けに使うため、進捗表示にはこちらを使う。
	// pos を持たない古い入力（0 のまま）では従来どおり n にフォールバックする。
	pos := st.Pos
	if pos == 0 {
		pos = st.N
	}

	label := fmt.Sprintf("%s [%d/%d] %s", icon, pos, s.total, nameAndSummary)
	right := formatDuration(st.DurationMs)
	switch st.Status {
	case "fail":
		if st.RC != 0 {
			right = right + "  rc=" + strconv.Itoa(st.RC)
		}
	case "timeout":
		// rc だけでは通常の失敗と区別が付かない。制限秒数（note に
		// "${limit}s" の形で入っている）を出してタイムアウトと分かるようにする。
		right = right + "  timeout"
		if st.Note != "" {
			right = right + " " + st.Note
		}
		if st.RC != 0 {
			right = right + "  rc=" + strconv.Itoa(st.RC)
		}
	}

	lines := []render.Line{{Kind: render.KindText, Indent: 2, Text: label, Right: right, IconLen: iconRuneLen, IconStyle: iconStyle}}

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

// FlushSkips は溜めていた skip イベントを集約 Block として返す。summary
// イベントを受け取ったとき、または stdin がそれ以前に閉じたときに呼ぶ。
func (s *State) FlushSkips() render.Block {
	if len(s.pendingSkip) == 0 {
		return render.Block{}
	}
	lines := []render.Line{
		{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("%s skip %d 件", iconSkip, len(s.pendingSkip)), IconLen: iconRuneLen, IconStyle: styleForIcon(iconSkip)},
	}
	for _, st := range s.pendingSkip {
		note := strings.TrimPrefix(st.Note, "cadence: ")
		label := fmt.Sprintf("[%d] %s", st.N, textwidth.Pad(st.Name, nameCol)) + note
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 6, Text: label, Dim: true})
		lines = append(lines, render.WrapText(note, 10)...)
	}
	s.pendingSkip = nil
	return render.Block{Lines: lines}
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
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 6, Text: fmt.Sprintf("[%d] %s", it.N, it.Name)})
		if it.Reason != "" {
			lines = append(lines, render.Line{Kind: render.KindText, Indent: 10, Text: it.Reason, Dim: true})
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
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("▸ [%d] %s", a.Step, first)})
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
		lines = append(lines, render.Line{Kind: render.KindText, Indent: 2, Text: fmt.Sprintf("▸ [%d] %s", it.Step, render.FirstLine(it.Text))})
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
