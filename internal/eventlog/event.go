// Package eventlog は myroutine-render の入力 JSONL イベントの型と
// パースを扱う。壊れた行・未知の ev は呼び出し側が無視できるよう
// エラーを返すだけで panic しない。
package eventlog

import "encoding/json"

// RunEvent は実行開始を表す。
type RunEvent struct {
	Mode      string `json:"mode"`
	StartedAt string `json:"started_at"`
	Total     int    `json:"total"`
	Log       string `json:"log"`
}

// StepEvent は1ステップの結果を表す。
type StepEvent struct {
	N          int      `json:"n"`
	Name       string   `json:"name"`
	Slug       string   `json:"slug"`
	Status     string   `json:"status"` // ok / skip / fail
	RC         int      `json:"rc"`
	DurationMs int64    `json:"duration_ms"`
	Summary    string   `json:"summary"`
	Note       string   `json:"note"`
	Tail       []string `json:"tail"`
}

// ActionItem は要対応（放置すると困る）項目。
type ActionItem struct {
	Step       int    `json:"step"`
	ID         string `json:"id"`
	Text       string `json:"text"`
	StreakDays int    `json:"streak_days"`
	New        bool   `json:"new"`
}

// TodoItem は「きょう回すもの」。
type TodoItem struct {
	Step int    `json:"step"`
	ID   string `json:"id"`
	Text string `json:"text"`
}

// InfoItem は対応不要の情報項目。
type InfoItem struct {
	Step int    `json:"step"`
	ID   string `json:"id"`
	Text string `json:"text"`
}

// DisabledItem は無効化されたステップ。
type DisabledItem struct {
	N      int    `json:"n"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Reason string `json:"reason"`
}

// SummaryEvent は実行全体のまとめ。
type SummaryEvent struct {
	WallMs   int64          `json:"wall_ms"`
	Actions  []ActionItem   `json:"actions"`
	Todos    []TodoItem     `json:"todos"`
	Infos    []InfoItem     `json:"infos"`
	Resolved []string       `json:"resolved"`
	Disabled []DisabledItem `json:"disabled"`
}

// Event は 1 行分のデコード結果。Ev が種別を表し、対応するフィールドだけが
// 埋まる。未知の Ev のときは全フィールドが zero value のまま。
type Event struct {
	Ev      string
	Run     *RunEvent
	Step    *StepEvent
	Summary *SummaryEvent
}

// evPeek は ev フィールドだけを先に見るための型。
type evPeek struct {
	Ev string `json:"ev"`
}

// Parse は1行の JSON をデコードする。壊れた JSON はエラーを返す。
// 未知の ev はエラーにはせず、Event.Ev だけをセットして返す
// （呼び出し側で switch のデフォルトケースとして無視すればよい）。
func Parse(line []byte) (Event, error) {
	var peek evPeek
	if err := json.Unmarshal(line, &peek); err != nil {
		return Event{}, err
	}
	ev := Event{Ev: peek.Ev}
	switch peek.Ev {
	case "run":
		var r RunEvent
		if err := json.Unmarshal(line, &r); err != nil {
			return Event{}, err
		}
		ev.Run = &r
	case "step":
		var s StepEvent
		if err := json.Unmarshal(line, &s); err != nil {
			return Event{}, err
		}
		ev.Step = &s
	case "summary":
		var s SummaryEvent
		if err := json.Unmarshal(line, &s); err != nil {
			return Event{}, err
		}
		ev.Summary = &s
	}
	return ev, nil
}
