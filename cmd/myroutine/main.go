// myroutine は Go 版の routine runner（ADR 0006）。ステップ定義は zsh のままで、
// 埋め込みの prelude（lib/prelude.zsh）越しに実行する。中身は internal/runner。
package main

import (
	"os"

	"github.com/rengotaku/myroutine/internal/runner"
)

func main() {
	os.Exit(runner.Main(os.Args[1:]))
}
