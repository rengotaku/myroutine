package view

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rengotaku/myroutine/internal/ledger"
	"github.com/rengotaku/myroutine/internal/night"
)

var now = time.Date(2026, 11, 10, 9, 12, 0, 0, time.UTC)

func day(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }

func render(t *testing.T, in Input) string {
	t.Helper()
	in.Now = now
	in.Host = "host1"
	b, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSplitText(t *testing.T) {
	body, cmds := SplitText("棚卸しする\n理由: 古い\n      → $ /permanent-fix review   # 見る\n  $ echo 'a #b' # c\n→ $ # だけ\n")
	if !reflect.DeepEqual(body, []string{"棚卸しする", "理由: 古い"}) {
		t.Errorf("body = %q", body)
	}
	if !reflect.DeepEqual(cmds, []string{"/permanent-fix review", "echo 'a #b'"}) {
		t.Errorf("cmds = %q", cmds)
	}
}

func TestEscapes(t *testing.T) {
	out := render(t, Input{Entries: []ledger.Entry{{
		ID: "a.x", Slug: "a", StepName: "<b>名</b>", Status: ledger.StatusOpen, Seen: 1,
		FirstAt: day(3), LastAt: day(3), Text: "<script>alert(1)</script>\n→ $ echo \"<img>\"",
	}}})
	if strings.Contains(out, "<script>alert(1)") || strings.Contains(out, "<img>") || strings.Contains(out, "<b>名") {
		t.Error("本文がエスケープされていない")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)") {
		t.Error("エスケープ後の本文が無い")
	}
}

func TestOrderAndWindow(t *testing.T) {
	es := []ledger.Entry{
		{ID: "new.o", Slug: "new", Status: ledger.StatusOpen, FirstAt: day(8), LastAt: day(9), Seen: 1},
		{ID: "old.o", Slug: "old", Status: ledger.StatusOpen, FirstAt: day(2), LastAt: day(9), Seen: 3},
		// ちょうど 30 日前は含む、1 秒超えたら含まない
		{ID: "edge.in", Slug: "e", Status: ledger.StatusDone, FirstAt: day(1), LastAt: day(1), ClosedAt: now.Add(-ClosedWindow)},
		{ID: "edge.out", Slug: "e", Status: ledger.StatusDone, FirstAt: day(1), LastAt: day(1), ClosedAt: now.Add(-ClosedWindow - time.Second)},
		{ID: "recent.c", Slug: "r", Status: ledger.StatusCleared, FirstAt: day(1), LastAt: day(1), ClosedAt: day(9)},
	}
	out := render(t, Input{Entries: es})
	if strings.Index(out, "old.o") > strings.Index(out, "new.o") {
		t.Error("未対応は初出が古い順")
	}
	if !strings.Contains(out, "edge.in") || strings.Contains(out, "edge.out") {
		t.Error("30 日の境界")
	}
	if strings.Index(out, "recent.c") > strings.Index(out, "edge.in") {
		t.Error("対応済みは閉じた日時が新しい順")
	}
	for _, want := range []string{"解消 11-09", "対応済み 10-11", "未対応 2 件", "myroutine done old.o", "初出 11-02 ・ 最終 11-09 ・ 3 回"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出力に無い", want)
		}
	}
	if strings.Contains(out, "myroutine done edge.in") {
		t.Error("閉じたものに done コマンドを出さない")
	}
}

func TestEmptyAndSkipped(t *testing.T) {
	out := render(t, Input{})
	if !strings.Contains(out, "未対応はありません") || strings.Contains(out, "壊れた行") {
		t.Errorf("0 件: %s", out)
	}
	out = render(t, Input{Skipped: 2})
	if !strings.Contains(out, "壊れた行を 2 行") {
		t.Error("壊れた行の件数が出ない")
	}
}

// エスケープした引用符を含む行は、# の後ろも切らずにそのまま残す。
func TestStripCommentKeepsEscapedQuote(t *testing.T) {
	in := `printf '%s\n' "a \" # b"`
	if got := stripComment(in); got != in {
		t.Fatalf("stripComment(%q) = %q, want unchanged", in, got)
	}
	if got := stripComment("ls -l   # 内訳だけ見る"); got != "ls -l" {
		t.Fatalf("comment not stripped: %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────
// 夜間実行のタブ
// ─────────────────────────────────────────────────────────────────

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

// goldenEntries は testdata/today-only.html を作ったときと同じ入力。
func goldenEntries() []ledger.Entry {
	return []ledger.Entry{
		{ID: "a.pending", Slug: "a", StepName: "棚卸し", Status: ledger.StatusOpen, Seen: 2,
			FirstAt: day(3), LastAt: day(9), Text: "棚卸しする\n→ $ make review # 見る"},
		{ID: "b.done", Slug: "b", StepName: "掃除", Status: ledger.StatusDone, Seen: 1,
			FirstAt: day(1), LastAt: day(1), ClosedAt: day(9)},
	}
}

// Night が nil のときの出力は、夜間実行のタブを足す前とバイトで同じ（既存の画面を変えない）。
func TestNightTabHiddenWhenNil(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "today-only.html"))
	if err != nil {
		t.Fatal(err)
	}
	got := render(t, Input{Entries: goldenEntries(), Skipped: 1})
	if got != string(want) {
		t.Errorf("Night == nil の出力が変わった（%d → %d バイト）", len(want), len(got))
	}
}

func TestNightFourCategories(t *testing.T) {
	rep := &night.Report{
		GeneratedAt: "2026-11-10T09:00:00+00:00",
		Jobs: []night.Job{
			{ID: "d1", Repo: "o/n", Issue: 1, Category: "done"},
			{ID: "s1", Repo: "o/n", Issue: 2, Category: "stopped"},
			{ID: "e1", Repo: "o/n", Issue: 3, Category: "error"},
			{ID: "t1", Repo: "o/n", Issue: 4, Category: "timeout"},
		},
	}
	out := render(t, Input{Night: &NightInput{Report: rep}})
	for _, want := range []string{"完了 1", "停止 1", "異常終了 1", "時間切れ 1", "o/n#1", "o/n#2", "o/n#3", "o/n#4"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出力に無い", want)
		}
	}
	// 区分ごとの一覧は done → stopped → error → timeout の順
	order := []string{"完了 1 件", "停止 1 件", "異常終了 1 件", "時間切れ 1 件"}
	for i := 1; i < len(order); i++ {
		if strings.Index(out, order[i-1]) > strings.Index(out, order[i]) {
			t.Errorf("%q が %q より後にある", order[i-1], order[i])
		}
	}
	if !strings.Contains(out, "夜間実行の要対応 3 件") {
		t.Error("夜間実行の要対応（停止+異常+時間切れ）の件数が出ない")
	}
}

func TestNightCardsAndCopyButtons(t *testing.T) {
	resume := `room resume night-x "quoted"`
	rep := &night.Report{
		GeneratedAt: "2026-11-10T09:00:00+00:00",
		Jobs: []night.Job{
			{ID: "s1", Repo: "o/n", Issue: 7, Category: "stopped", State: "waiting-human",
				Reason:   strptr("承認境界: <script>alert(1)</script>"),
				Progress: strptr("<b>引き継ぎ</b>の先頭"), Room: strptr("night-x"),
				PR: intptr(12), ResumeCmd: &resume},
			{ID: "d1", Repo: "o/n", Issue: 8, Category: "done"},
			{ID: "e1", Repo: "o/n", Issue: 9, Category: "error"},
		},
	}
	out := render(t, Input{Night: &NightInput{Report: rep}})
	for _, want := range []string{
		"承認境界: ", "&lt;script&gt;alert(1)&lt;/script&gt;",
		"&lt;b&gt;引き継ぎ&lt;/b&gt;の先頭",
		"room night-x", "PR #12",
		`data-cmd="room resume night-x &#34;quoted&#34;"`,
		"myroutine night-done s1", "myroutine night-done e1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出力に無い", want)
		}
	}
	if strings.Contains(out, "<script>alert(1)") || strings.Contains(out, "<b>引き継ぎ") {
		t.Error("本文がエスケープされていない")
	}
	if strings.Contains(out, "night-done d1") {
		t.Error("完了の件に night-done を出している")
	}
	// 再開コマンドが null の件（e1）にはコピーのボタンを出さない
	if n := strings.Count(out, `data-cmd="room resume`); n != 1 {
		t.Errorf("再開コマンドのコピー = %d 個", n)
	}
	if strings.Contains(out, "null") {
		t.Error("null という文字が出ている")
	}
}

func TestNightAckedHidden(t *testing.T) {
	rep := &night.Report{
		GeneratedAt: "2026-11-10T09:00:00+00:00",
		Jobs: []night.Job{
			{ID: "s1", Repo: "o/s", Issue: 1, Category: "stopped"},
			{ID: "e1", Repo: "o/e", Issue: 2, Category: "error"},
			{ID: "t1", Repo: "o/t", Issue: 3, Category: "timeout"},
		},
	}
	acks := map[string]time.Time{
		"s1": now.Add(-time.Hour),                   // 直近: 折りたたみに残る
		"e1": now.Add(-ClosedWindow - 24*time.Hour), // 30 日を超えた: どこにも出さない
	}
	out := render(t, Input{Night: &NightInput{Report: rep, Acks: acks}})
	if !strings.Contains(out, "停止 0") || !strings.Contains(out, "異常終了 0") || !strings.Contains(out, "時間切れ 1") {
		t.Error("対応済みの件が件数から外れていない")
	}
	if strings.Contains(out, "night-done s1") || strings.Contains(out, "night-done e1") {
		t.Error("対応済みの件に night-done を出している")
	}
	if !strings.Contains(out, "対応済み 1 件（直近 30 日") || !strings.Contains(out, "o/s#1") {
		t.Error("直近の対応済みが折りたたみに出ていない")
	}
	if strings.Contains(out, "o/e#2") {
		t.Error("30 日を超えた対応済みが残っている")
	}
	if !strings.Contains(out, "夜間実行の要対応 1 件") {
		t.Error("対応済みを要対応の件数から外していない")
	}
}

func TestNightStale(t *testing.T) {
	job := []night.Job{{ID: "d1", Repo: "o/n", Issue: 1, Category: "done"}}
	cases := []struct{ name, gen, want string }{
		// Now は 2026-11-10 09:12 UTC。JST の朝の日付と UTC の日付が同じ日
		{"今日", "2026-11-10T09:00:00+09:00", ""},
		{"前日", "2026-11-09T09:00:00+09:00", "今朝のレポートがありません（前回: 11月9日）"},
		{"読めない", "yesterday", "今朝のレポートがありません（前回: 不明）"},
		// RF-6: JST では翌日だが、Now のタイムゾーン（UTC）へ直すと今日。
		// タイムゾーンを直さずに日付を比べると、ここが「前回」になってしまう。
		{"時差", "2026-11-11T00:30:00+09:00", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := render(t, Input{Night: &NightInput{Report: &night.Report{GeneratedAt: c.gen, Jobs: job}}})
			if c.want == "" {
				if strings.Contains(out, "今朝のレポートがありません") {
					t.Error("今日のレポートなのに古いと出した")
				}
				return
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("%q が出力に無い", c.want)
			}
		})
	}
}

func TestNightBand(t *testing.T) {
	started := "2026-11-10T08:00:00+00:00"
	queuedOld := "2026-11-10T07:00:00+00:00"
	queuedNew := "2026-11-10T08:30:00+00:00"
	report := &night.Report{GeneratedAt: "2026-11-10T09:00:00+00:00",
		Jobs: []night.Job{{ID: "d1", Repo: "o/n", Issue: 1, Category: "done"}}}

	t.Run("実行中あり", func(t *testing.T) {
		st := &night.Status{ExecutorAlive: true,
			Running: []night.StatusJob{{ID: "r1", Repo: "o/n", Issue: 57, StartedAt: &started, Room: strptr("night-n-57")}},
			Queued: []night.StatusJob{
				{ID: "q2", Repo: "o/n", Issue: 59, QueuedAt: &queuedNew},
				{ID: "q1", Repo: "o/n", Issue: 58, QueuedAt: &queuedOld},
			}}
		out := render(t, Input{Night: &NightInput{Report: report, Status: st}})
		for _, want := range []string{"実行中 1 件 ・ 待機 2 件", "開始 2026-11-10 08:00", "経過 1時間12分", "night-n-57", "積んだ 2026-11-10 07:00", "band-live"} {
			if !strings.Contains(out, want) {
				t.Errorf("%q が出力に無い", want)
			}
		}
		if strings.Index(out, "o/n#58") > strings.Index(out, "o/n#59") {
			t.Error("待機が古い順に並んでいない")
		}
	})

	t.Run("実行なし", func(t *testing.T) {
		st := &night.Status{ExecutorAlive: false,
			Queued: []night.StatusJob{{ID: "q1", Repo: "o/n", Issue: 58, QueuedAt: &queuedOld},
				{ID: "q2", Repo: "o/n", Issue: 59, QueuedAt: &queuedNew}}}
		out := render(t, Input{Night: &NightInput{Report: report, Status: st}})
		if !strings.Contains(out, "いまは動いていません。待機中の 2 件は、次の実行で処理します") {
			t.Error("実行なしの文言が無い")
		}
	})

	t.Run("実行本体が止まっている", func(t *testing.T) {
		st := &night.Status{ExecutorAlive: false,
			StaleRunning: []night.StatusJob{{ID: "r1", Repo: "o/n", Issue: 57, StartedAt: &started}}}
		out := render(t, Input{Night: &NightInput{Report: report, Status: st}})
		if !strings.Contains(out, "実行本体が動いていません。running のまま残っている件は『異常終了』に数えます") {
			t.Error("止まっているときの文言が無い")
		}
		if !strings.Contains(out, "band-stale") || !strings.Contains(out, "o/n#57") {
			t.Error("赤の帯に件が出ていない")
		}
	})

	t.Run("状態を読めない", func(t *testing.T) {
		out := render(t, Input{Night: &NightInput{Report: report, StatusErr: "night-run が見つかりません"}})
		if !strings.Contains(out, "状態を読めません") || !strings.Contains(out, "night-run が見つかりません") {
			t.Error("読めない理由が出ていない")
		}
		if !strings.Contains(out, "完了 1") {
			t.Error("状態が読めなくても前の夜の結果を出していない")
		}
	})
}

func TestNightEmptyAndUnknownCategory(t *testing.T) {
	out := render(t, Input{Night: &NightInput{Report: &night.Report{GeneratedAt: "2026-11-10T09:00:00+00:00"}}})
	if !strings.Contains(out, "前の夜に動いた件はありません") {
		t.Error("0 件の文言が無い")
	}
	for _, want := range []string{"完了 0", "停止 0", "異常終了 0", "時間切れ 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出力に無い", want)
		}
	}

	rep := &night.Report{GeneratedAt: "2026-11-10T09:00:00+00:00",
		Jobs: []night.Job{
			{ID: "x1", Repo: "o/x", Issue: 5, Category: "weird"},
			{ID: "d1", Repo: "o/n", Issue: 1, Category: "done"},
		}}
	out = render(t, Input{Night: &NightInput{Report: rep}})
	if !strings.Contains(out, "o/x#5") || !strings.Contains(out, "その他 1 件") {
		t.Error("4 区分に無い category を「その他」に出していない")
	}
	if strings.Index(out, "その他 1 件") < strings.Index(out, "完了 1 件") {
		t.Error("「その他」が一覧の最後に無い")
	}
}

func TestNightReportErr(t *testing.T) {
	out := render(t, Input{
		Entries: goldenEntries(),
		Night:   &NightInput{ReportErr: "/x/report/latest.json がありません"},
	})
	if !strings.Contains(out, "結果を読めません: /x/report/latest.json がありません") {
		t.Error("結果を読めない理由が出ていない")
	}
	if !strings.Contains(out, "未対応 1 件") || !strings.Contains(out, "棚卸し") {
		t.Error("結果が読めないときに今日の要対応を描いていない")
	}
	if !strings.Contains(out, "夜間実行") {
		t.Error("夜間実行のタブが無い")
	}
}

func TestTodayTabNightCount(t *testing.T) {
	rep := &night.Report{GeneratedAt: "2026-11-10T09:00:00+00:00",
		Jobs: []night.Job{
			{ID: "s1", Repo: "o/n", Issue: 1, Category: "stopped"},
			{ID: "s2", Repo: "o/n", Issue: 2, Category: "stopped"},
			{ID: "e1", Repo: "o/n", Issue: 3, Category: "error"},
			{ID: "d1", Repo: "o/n", Issue: 4, Category: "done"},
		}}
	acks := map[string]time.Time{"e1": now.Add(-time.Hour)}
	out := render(t, Input{Entries: goldenEntries(), Night: &NightInput{Report: rep, Acks: acks}})
	if !strings.Contains(out, "夜間実行の要対応 2 件") {
		t.Error("今日の要対応に夜間実行の要対応の件数が出ていない")
	}
}
