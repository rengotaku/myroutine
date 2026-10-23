// Package render は myroutine-render の描画層。イベント処理側は Line / Block
// を組み立てるだけにし、実際に io.Writer へ書き出すのは Render のこの1箇所に
// 閉じ込める（fmt.Fprintf をイベント処理側に散らばらせない）。
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/rengotaku/myroutine/internal/textwidth"
)

// Kind は Line の種別。
type Kind int

const (
	// KindText は通常のテキスト行。
	KindText Kind = iota
	// KindBlank は空行。
	KindBlank
	// KindRule は Width いっぱいの罫線。
	KindRule
)

// Style は Line の先頭アイコンに当てる色の意味を表す。実際の ANSI コードは
// このパッケージに閉じ込め、呼び出し側（internal/format）は意味だけを渡す。
type Style int

const (
	// StyleNone は着色しない。
	StyleNone Style = iota
	// StyleOK は成功系（✓ 等）。
	StyleOK
	// StyleFail は失敗系（✗ 等）。
	StyleFail
	// StyleSkip は skip/無効系（⊘ 等）。
	StyleSkip
)

// Line は1行分の描画情報を持つ。
type Line struct {
	Kind Kind
	// Indent は行頭に足す半角スペースの数。
	Indent int
	// Text は Indent の後に置くテキスト（KindText のときのみ使う）。
	Text string
	// Right が空でなければ、Text を右寄せの Right と同じ行に揃えて描画する。
	// Options.Color が有効なとき、Right は所要時間・補足として常に dim で描画する。
	Right string
	// RuleChar は KindRule のときに繰り返す文字（既定は "═"）。
	RuleChar string
	// IconLen は Text 先頭の何 rune 分をアイコンとして IconStyle で着色するか。
	// 0 なら着色しない。
	IconLen int
	// IconStyle は IconLen > 0 のときのアイコンの色。
	IconStyle Style
	// Dim が true なら Text 全体を補足情報として dim で描画する
	// （tail 抜粋・全文ログ導線・skip/無効の理由等）。IconLen による着色とは
	// 排他（Dim を優先する）。
	Dim bool
	// Wrap が true で Right=="" のとき、Text が表示幅を超える行は末尾を
	// 切り詰めず複数行へ折り返す（説明文向け）。継続行は Indent をそのまま
	// 使うため、1 行目の本文開始位置に自動で揃う。false（既定）の行は
	// 幅を超えた分を "…" で切り詰める（tail 抜粋・ログパス等、原文の断片が
	// 意味を持つ行向け）。--width の契約はどちらの経路でも必ず守る。
	Wrap bool
	// HangIndent は折り返しの継続行にだけ足す追加インデント。Text が
	// "▸ [3] " のような行頭マーカーを含む場合、継続行を Indent のまま
	// 出すとマーカーの下に本文が潜り込んで境目が読めない。マーカーの
	// 表示幅を入れると、継続行が本文開始位置にぶら下がる。
	HangIndent int
	// Verbatim が true の行は折り返しも切り詰めもせず、幅を超えても 1 行の
	// まま出す（コピーして実行するコマンド行向け）。描画側で改行と空白を
	// 挟むと貼り付けたコマンドが壊れる。端末の自動折り返しはコピー時に
	// 1 行に戻るため、この行に限り --width の上限から外す。
	Verbatim bool
	// Command が true なら、Text の「$ 」から行末までを着色する（要対応の
	// 次に打つコマンドを、説明の行と見分けるため）。行頭の空白と「→」は
	// 色の外に残す。Text に「$ 」が無ければ着色しない。
	Command bool
}

// Block は見出し（省略可）と Line の並び。
type Block struct {
	Lines []Line
}

// Options は描画パラメータ。
type Options struct {
	Width int
	Color bool
}

const defaultWidth = 100

const (
	ansiReset      = "\x1b[0m"
	ansiGreen      = "\x1b[32m"
	ansiRed        = "\x1b[31m"
	ansiBrightGray = "\x1b[90m"
	ansiDim        = "\x1b[2m"
	ansiCommand    = "\x1b[1;36m"
)

func ansiForStyle(st Style) string {
	switch st {
	case StyleOK:
		return ansiGreen
	case StyleFail:
		return ansiRed
	case StyleSkip:
		return ansiBrightGray
	default:
		return ""
	}
}

// Render は blocks を w へ書き出す唯一の入口。
func Render(w io.Writer, blocks []Block, opt Options) {
	width := opt.Width
	if width <= 0 {
		width = defaultWidth
	}
	for _, block := range blocks {
		for _, line := range block.Lines {
			switch line.Kind {
			case KindBlank:
				fmt.Fprintln(w)
			case KindRule:
				ch := line.RuleChar
				if ch == "" {
					ch = "═"
				}
				fmt.Fprintln(w, strings.Repeat(ch, width/textwidth.Width(ch)))
			default:
				for _, s := range renderTextLines(line, width, opt.Color) {
					fmt.Fprintln(w, s)
				}
			}
		}
	}
}

// renderTextLines は KindText の1つの Line を、実際に出力する物理行（1行
// または複数行）へ変換する。Right を持つ行は JoinRow が既に幅を守っている
// ため常に1行。Right を持たない行は、幅を超えた分を Wrap の有無で
// 折り返すか切り詰めるかに振り分ける（どちらの経路でも幅を超えて出力
// しない）。
func renderTextLines(line Line, width int, color bool) []string {
	prefix := strings.Repeat(" ", line.Indent)
	if line.Right != "" {
		left := prefix + colorizeText(line, color)
		right := line.Right
		if color {
			right = ansiDim + right + ansiReset
		}
		// JoinRow / textwidth は ANSI エスケープを幅 0 として扱うため、
		// 着色済みの文字列を渡しても桁揃えは崩れない。
		return []string{JoinRow(left, right, width)}
	}

	left := prefix + colorizeText(line, color)
	if line.Verbatim || textwidth.Width(left) <= width {
		return []string{left}
	}
	if !line.Wrap {
		return []string{textwidth.Truncate(left, width)}
	}

	// 折り返しは着色前の Text に対して行う。着色（Dim / アイコン）を先に
	// 適用してしまうと ANSI エスケープが物理行をまたぎ、端末側で色が
	// 閉じない・継続行に余計な色が乗る等の崩れが起きるため、行ごとに
	// 都度着色し直す。
	// 1 行目と継続行で使える幅が違う（継続行は HangIndent の分だけ狭い）。
	// 同じ budget で割ると継続行だけ幅をはみ出すため、分けて折り返す。
	firstBudget := width - line.Indent
	contBudget := width - line.Indent - line.HangIndent
	if firstBudget < 1 {
		firstBudget = 1
	}
	if contBudget < 1 {
		contBudget = 1
	}
	chunks := textwidth.WrapWidths(line.Text, firstBudget, contBudget)
	contPrefix := prefix + strings.Repeat(" ", line.HangIndent)
	out := make([]string, 0, len(chunks))
	for i, c := range chunks {
		sub := line
		sub.Text = c
		if i == 0 {
			out = append(out, prefix+colorizeText(sub, color))
			continue
		}
		// アイコンは1行目の先頭にしか無い。
		sub.IconLen = 0
		out = append(out, contPrefix+colorizeText(sub, color))
	}
	return out
}

// colorizeText は line.Text に ANSI コードを埋め込む。着色対象は
// (1) IconLen > 0 なら先頭 IconLen rune だけを IconStyle の色にする、
// (2) Dim なら全体を dim にする（(1) と排他、Dim を優先）、
// (3) Command なら「$ 」から行末までを着色する。
// どれでもなければそのまま返す（本文を色で埋めない）。
func colorizeText(line Line, color bool) string {
	if !color {
		return line.Text
	}
	if line.Dim {
		return ansiDim + line.Text + ansiReset
	}
	if line.Command {
		if i := strings.Index(line.Text, "$ "); i >= 0 {
			return line.Text[:i] + ansiCommand + line.Text[i:] + ansiReset
		}
		return line.Text
	}
	if line.IconLen > 0 && line.IconStyle != StyleNone {
		code := ansiForStyle(line.IconStyle)
		if code == "" {
			return line.Text
		}
		runes := []rune(line.Text)
		if line.IconLen > len(runes) {
			return line.Text
		}
		icon := string(runes[:line.IconLen])
		rest := string(runes[line.IconLen:])
		return code + icon + ansiReset + rest
	}
	return line.Text
}

// JoinRow は left と right を、表示幅 width の1行に収める。
// left が width - width(right) - 2 を超える場合は末尾を切り詰める。
func JoinRow(left, right string, width int) string {
	rw := textwidth.Width(right)
	budget := width - rw - 2
	if budget < 1 {
		budget = 1
	}
	if textwidth.Width(left) > budget {
		left = textwidth.Truncate(left, budget)
	}
	gap := width - textwidth.Width(left) - rw
	if gap < 2 {
		gap = 2
	}
	return left + strings.Repeat(" ", gap) + right
}

// WrapText は複数行になりうるテキストを、1行目とそれ以降で扱いを分けて
// Line のスライスに変換する。「→ $ command」形式の行は "$ command" として
// 継続行と同じインデントに揃える。
//
// first は既に組み立て済みの1行目テキスト（呼び出し側が icon/label を含めて
// 用意する）。text の2行目以降だけを continIndent で継続行として追加する。
func WrapText(text string, continIndent int) []Line {
	parts := strings.Split(text, "\n")
	if len(parts) <= 1 {
		return nil
	}
	var lines []Line
	for _, p := range parts[1:] {
		t := p
		if strings.HasPrefix(t, "→ $ ") {
			t = "$ " + strings.TrimPrefix(t, "→ $ ")
		} else if strings.HasPrefix(t, "$ ") {
			// 既に "$ " 形式ならそのまま。
		}
		if isCommandLine(t) {
			lines = append(lines, Line{Kind: KindText, Indent: continIndent, Text: t, Verbatim: true})
			continue
		}
		lines = append(lines, Line{Kind: KindText, Indent: continIndent, Text: t, Wrap: true})
	}
	return lines
}

// isCommandLine は、人がコピーして実行する前提のコマンド行かを返す。
// ステップの add_action 本文は継続行の先頭に空白を含むため、空白を除いて判定する。
func isCommandLine(t string) bool {
	s := strings.TrimLeft(t, " ")
	return strings.HasPrefix(s, "→ $ ") || strings.HasPrefix(s, "$ ")
}

// HasCommandLine は、要対応の本文 text の 2 行目以降にコマンド行があるかを返す。
// 1 行目は見出しとして扱われ、コマンド行としては描画されない（WrapText と同じ）。
func HasCommandLine(text string) bool {
	parts := strings.Split(text, "\n")
	for _, p := range parts[1:] {
		if isCommandLine(p) {
			return true
		}
	}
	return false
}

// FirstLine は改行を含みうる text の1行目だけを返す。
func FirstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}
