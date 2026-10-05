// Package lib は Go の runner に埋め込む zsh の prelude を持つ。
// go:embed は親ディレクトリを参照できないため、prelude.zsh と同じ場所に置く。
package lib

import _ "embed"

// Prelude は lib/prelude.zsh の中身。runner が実行時に一時ディレクトリへ書き出して使う。
//
//go:embed prelude.zsh
var Prelude string
