// Package textwidth は表示幅の計算・パディング・切り詰めを 1 箇所に閉じ込める。
// fmt.Sprintf の "%-Ns" 系パディングは文字数で詰めるため、全角文字が混ざると
// 桁が揃わない。このパッケージ以外では幅を意識したパディングを行わないこと
// （internal/render/nowidth_test.go が機械的に検査する）。
package textwidth

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

// cond は EastAsianWidth を明示的に固定した runewidth condition。
// 環境変数 RUNEWIDTH_EASTASIAN の値に挙動が左右されると、同じログでも
// 実行環境によって桁がずれる（この用途ではログの再現性を優先する）。
var cond = &runewidth.Condition{
	EastAsianWidth: false,
}

// ansiRe は ANSI エスケープシーケンス（色指定等）にマッチする。
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Width は s の表示幅を返す。ANSI エスケープシーケンスは幅 0 として扱う。
func Width(s string) int {
	return cond.StringWidth(stripANSI(s))
}

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansiRe.ReplaceAllString(s, "")
}

// Pad は s の表示幅が w に満たない場合、右側にスペースを足して w に揃える。
// 既に w 以上ならそのまま返す（切り詰めはしない）。
func Pad(s string, w int) string {
	d := w - Width(s)
	if d <= 0 {
		return s
	}
	return s + strings.Repeat(" ", d)
}

// PadLeft は s の表示幅が w に満たない場合、左側にスペースを足して w に揃える。
func PadLeft(s string, w int) string {
	d := w - Width(s)
	if d <= 0 {
		return s
	}
	return strings.Repeat(" ", d) + s
}

// Truncate は s の表示幅が w を超える場合、末尾を "…" に置き換えて w 以内に
// 切り詰める。ANSI エスケープシーケンスを含む文字列でも、シーケンス自体を
// 壊さず（幅計算からは除外して）切り詰める。w が 0 以下なら空文字を返す。
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}

	const ellipsis = "…"
	ellipsisW := Width(ellipsis)
	budget := w - ellipsisW
	if budget < 0 {
		budget = 0
	}

	var b strings.Builder
	width := 0
	runes := splitPreservingANSI(s)
	for _, tok := range runes {
		if tok.isEscape {
			b.WriteString(tok.text)
			continue
		}
		rw := cond.RuneWidth(tok.r)
		if width+rw > budget {
			break
		}
		width += rw
		b.WriteRune(tok.r)
	}
	if budget == 0 {
		return ellipsis
	}
	return b.String() + ellipsis
}

type token struct {
	isEscape bool
	text     string
	r        rune
}

// splitPreservingANSI は s を ANSI エスケープシーケンスとそれ以外の rune に
// 分解する。エスケープシーケンスはひとかたまりの token として扱う。
func splitPreservingANSI(s string) []token {
	var out []token
	rest := s
	for len(rest) > 0 {
		loc := ansiRe.FindStringIndex(rest)
		if loc == nil {
			for _, r := range rest {
				out = append(out, token{r: r})
			}
			break
		}
		for _, r := range rest[:loc[0]] {
			out = append(out, token{r: r})
		}
		out = append(out, token{isEscape: true, text: rest[loc[0]:loc[1]]})
		rest = rest[loc[1]:]
	}
	return out
}
