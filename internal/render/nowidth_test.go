package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbiddenPad は fmt 系のフォーマット文字列に含まれる、表示幅を意識しない
// 文字数ベースのパディング指定（例: "%-20s", "%10d"）を検出する。
// internal/textwidth 以外のソースがこれを使うと、全角文字混じりの文字列で
// 桁がずれる（本 Issue の主目的）。
// %d / %02d のような数値のゼロ埋めは表示幅とは無関係なので対象外にする。
// 対象は "%-20s" "%20s" のような、文字列を文字数で詰める指定。
var forbiddenPad = regexp.MustCompile(`%-?\d+[sv]`)

func TestNoManualWidthPadding(t *testing.T) {
	root := "../.." // リポジトリルート
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// textwidth パッケージ自体は幅計算の実装なので対象外。
		if strings.Contains(path, string(filepath.Separator)+"textwidth"+string(filepath.Separator)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if loc := forbiddenPad.FindIndex(data); loc != nil {
			t.Errorf("%s に文字数ベースのパディング指定 (%q) が見つかった。internal/textwidth の Pad/PadLeft/Truncate を使うこと", path, string(data[loc[0]:loc[1]]))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk error: %v", err)
	}
}
