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
				fmt.Fprintln(w, renderTextLine(line, width, opt.Color))
			}
		}
	}
}

func renderTextLine(line Line, width int, color bool) string {
	prefix := strings.Repeat(" ", line.Indent)
	text := colorizeText(line, color)
	left := prefix + text
	if line.Right == "" {
		return left
	}
	right := line.Right
	if color {
		right = ansiDim + right + ansiReset
	}
	// JoinRow / textwidth は ANSI エスケープを幅 0 として扱うため、着色済みの
	// 文字列を渡しても桁揃えは崩れない。
	return JoinRow(left, right, width)
}

// colorizeText は line.Text に ANSI コードを埋め込む。着色対象は
// (1) IconLen > 0 なら先頭 IconLen rune だけを IconStyle の色にする、
// (2) Dim なら全体を dim にする（(1) と排他、Dim を優先）。
// どちらでもなければそのまま返す（本文を色で埋めない）。
func colorizeText(line Line, color bool) string {
	if !color {
		return line.Text
	}
	if line.Dim {
		return ansiDim + line.Text + ansiReset
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
		lines = append(lines, Line{Kind: KindText, Indent: continIndent, Text: t})
	}
	return lines
}

// FirstLine は改行を含みうる text の1行目だけを返す。
func FirstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}
