package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const usageLine = "Usage: myroutine [-n|--dry-run] [-y|--yes] [--json|--jsonl] [--timeout N] [--no-lock] [--force-cadence] [ls | last | <slug: brew,omz>]"

// Options はコマンドライン引数の解釈結果。
type Options struct {
	DryRun       bool
	Yes          bool
	JSON         bool
	JSONL        bool
	NoLock       bool
	ForceCadence bool
	Help         bool
	// Timeout は --timeout の値。nil なら MYROUTINE_TIMEOUT を使う。
	Timeout *int
	Args    []string
}

// Config は環境変数から決まる設定。
type Config struct {
	StepsDir    string
	LogDir      string
	Timeout     int
	ParallelMax int
	// NowEpoch は MYROUTINE_NOW_EPOCH（間隔判定用の擬似時計）。nil なら現在時刻。
	NowEpoch *int64
	NoColor  bool
	// SummaryCheck は成功ステップの一言の検査（ADR 0007）。MYROUTINE_SUMMARY_CHECK=0 で切る
	// （一言を出さない仮のステップを大量に使う統合テスト向け）。
	SummaryCheck bool
}

// usageError は使い方の誤り。メッセージを出して終了コード 1 で終わる。
type usageError struct {
	msg       string
	showUsage bool
}

func (e *usageError) Error() string { return e.msg }

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// parseArgs は zsh 版と同じ規則でオプションを読む。オプションと slug は混在してよい。
func parseArgs(args []string) (Options, error) {
	var o Options
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-n", "--dry-run":
			o.DryRun = true
		case "-y", "--yes":
			o.Yes = true
		case "--json":
			o.JSON = true
		case "--jsonl":
			o.JSONL = true
		case "--no-lock":
			o.NoLock = true
		case "--force-cadence":
			o.ForceCadence = true
		case "--timeout":
			if i+1 >= len(args) || !isDigits(args[i+1]) {
				return o, &usageError{msg: "ERROR: --timeout には秒数を指定してください"}
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil {
				return o, &usageError{msg: "ERROR: --timeout には秒数を指定してください"}
			}
			o.Timeout = &v
			i++
		case "-h", "--help":
			o.Help = true
			return o, nil
		case "--":
			o.Args = append(o.Args, args[i+1:]...)
			return o, nil
		default:
			if strings.HasPrefix(a, "-") {
				return o, &usageError{msg: "ERROR: 不明なオプション: " + a, showUsage: true}
			}
			o.Args = append(o.Args, a)
		}
	}
	// --json と --jsonl は出力形式が根本的に違う（1 個 vs 逐次）ため併用できない
	if o.JSON && o.JSONL {
		return o, &usageError{msg: "ERROR: --json と --jsonl は併用できません"}
	}
	return o, nil
}

// envOr は zsh の ${VAR:=default} と同じく、未設定と空を同じに扱う。
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func loadConfig(o Options) (Config, error) {
	home := os.Getenv("HOME")
	c := Config{
		StepsDir: envOr("MYROUTINE_STEPS_DIR", filepath.Join(envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "myroutine", "steps.d")),
		LogDir:   envOr("MYROUTINE_LOG_DIR", filepath.Join(envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "myroutine")),
		NoColor:  os.Getenv("NO_COLOR") != "",

		SummaryCheck: os.Getenv("MYROUTINE_SUMMARY_CHECK") != "0",
	}

	t, err := strconv.Atoi(envOr("MYROUTINE_TIMEOUT", "600"))
	if err != nil || t < 0 {
		return c, fmt.Errorf("ERROR: MYROUTINE_TIMEOUT には 0 以上の秒数を指定してください: %s", os.Getenv("MYROUTINE_TIMEOUT"))
	}
	c.Timeout = t
	if o.Timeout != nil {
		c.Timeout = *o.Timeout
	}

	// zsh 版は算術評価で 1 未満（数でない値を含む）を 1 に倒していた
	pm, err := strconv.Atoi(envOr("MYROUTINE_PARALLEL_MAX", "4"))
	if err != nil || pm < 1 {
		pm = 1
	}
	c.ParallelMax = pm

	if v := os.Getenv("MYROUTINE_NOW_EPOCH"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, fmt.Errorf("ERROR: MYROUTINE_NOW_EPOCH には epoch 秒を指定してください: %s", v)
		}
		c.NowEpoch = &n
	}
	return c, nil
}
