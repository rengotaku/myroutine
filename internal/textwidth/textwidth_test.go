package textwidth

import (
	"strings"
	"testing"
)

func TestPadFullWidth(t *testing.T) {
	got := Pad("ネット疎通確認", 20)
	if w := Width(got); w != 20 {
		t.Fatalf("Width(Pad(...)) = %d, want 20 (result=%q)", w, got)
	}
}

func TestPadASCII(t *testing.T) {
	got := Pad("abc", 10)
	if w := Width(got); w != 10 {
		t.Fatalf("Width(Pad(...)) = %d, want 10 (result=%q)", w, got)
	}
	if got != "abc       " {
		t.Fatalf("got %q", got)
	}
}

func TestPadNoop(t *testing.T) {
	// 既に幅を超えている場合は何もしない。
	s := "とても長い日本語の文字列です"
	got := Pad(s, 4)
	if got != s {
		t.Fatalf("Pad should be no-op when already wider, got %q", got)
	}
}

func TestPadLeft(t *testing.T) {
	got := PadLeft("42", 6)
	if got != "    42" {
		t.Fatalf("got %q", got)
	}
}

func TestWidthEmoji(t *testing.T) {
	// ✓ ⊘ ✗ は記号として使う。表示幅が 0 や負にならないことだけ確認する。
	for _, s := range []string{"✓", "⊘", "✗", "▸"} {
		if w := Width(s); w <= 0 {
			t.Fatalf("Width(%q) = %d, want > 0", s, w)
		}
	}
}

func TestWidthMixed(t *testing.T) {
	// 全角+半角混在の表示幅は文字数と一致しない（全角は 2 幅相当を期待）。
	s := "AB全角"
	if w := Width(s); w == len([]rune(s)) {
		t.Fatalf("Width(%q) = %d, expected to differ from rune count", s, w)
	}
}

func TestTruncateShort(t *testing.T) {
	got := Truncate("short", 10)
	if got != "short" {
		t.Fatalf("got %q, want unchanged", got)
	}
}

func TestTruncateLong(t *testing.T) {
	got := Truncate("abcdefghij", 5)
	if w := Width(got); w > 5 {
		t.Fatalf("Width(%q) = %d, want <= 5", got, w)
	}
	if got[len(got)-len("…"):] != "…" {
		t.Fatalf("got %q, want ellipsis suffix", got)
	}
}

func TestTruncateFullWidth(t *testing.T) {
	got := Truncate("ドットファイル同期処理の実行結果", 10)
	if w := Width(got); w > 10 {
		t.Fatalf("Width(%q) = %d, want <= 10", got, w)
	}
}

func TestTruncatePreservesANSI(t *testing.T) {
	s := "\x1b[31mfailed step name here\x1b[0m"
	got := Truncate(s, 8)
	if w := Width(got); w > 8 {
		t.Fatalf("Width(%q) = %d, want <= 8", got, w)
	}
	// エスケープシーケンス自体は壊れず含まれている（Contains で確認）。
	if !contains(got, "\x1b[31m") {
		t.Fatalf("got %q, want to contain the leading ANSI escape untouched", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// 切り詰めた位置より後ろに閉じの reset があると一緒に捨ててしまい、以降の行まで
// 着色が残る。失敗ステップの tail 抜粋は色付きで来るので実際に起きる。
func TestTruncateClosesOpenStyle(t *testing.T) {
	const reset = "\x1b[0m"
	s := "\x1b[31m" + strings.Repeat("あ", 20) + reset
	got := Truncate(s, 10)

	if Width(got) > 10 {
		t.Errorf("表示幅が超えた: %d (%q)", Width(got), got)
	}
	if !strings.Contains(got, reset) {
		t.Errorf("着色が閉じられていない: %q", got)
	}
}
