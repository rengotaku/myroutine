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

// Line は1行分の描画情報を持つ。
type Line struct {
	Kind Kind
	// Indent は行頭に足す半角スペースの数。
	Indent int
	// Text は Indent の後に置くテキスト（KindText のときのみ使う）。
	Text string
	// Right が空でなければ、Text を右寄せの Right と同じ行に揃えて描画する。
	Right string
	// RuleChar は KindRule のときに繰り返す文字（既定は "═"）。
	RuleChar string
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
				fmt.Fprintln(w, renderTextLine(line, width))
			}
		}
	}
}

func renderTextLine(line Line, width int) string {
	prefix := strings.Repeat(" ", line.Indent)
	if line.Right == "" {
		return prefix + line.Text
	}
	left := prefix + line.Text
	return JoinRow(left, line.Right, width)
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
