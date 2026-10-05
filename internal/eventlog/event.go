// Package eventlog は JSONL イベントの型とパース・書き出しを扱う。
// 書き手（Go の runner）と読み手（レンダラ）で同じ型を使う。壊れた行・未知の
// ev は呼び出し側が無視できるようエラーを返すだけで panic しない。
package eventlog

import (
	"bytes"
	"encoding/json"
)

// RunEvent は実行開始を表す。
type RunEvent struct {
	Mode      string `json:"mode"`
	StartedAt string `json:"started_at"`
	Total     int    `json:"total"`
	Log       string `json:"log"`
}

// StartEvent はステップが実行に入ったことを表す。skip・無効・間隔外など
// 実行に入らないステップでは流れない。
type StartEvent struct {
	N    int    `json:"n"`
	Pos  int    `json:"pos,omitempty"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	At   string `json:"at"` // HH:MM:SS
}

// StepEvent は1ステップの結果を表す。
type StepEvent struct {
	N          int      `json:"n"`
	Name       string   `json:"name"`
	Slug       string   `json:"slug"`
	Status     string   `json:"status"` // ok / skip / fail / timeout / disabled
	RC         int      `json:"rc"`
	DurationMs int64    `json:"duration_ms"`
	Summary    string   `json:"summary"`
	Note       string   `json:"note"`
	Tail       []string `json:"tail"`
	// Pos は run イベントの total と同じ集合（今回の実行で無効化されていない
	// ステップ）で数えた通し番号。N（ステップ固有番号）とは分母が異なるため
	// 別フィールド。古い（pos を持たない）入力では 0 のまま届く。
	// disabled は total に含まれないので pos を持たない（書き出さない）。
	Pos int `json:"pos,omitempty"`
	// EndedAt はステップの終了時刻（HH:MM:SS）。古い入力では空。
	EndedAt string `json:"ended_at"`
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
	Start   *StartEvent
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
	case "start":
		var s StartEvent
		if err := json.Unmarshal(line, &s); err != nil {
			return Event{}, err
		}
		ev.Start = &s
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

// JSONStep は --json の steps 配列の 1 要素。
type JSONStep struct {
	N          int    `json:"n"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	Status     string `json:"status"`
	RC         int    `json:"rc"`
	DurationMs int64  `json:"duration_ms"`
	Note       string `json:"note"`
}

// JSONResult は --json が実行終了後に 1 個だけ出す全体の結果。
type JSONResult struct {
	Steps    []JSONStep     `json:"steps"`
	Actions  []ActionItem   `json:"actions"`
	Todos    []TodoItem     `json:"todos"`
	Infos    []InfoItem     `json:"infos"`
	Resolved []string       `json:"resolved"`
	Disabled []DisabledItem `json:"disabled"`
}

// Encode は v を 1 行の JSON にする（末尾の改行は含まない）。HTML 用の
// エスケープ（< を \u003c にする等）はしない。
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// EncodeEvent は ev を先頭に持つ 1 行の JSONL イベントにする。payload は
// RunEvent / StartEvent / StepEvent / SummaryEvent のいずれかのポインタ。
func EncodeEvent(ev string, payload any) ([]byte, error) {
	body, err := Encode(payload)
	if err != nil {
		return nil, err
	}
	head, err := Encode(ev)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(body)+len(head)+8)
	out = append(out, `{"ev":`...)
	out = append(out, head...)
	if len(body) > 2 { // "{}" でなければ区切りを入れて中身を続ける
		out = append(out, ',')
	}
	out = append(out, body[1:]...)
	return out, nil
}
