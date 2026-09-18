// myroutine-render は stdin から JSONL イベントを読み、整形して stdout に
// 出す。収集（zsh 側の bin/myroutine）と描画をこのバイナリに分離することで、
// 全角文字混じりでも桁が揃う出力にする。
package main

import (
	"bufio"
	"flag"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/rengotaku/myroutine/internal/eventlog"
	"github.com/rengotaku/myroutine/internal/format"
	"github.com/rengotaku/myroutine/internal/render"
)

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Args[1:]))
}

func run(in *os.File, out *os.File, args []string) int {
	fs := flag.NewFlagSet("myroutine-render", flag.ContinueOnError)
	width := fs.Int("width", 0, "出力幅（未指定なら COLUMNS → tty 幅 → 100 の順で決定）")
	noColor := fs.Bool("no-color", false, "色付けを無効にする")
	verbose := fs.Bool("verbose", false, "成功ステップでも tail があれば展開する")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	opt := render.Options{
		Width: resolveWidth(*width, out),
		Color: resolveColor(*noColor, out),
	}

	process(in, out, *verbose, opt)
	return 0
}

// process は JSONL イベントを読み終わるまでの本処理。io.Reader/io.Writer だけに
// 依存するため、*os.File を用意せずテストできる。
func process(in io.Reader, out io.Writer, verbose bool, opt render.Options) {
	st := format.NewState(verbose)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	sawSummary := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		ev, err := eventlog.Parse(line)
		if err != nil {
			// 壊れた行は無視して読み進める。
			continue
		}
		switch ev.Ev {
		case "run":
			st.Run(*ev.Run)
		case "step":
			block := st.Step(*ev.Step)
			if len(block.Lines) > 0 {
				render.Render(out, []render.Block{block}, opt)
			}
		case "summary":
			blocks := st.Summary(*ev.Summary)
			render.Render(out, blocks, opt)
			sawSummary = true
		default:
			// 未知の ev は無視する。
		}
	}

	if !sawSummary {
		// summary が来ないまま stdin が閉じた（実行が中断された）場合は、
		// 溜めていた skip だけ出して終わる。異常終了扱いにはしない。
		if skipBlock := st.FlushSkips(); len(skipBlock.Lines) > 0 {
			render.Render(out, []render.Block{skipBlock}, opt)
		}
	}
}

// resolveWidth は --width → COLUMNS 環境変数 → tty 幅 → 既定 100 の順で
// 出力幅を決める。
func resolveWidth(flagWidth int, out *os.File) int {
	if flagWidth > 0 {
		return flagWidth
	}
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if isTTY(out) {
		if w, ok := termWidth(out.Fd()); ok {
			return w
		}
	}
	return 100
}

// resolveColor は stdout が tty かつ NO_COLOR 未設定のときだけ色を有効にする。
func resolveColor(noColorFlag bool, out *os.File) bool {
	if noColorFlag {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTTY(out)
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
