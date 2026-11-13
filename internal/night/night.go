// Package night は夜間実行（night-run、rengotaku/dotfiles-ubuntu 側）の入力を読む。
//
// 入力は 3 つ。前の夜の結果は $NIGHT_RUN_DIR/report/latest.json（dotfiles が毎朝書く。
// 読むだけで書き換えない）。いまの状態は `night-run status` の標準出力（JSON）。
// 対応済みの記録は $MYROUTINE_LOG_DIR/night-ack.jsonl（この repo が書く、追記専用）。
package night

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// EnvDir は夜間実行のディレクトリを指す環境変数。
	EnvDir = "NIGHT_RUN_DIR"
	// DefaultBin は PATH から探す実行ファイル名。
	DefaultBin = "night-run"
	// AckFileName は対応済みの記録（1 行 1 件の JSONL）。
	AckFileName = "night-ack.jsonl"
)

// Job は前の夜に動いた 1 件。null のことがある項目はポインタで受ける。
type Job struct {
	ID         string  `json:"id"`
	Repo       string  `json:"repo"`
	Issue      int     `json:"issue"`
	State      string  `json:"state"`
	Category   string  `json:"category"`
	Reason     *string `json:"reason"`
	Room       *string `json:"room"`
	ResumeCmd  *string `json:"resume_cmd"`
	Progress   *string `json:"progress"`
	MergedSHA  *string `json:"merged_sha"`
	PR         *int    `json:"pr"`
	StartedAt  *string `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
}

// Report は report/latest.json の中身。
type Report struct {
	GeneratedAt string         `json:"generated_at"`
	Since       string         `json:"since"`
	Counts      map[string]int `json:"counts"`
	Jobs        []Job          `json:"jobs"`
}

// StatusJob は「いまの状態」の 1 件（実行中・待機・running のまま残った件）。
type StatusJob struct {
	ID        string  `json:"id"`
	Repo      string  `json:"repo"`
	Issue     int     `json:"issue"`
	StartedAt *string `json:"started_at"`
	QueuedAt  *string `json:"queued_at"`
	Room      *string `json:"room"`
}

// Status は `night-run status` の出力。
type Status struct {
	GeneratedAt    string      `json:"generated_at"`
	ExecutorAlive  bool        `json:"executor_alive"`
	Running        []StatusJob `json:"running"`
	Queued         []StatusJob `json:"queued"`
	StaleRunning   []StatusJob `json:"stale_running"`
	NeedsAttention int         `json:"needs_attention"`
}

type ack struct {
	At string `json:"at"`
	ID string `json:"id"`
}

// Dir は NIGHT_RUN_DIR、無ければ $HOME/.local/state/night-run を返す。
// HOME も無いときは空文字（呼ぶ側は「ディレクトリが無い」として扱う）。
func Dir() string {
	if v := os.Getenv(EnvDir); v != "" {
		return v
	}
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "night-run")
}

// LoadReport は dir/report/latest.json を読む。無い・空・壊れているときは error を返し、
// 呼ぶ側が「結果を読めません」と出せるようにする。
func LoadReport(dir string) (Report, error) {
	var rep Report
	if dir == "" {
		return rep, errors.New("夜間実行のディレクトリが分かりません（NIGHT_RUN_DIR と HOME が未設定）")
	}
	path := filepath.Join(dir, "report", "latest.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rep, fmt.Errorf("%s がありません", path)
		}
		return rep, fmt.Errorf("%s を読めません: %v", path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return rep, fmt.Errorf("%s が空です", path)
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return rep, fmt.Errorf("JSON が壊れています: %v", err)
	}
	return rep, nil
}

// RunStatus は `night-run status` を timeout で打ち切って実行し、JSON を読む。
// bin が空なら night-run を PATH から探す。失敗の理由は error の文言に入れる。
func RunStatus(bin string, timeout time.Duration) (Status, error) {
	var st Status
	name := bin
	if name == "" {
		name = DefaultBin
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, "status")
	// 子（sh → sleep 等）がパイプを掴んだままだと、kill しても読み取りが終わらず
	// 時間切れで戻らない。待ち時間の上限を置いて、切れたらパイプを閉じる。
	cmd.WaitDelay = 100 * time.Millisecond
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	// 時間切れは終了コードより先に見る（TERM を捕まえて 0 で終わっても時間切れ）
	if ctx.Err() == context.DeadlineExceeded {
		return st, fmt.Errorf("%s status が %s 以内に終わりません", name, timeout)
	}
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return st, fmt.Errorf("%s が見つかりません", name)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := strings.TrimSpace(errb.String())
			if msg == "" {
				msg = "標準エラーは空でした"
			}
			return st, fmt.Errorf("%s status が終了コード %d で終わりました: %s", name, ee.ExitCode(), msg)
		}
		return st, fmt.Errorf("%s status を実行できません: %v", name, err)
	}
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		return st, fmt.Errorf("%s status の出力を読めません: %v", name, err)
	}
	return st, nil
}

// LoadAcks は night-ack.jsonl を読む。壊れた行（JSON でない・at が時刻でない・id が空）は
// 飛ばして skipped に数える。
func LoadAcks(path string) (acked map[string]time.Time, skipped int) {
	acked = map[string]time.Time{}
	f, err := os.Open(path)
	if err != nil {
		return acked, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var a ack
		if json.Unmarshal(line, &a) != nil || a.ID == "" {
			skipped++
			continue
		}
		at, err := time.Parse(time.RFC3339, a.At)
		if err != nil {
			skipped++
			continue
		}
		acked[a.ID] = at
	}
	return acked, skipped
}

// AppendAck は対応済みを 1 回の書き込みで追記する（上書きしない）。
// 前回が改行なしで途切れていたら、先に改行を足して新しい行を壊さない（ledger.Append と同じ）。
func AppendAck(path string, ids []string, at time.Time) error {
	seen := map[string]bool{}
	var buf []byte
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		b, err := json.Marshal(ack{At: at.Format(time.RFC3339), ID: id})
		if err != nil {
			return err
		}
		buf = append(append(buf, b...), '\n')
	}
	if len(buf) == 0 {
		return nil
	}
	if last, ok := lastByte(path); ok && last != 10 {
		buf = append([]byte{10}, buf...)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// AckPath は状態ディレクトリ内の night-ack.jsonl のパス。
func AckPath(logDir string) string { return filepath.Join(logDir, AckFileName) }

// lastByte はファイル末尾の 1 バイト。ファイルが無い・空・読めないときは ok=false。
func lastByte(path string) (b byte, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return 0, false
	}
	var one [1]byte
	if _, err := f.ReadAt(one[:], st.Size()-1); err != nil {
		return 0, false
	}
	return one[0], true
}
