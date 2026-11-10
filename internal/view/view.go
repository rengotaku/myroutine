// Package view は要対応の履歴（internal/ledger）を 1 枚の自己完結した HTML にする。
// 外部の CSS / JS / フォントは読まない。本文は html/template でエスケープする。
package view

import (
	"bytes"
	_ "embed"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/rengotaku/myroutine/internal/ledger"
)

// ClosedWindow は対応済み・解消済みを表示に残す期間。
const ClosedWindow = 30 * 24 * time.Hour

// Input は描画に必要なもの一式。
type Input struct {
	Entries []ledger.Entry
	Skipped int // 履歴の壊れた行数
	Now     time.Time
	Host    string
}

// Card は画面に出る 1 件。
type Card struct {
	ID       string
	Slug     string
	StepName string
	Body     []string // コマンド行以外の本文の行
	Cmds     []string // コピー対象のコマンド（コメント除去済み）
	DoneCmd  string   // 未対応のときだけ。`myroutine done <id>`
	First    string   // MM-DD
	Last     string   // MM-DD
	Seen     int
	Closed   string // 閉じた日 MM-DD
	Label    string // 「対応済み」「解消」（閉じたものだけ）
}

type page struct {
	Host       string
	Stamp      string
	Skipped    int
	Open       []Card
	Closed     []Card
	OpenCount  int
	ClosedDays int
}

//go:embed view.html.tmpl
var tmplSrc string

var tmpl = template.Must(template.New("view").Parse(tmplSrc))

// Render は HTML を返す。
func Render(in Input) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, build(in)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func build(in Input) page {
	loc := in.Now.Location()
	var open, closed []ledger.Entry
	for _, e := range in.Entries {
		switch e.Status {
		case ledger.StatusOpen:
			open = append(open, e)
		default:
			// 閉じた日時が直近 30 日（ちょうど 30 日前を含む）のものだけ
			if in.Now.Sub(e.ClosedAt) <= ClosedWindow {
				closed = append(closed, e)
			}
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].FirstAt.Before(open[j].FirstAt) })
	sort.SliceStable(closed, func(i, j int) bool { return closed[i].ClosedAt.After(closed[j].ClosedAt) })

	p := page{
		Host:       in.Host,
		Stamp:      in.Now.Format("2006-01-02 15:04"),
		Skipped:    in.Skipped,
		OpenCount:  len(open),
		ClosedDays: int(ClosedWindow / (24 * time.Hour)),
	}
	for _, e := range open {
		c := card(e, loc)
		c.DoneCmd = "myroutine done " + e.ID
		p.Open = append(p.Open, c)
	}
	for _, e := range closed {
		c := card(e, loc)
		c.Closed = e.ClosedAt.In(loc).Format("01-02")
		c.Label = "解消"
		if e.Status == ledger.StatusDone {
			c.Label = "対応済み"
		}
		p.Closed = append(p.Closed, c)
	}
	return p
}

func card(e ledger.Entry, loc *time.Location) Card {
	body, cmds := SplitText(e.Text)
	name := e.StepName
	if name == "" {
		name = e.Slug
	}
	return Card{
		ID: e.ID, Slug: e.Slug, StepName: name, Body: body, Cmds: cmds,
		First: e.FirstAt.In(loc).Format("01-02"), Last: e.LastAt.In(loc).Format("01-02"), Seen: e.Seen,
	}
}

// SplitText は要対応の本文を、コマンド以外の行とコマンドに分ける。
// コマンド行は行頭の空白を除いて「→ $ 」か「$ 」で始まる行。コマンドは # 以降の
// コメントを外す（コピーして打つものにコメントを混ぜない）。
func SplitText(text string) (body, cmds []string) {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		var cmd string
		switch {
		case strings.HasPrefix(t, "→ $ "):
			cmd = t[len("→ $ "):]
		case strings.HasPrefix(t, "$ "):
			cmd = t[len("$ "):]
		default:
			if t != "" {
				body = append(body, t)
			}
			continue
		}
		if cmd = stripComment(cmd); cmd != "" {
			cmds = append(cmds, cmd)
		}
	}
	return body, cmds
}

// stripComment は空白の直後にある（引用符の外の）# 以降を落とす。
// バックスラッシュを含む行はエスケープを正しく追えないので、切らずにそのまま返す。
func stripComment(s string) string {
	if strings.ContainsRune(s, '\\') {
		return strings.TrimSpace(s)
	}
	var quote rune
	prevSpace := true
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '#' && prevSpace:
			return strings.TrimSpace(s[:i])
		}
		prevSpace = r == ' ' || r == '\t'
	}
	return strings.TrimSpace(s)
}
