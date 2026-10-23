package render

import "testing"

func TestHasCommandLine(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"壊れている\n      → $ make fix", true},
		{"壊れている\n$ make fix", true},
		{"壊れている\n  対象: a, b\n      → $ make fix", true},
		{"壊れている — 確認する", false},
		{"$ make fix", false}, // 1 行目は見出しで、コマンド行として描画されない
		{"壊れている — $ make fix で確認する", false},
		{"壊れている\n      → 直したら $ make fix", false},
	}
	for _, c := range cases {
		if got := HasCommandLine(c.text); got != c.want {
			t.Errorf("%q: got %v, want %v", c.text, got, c.want)
		}
	}
}
