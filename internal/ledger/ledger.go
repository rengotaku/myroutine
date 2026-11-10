// Package ledger は要対応の履歴（actions.jsonl）の読み書きを持つ。
//
// 1 行 1 イベントの追記専用ファイルで、実行ごとの「出た（seen）」「出なくなった
// （cleared）」と、人が打った「片付けた（done）」を残す。読むときに id ごとに畳み、
// 開いている要対応と閉じた要対応を分ける。runner（書く）と view（読む）が共有する。
package ledger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const FileName = "actions.jsonl"

const (
	EvSeen    = "seen"
	EvCleared = "cleared"
	EvDone    = "done"
)

// Status は畳んだあとの要対応の状態。
type Status string

const (
	StatusOpen    Status = "open"
	StatusDone    Status = "done"
	StatusCleared Status = "cleared"
)

// Event は actions.jsonl の 1 行。seen だけが slug / step_name / text を持つ。
type Event struct {
	Ev       string `json:"ev"`
	At       string `json:"at"`
	ID       string `json:"id"`
	Slug     string `json:"slug,omitempty"`
	StepName string `json:"step_name,omitempty"`
	Text     string `json:"text,omitempty"`
}

// Entry は id ごとに畳んだ 1 件。同じ id が閉じたあとにまた出たら別の Entry になる。
type Entry struct {
	ID       string
	Slug     string
	StepName string
	Text     string // 最新の本文
	FirstAt  time.Time
	LastAt   time.Time // 最後に seen した時刻
	Seen     int
	Status   Status
	ClosedAt time.Time // open のときはゼロ値
}

// Path は状態ディレクトリ内の actions.jsonl のパス。
func Path(logDir string) string { return filepath.Join(logDir, FileName) }

// Append はイベントを 1 回の書き込みで追記する（上書きしない）。
func Append(path string, evs []Event) error {
	if len(evs) == 0 {
		return nil
	}
	var buf []byte
	for _, e := range evs {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf = append(append(buf, b...), '\n')
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

// Load はファイルを畳んで、開いた順の Entry と、読み飛ばした行数を返す。
// ファイルが無いのは 0 件として正常。壊れた行（JSON でない・時刻や id が不正・
// 未知の ev）は飛ばして skipped に数える。
func Load(path string) (entries []Entry, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	openIdx := map[string]int{} // id → entries 内の、いま開いている件
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if json.Unmarshal(line, &e) != nil || e.ID == "" {
			skipped++
			continue
		}
		at, perr := time.Parse(time.RFC3339, e.At)
		if perr != nil {
			skipped++
			continue
		}
		switch e.Ev {
		case EvSeen:
			if i, ok := openIdx[e.ID]; ok {
				en := &entries[i]
				en.Seen++
				en.Text, en.Slug, en.StepName = e.Text, e.Slug, e.StepName
				en.LastAt = at
				continue
			}
			entries = append(entries, Entry{
				ID: e.ID, Slug: e.Slug, StepName: e.StepName, Text: e.Text,
				FirstAt: at, LastAt: at, Seen: 1, Status: StatusOpen,
			})
			openIdx[e.ID] = len(entries) - 1
		case EvCleared, EvDone:
			i, ok := openIdx[e.ID]
			if !ok {
				continue // 開いていない id への閉じは何もしない
			}
			entries[i].Status = StatusCleared
			if e.Ev == EvDone {
				entries[i].Status = StatusDone
			}
			entries[i].ClosedAt = at
			delete(openIdx, e.ID)
		default:
			skipped++
		}
	}
	if err := sc.Err(); err != nil {
		return entries, skipped, fmt.Errorf("%s を読めません: %w", path, err)
	}
	return entries, skipped, nil
}

// Open は未対応（open）の Entry だけを返す。
func Open(entries []Entry) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Status == StatusOpen {
			out = append(out, e)
		}
	}
	return out
}
