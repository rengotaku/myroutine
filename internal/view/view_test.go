package view

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rengotaku/myroutine/internal/ledger"
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
