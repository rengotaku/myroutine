// Package view は要対応の履歴（internal/ledger）を 1 枚の自己完結した HTML にする。
// 外部の CSS / JS / フォントは読まない。本文は html/template でエスケープする。
package view

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/rengotaku/myroutine/internal/ledger"
	"github.com/rengotaku/myroutine/internal/night"
)

// ClosedWindow は対応済み・解消済みを表示に残す期間。
const ClosedWindow = 30 * 24 * time.Hour

// Input は描画に必要なもの一式。
type Input struct {
	Entries []ledger.Entry
	Skipped int // 履歴の壊れた行数
	Now     time.Time
	Host    string
	// Night は夜間実行のタブの入力。nil のときはタブを出さず、今までの画面と同じ出力にする。
	Night *NightInput
}

// NightInput は夜間実行のタブに出す、読み込み済みの入力一式。
// 読み込み（internal/night）は view の外（view サブコマンド）が行う。
type NightInput struct {
	Report    *night.Report
	ReportErr string
	Status    *night.Status
	StatusErr string
	Acks      map[string]time.Time
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
	Night      *nightPage
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
	if in.Night != nil {
		p.Night = buildNight(*in.Night, in.Now, loc)
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

// ─────────────────────────────────────────────────────────────────
// 夜間実行のタブ
// ─────────────────────────────────────────────────────────────────

// nightCategories は前の夜の結果の 4 区分（表示順）。
var nightCategories = []struct{ Key, Label string }{
	{"done", "完了"},
	{"stopped", "停止"},
	{"error", "異常終了"},
	{"timeout", "時間切れ"},
}

type nightPage struct {
	NightCount int  // 夜間実行の要対応（停止 + 異常終了 + 時間切れ。対応済みを除く）
	HasReport  bool // 結果を読めた
	ReportErr  string
	Generated  string // generated_at の表示（例 2026-11-13 09:03）
	Stale      string // 「今朝のレポートがありません…」（今朝のものなら空）
	Done       int
	Stopped    int
	Error      int
	Timeout    int
	Empty      bool // 前の夜に動いた件が 0
	Groups     []nightGroup
	Acked      []nightCard
	AckedDays  int
	Band       *band
}

type nightGroup struct {
	Key, Label string
	Count      int
	Cards      []nightCard
}

type nightCard struct {
	ID        string
	Title     string
	Category  string
	State     string
	Reason    string
	Progress  string
	Meta      string
	ResumeCmd string // 無ければコピーのボタンを出さない
	DoneCmd   string // 空なら出さない（完了・実行中・対応済み）
}

// band は「いまの状態」の帯。3 つの状態（live / idle / stale）と、読めないとき（err）。
type band struct {
	Kind, Head, Message string
	RunningItems        []bandItem
	QueuedItems         []bandItem
	StaleItems          []bandItem
}

type bandItem struct {
	Title  string
	Detail string
}

func buildNight(in NightInput, now time.Time, loc *time.Location) *nightPage {
	np := &nightPage{AckedDays: int(ClosedWindow / (24 * time.Hour))}

	// 帯（いまの状態）は、前の夜の結果とは独立に出す
	switch {
	case in.StatusErr != "":
		np.Band = &band{Kind: "err", Head: "状態を読めません", Message: in.StatusErr}
	case in.Status != nil:
		np.Band = buildBand(*in.Status, now, loc)
	}

	if in.ReportErr != "" {
		np.ReportErr = in.ReportErr
		if in.Status != nil {
			np.NightCount = in.Status.NeedsAttention
		}
		return np
	}
	if in.Report == nil {
		np.ReportErr = "結果が渡されていません"
		return np
	}
	rep := *in.Report
	np.HasReport = true
	np.Generated = stampOf(rep.GeneratedAt, loc)
	np.Stale = staleNote(rep.GeneratedAt, now, loc)
	np.Empty = len(rep.Jobs) == 0

	byKey := map[string][]nightCard{}
	for _, j := range rep.Jobs {
		c := cardOf(j, loc)
		if at, ok := in.Acks[j.ID]; ok {
			// 対応済みは一覧と件数から外す。記録から 30 日は折りたたみに残す。
			if now.Sub(at) <= ClosedWindow {
				c.DoneCmd = ""
				np.Acked = append(np.Acked, c)
			}
			continue
		}
		switch j.Category {
		case "done":
			c.DoneCmd = ""
			byKey["done"] = append(byKey["done"], c)
		case "stopped", "error", "timeout":
			byKey[j.Category] = append(byKey[j.Category], c)
		case "running":
			// 実行中は要対応ではない（異常終了の一覧に出さない）
			c.DoneCmd = ""
			byKey["running"] = append(byKey["running"], c)
		default:
			// 4 区分と running 以外は落とさず「その他」に置く（RF-7）
			byKey["other"] = append(byKey["other"], c)
		}
	}

	for _, cat := range nightCategories {
		cards := byKey[cat.Key]
		switch cat.Key {
		case "done":
			np.Done = len(cards)
		case "stopped":
			np.Stopped = len(cards)
		case "error":
			np.Error = len(cards)
		case "timeout":
			np.Timeout = len(cards)
		}
		if len(cards) > 0 {
			np.Groups = append(np.Groups, nightGroup{Key: cat.Key, Label: cat.Label, Count: len(cards), Cards: cards})
		}
	}
	if cards := byKey["running"]; len(cards) > 0 {
		np.Groups = append(np.Groups, nightGroup{Key: "running", Label: "実行中", Count: len(cards), Cards: cards})
	}
	if cards := byKey["other"]; len(cards) > 0 {
		np.Groups = append(np.Groups, nightGroup{Key: "other", Label: "その他", Count: len(cards), Cards: cards})
	}
	np.NightCount = np.Stopped + np.Error + np.Timeout
	return np
}

func cardOf(j night.Job, loc *time.Location) nightCard {
	c := nightCard{
		ID:       j.ID,
		Title:    titleOf(j.Repo, j.Issue, j.ID),
		Category: j.Category,
		State:    j.State,
		DoneCmd:  "myroutine night-done " + j.ID,
	}
	if j.Reason != nil {
		c.Reason = *j.Reason
	}
	if j.Progress != nil {
		c.Progress = *j.Progress
	}
	if j.ResumeCmd != nil {
		c.ResumeCmd = *j.ResumeCmd
	}
	var meta []string
	if j.Room != nil && *j.Room != "" {
		meta = append(meta, "room "+*j.Room)
	}
	if j.PR != nil {
		meta = append(meta, fmt.Sprintf("PR #%d", *j.PR))
	}
	if j.StartedAt != nil {
		if s := stampOf(*j.StartedAt, loc); s != "" {
			meta = append(meta, "開始 "+s)
		}
	}
	if j.FinishedAt != nil {
		if s := stampOf(*j.FinishedAt, loc); s != "" {
			meta = append(meta, "終了 "+s)
		}
	}
	c.Meta = strings.Join(meta, " ・ ")
	return c
}

func buildBand(st night.Status, now time.Time, loc *time.Location) *band {
	b := &band{}
	queued := len(st.Queued)
	switch {
	case st.ExecutorAlive:
		b.Kind = "live"
		b.Head = fmt.Sprintf("実行中 %d 件 ・ 待機 %d 件", len(st.Running), queued)
		for _, j := range st.Running {
			b.RunningItems = append(b.RunningItems, bandItem{
				Title:  titleOf(j.Repo, j.Issue, j.ID),
				Detail: runningDetail(j, now, loc),
			})
		}
		for _, j := range sortedQueued(st.Queued) {
			var detail string
			if j.QueuedAt != nil {
				if s := stampOf(*j.QueuedAt, loc); s != "" {
					detail = "積んだ " + s
				}
			}
			b.QueuedItems = append(b.QueuedItems, bandItem{Title: titleOf(j.Repo, j.Issue, j.ID), Detail: detail})
		}
	case len(st.StaleRunning) > 0:
		b.Kind = "stale"
		b.Head = fmt.Sprintf("実行中 0 件 ・ 待機 %d 件", queued)
		b.Message = "実行本体が動いていません。running のまま残っている件は『異常終了』に数えます"
		for _, j := range st.StaleRunning {
			b.StaleItems = append(b.StaleItems, bandItem{
				Title:  titleOf(j.Repo, j.Issue, j.ID),
				Detail: runningDetail(j, now, loc),
			})
		}
	default:
		b.Kind = "idle"
		b.Head = fmt.Sprintf("実行中 0 件 ・ 待機 %d 件", queued)
		b.Message = fmt.Sprintf("いまは動いていません。待機中の %d 件は、次の実行で処理します", queued)
	}
	return b
}

// sortedQueued は待機を積んだ時刻の古い順に並べる（時刻が無い・読めないものは最後）。
func sortedQueued(in []night.StatusJob) []night.StatusJob {
	out := append([]night.StatusJob(nil), in...)
	parse := func(p *string) (time.Time, bool) {
		if p == nil {
			return time.Time{}, false
		}
		t, err := time.Parse(time.RFC3339, *p)
		return t, err == nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, aok := parse(out[i].QueuedAt)
		b, bok := parse(out[j].QueuedAt)
		if !aok || !bok {
			return aok // 読めたものが先
		}
		return a.Before(b)
	})
	return out
}

func runningDetail(j night.StatusJob, now time.Time, loc *time.Location) string {
	var parts []string
	if j.StartedAt != nil {
		if s := stampOf(*j.StartedAt, loc); s != "" {
			parts = append(parts, "開始 "+s)
		}
		if t, err := time.Parse(time.RFC3339, *j.StartedAt); err == nil {
			if d := now.Sub(t); d > 0 {
				parts = append(parts, "経過 "+elapsed(d))
			}
		}
	}
	if j.Room != nil && *j.Room != "" {
		parts = append(parts, *j.Room)
	}
	return strings.Join(parts, " ・ ")
}

func elapsed(d time.Duration) string {
	m := int(d.Minutes())
	if m < 60 {
		return fmt.Sprintf("%d分", m)
	}
	return fmt.Sprintf("%d時間%d分", m/60, m%60)
}

// stampOf は RFC3339 の時刻を、表示用（01-02 15:04 ではなく年も入る形）にする。
// 読めない値はそのまま返す（画面に "null" は出さない）。
func stampOf(s string, loc *time.Location) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.In(loc).Format("2006-01-02 15:04")
}

// staleNote は generated_at の日付が「いま」（Now のタイムゾーン）と違うときの注記を返す。
// タイムゾーンの違う時刻をそのまま日付で比べないよう、generated_at を Now の
// タイムゾーンへ直してから比べる（RF-6）。
func staleNote(gen string, now time.Time, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339, gen)
	if err != nil {
		return "今朝のレポートがありません（前回: 不明）"
	}
	g := t.In(loc)
	if g.Year() == now.Year() && g.Month() == now.Month() && g.Day() == now.Day() {
		return ""
	}
	return fmt.Sprintf("今朝のレポートがありません（前回: %d月%d日）", int(g.Month()), g.Day())
}

func titleOf(repo string, issue int, id string) string {
	t := repo
	if issue > 0 {
		if t != "" {
			t = fmt.Sprintf("%s#%d", t, issue)
		} else {
			t = fmt.Sprintf("#%d", issue)
		}
	}
	if t == "" {
		t = id
	}
	return t
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
