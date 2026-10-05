package runner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	slugRe      = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	legacyRe    = regexp.MustCompile(`^[0-9]+-(.+)$`)
	reservedSet = map[string]bool{"ls": true, "last": true}
)

// readOrder は $MYROUTINE_STEPS_DIR/order を読み、slug の並びを返す。1 行 1 slug で、
// # 以降はコメント、空行は無視する。並びとファイルの食い違いは黙って読み飛ばさず、
// 見つけた分をすべて errs に書いてから失敗を返す（zsh 版 _mr_read_order と同じ）。
func readOrder(stepsDir string, errs *diag) ([]string, bool) {
	orderFile := filepath.Join(stepsDir, "order")
	st, err := os.Stat(orderFile)
	if err != nil || st.IsDir() {
		errs.line("ERROR: 実行順の宣言ファイルがありません: " + orderFile)
		errs.line("       1 行 1 slug で実行順を書いてください（slug = ステップのファイル名から .sh を除いたもの）")
		return nil, false
	}
	f, err := os.Open(orderFile)
	if err != nil {
		errs.line("ERROR: 実行順の宣言ファイルを読めません: " + orderFile)
		return nil, false
	}
	defer f.Close()

	bad := false
	var declared []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineno := 0
	for sc.Scan() {
		lineno++
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		slug := strings.TrimSpace(line)
		if slug == "" {
			continue
		}
		// slug は action id に "<slug>.<id>" の形で前置される。slug 自身がドットを
		// 含むと id からの復元（最初のドットまで）が壊れ、連続日数・NEW・解消の
		// 判定対象から漏れる。履歴の区切り文字（カンマ）も入れられない。
		switch {
		case !slugRe.MatchString(slug):
			errs.line(fmt.Sprintf("ERROR: ステップ slug に使えるのは英数字と _ - だけです: %s (order:%d)", slug, lineno))
			bad = true
		case isDigits(slug):
			errs.line(fmt.Sprintf("ERROR: slug を数字だけにはできません: %s (order:%d)", slug, lineno))
			errs.line("       指定実行で番号と区別できないため、英字を含めてください")
			bad = true
		case reservedSet[slug]:
			errs.line(fmt.Sprintf("ERROR: slug がサブコマンドと衝突しています: %s (order:%d)", slug, lineno))
			errs.line("       ls / last は使えません。別の slug に変えてください")
			bad = true
		case seen[slug]:
			errs.line(fmt.Sprintf("ERROR: order で slug が重複しています: %s (order:%d)", slug, lineno))
			errs.line("       slug は要対応 id の前置に使うため一意である必要があります")
			bad = true
		default:
			seen[slug] = true
			declared = append(declared, slug)
		}
	}
	if err := sc.Err(); err != nil {
		errs.line("ERROR: 実行順の宣言ファイルを読めません: " + orderFile)
		return nil, false
	}

	// order にあるのにファイルが無い
	for _, slug := range declared {
		if st, err := os.Stat(filepath.Join(stepsDir, slug+".sh")); err != nil || !st.Mode().IsRegular() {
			errs.line("ERROR: order に書かれているステップのファイルがありません: " + slug + ".sh")
			bad = true
		}
	}
	// ファイルはあるのに order に無い（zsh の *.sh と同じく隠しファイルは対象外）
	for _, name := range stepFileNames(stepsDir) {
		slug := strings.TrimSuffix(name, ".sh")
		if seen[slug] {
			continue
		}
		errs.line("ERROR: order に書かれていないステップがあります: " + name)
		if m := legacyRe.FindStringSubmatch(slug); m != nil {
			// 旧形式（NN-name.sh）。NN- 付きのまま order に足すと slug が変わり、
			// last-run/<slug> と要対応 id の前置が引き継がれなくなる。
			errs.line(fmt.Sprintf("       旧形式のファイル名です。%s.sh に名前を変え、order に %s を足してください", m[1], m[1]))
			errs.line(fmt.Sprintf("       （%s のまま足すと slug が変わり、実行間隔の記録と要対応の履歴が引き継がれません）", slug))
		} else {
			errs.line(fmt.Sprintf("       実行する順に %s を order へ足してください（実行しないなら .sh を外す）", slug))
		}
		bad = true
	}
	if bad {
		return nil, false
	}

	if len(declared) == 0 {
		errs.line("ERROR: ステップがありません: " + orderFile)
		errs.line("       order に slug を 1 行 1 つ書き、同名の <slug>.sh を置いてください（例: network → network.sh）")
		return nil, false
	}
	return declared, true
}

func stepFileNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".sh") || n == ".sh" {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
