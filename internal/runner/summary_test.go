package runner

import (
	"strings"
	"testing"
)

func TestSummaryProblem(t *testing.T) {
	cases := []struct {
		summary string
		want    string // 空なら問題なし。それ以外は返り値に含まれるべき語
	}{
		{"3 件更新", ""},
		{"対象なし", ""},
		{"154.0.8037.98（最新）", ""},
		{"AWS SSO ログイン済み", ""}, // 大文字だけの略語は許す
		{"", "emit_summary"},
		{"同期完了", "「完了」"},
		{"OK", "「OK」"},
		{"clean", "英単語「clean」"},
		{"killed 0 / stale 0", "英単語「killed」"},
		{strings.Repeat("あ", 21), "一言が長い（42 カラム"},
	}
	for _, c := range cases {
		got := summaryProblem(c.summary)
		if c.want == "" && got != "" {
			t.Errorf("%q: 問題なしのはずが %q", c.summary, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%q: %q を含むべきが %q", c.summary, c.want, got)
		}
	}
}
