package eventlog

import "testing"

func TestParseStep(t *testing.T) {
	ev, err := Parse([]byte(`{"ev":"step","n":1,"name":"AWS SSO Login","slug":"aws-sso","status":"ok","rc":0,"duration_ms":8100,"summary":"","note":"","tail":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Ev != "step" || ev.Step == nil {
		t.Fatalf("got %+v", ev)
	}
	if ev.Step.N != 1 || ev.Step.Name != "AWS SSO Login" || ev.Step.Status != "ok" {
		t.Fatalf("got %+v", ev.Step)
	}
}

func TestParseUnknownEv(t *testing.T) {
	ev, err := Parse([]byte(`{"ev":"future-event","foo":"bar"}`))
	if err != nil {
		t.Fatalf("unknown ev should not error, got %v", err)
	}
	if ev.Ev != "future-event" || ev.Step != nil || ev.Run != nil || ev.Summary != nil {
		t.Fatalf("got %+v, want empty payload fields", ev)
	}
}

func TestParseBrokenJSON(t *testing.T) {
	_, err := Parse([]byte(`{"ev":"step", not json`))
	if err == nil {
		t.Fatalf("expected error for broken json")
	}
}

func TestParseBrokenJSONInsideKnownEv(t *testing.T) {
	// ev は読めるが、後続フィールドの型が壊れている場合もエラーを返す
	// （呼び出し側で該当行を無視できるように）。
	_, err := Parse([]byte(`{"ev":"step","n":"not-a-number"}`))
	if err == nil {
		t.Fatalf("expected error for malformed step payload")
	}
}

func TestParseSummary(t *testing.T) {
	ev, err := Parse([]byte(`{"ev":"summary","wall_ms":73000,"actions":[{"step":3,"id":"x","text":"a — b","streak_days":2,"new":true}],"todos":[],"infos":[],"resolved":["old-id"],"disabled":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Summary == nil || ev.Summary.WallMs != 73000 || len(ev.Summary.Actions) != 1 {
		t.Fatalf("got %+v", ev.Summary)
	}
	if ev.Summary.Resolved[0] != "old-id" {
		t.Fatalf("got %+v", ev.Summary.Resolved)
	}
}

func TestParseStart(t *testing.T) {
	ev, err := Parse([]byte(`{"ev":"start","n":5,"pos":4,"name":"Brew Update","slug":"brew","at":"08:38:12"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Ev != "start" || ev.Start == nil {
		t.Fatalf("got %+v", ev)
	}
	if ev.Start.N != 5 || ev.Start.Pos != 4 || ev.Start.Name != "Brew Update" || ev.Start.At != "08:38:12" {
		t.Fatalf("got %+v", ev.Start)
	}
}

func TestParseStepEndedAt(t *testing.T) {
	ev, err := Parse([]byte(`{"ev":"step","n":1,"name":"a","slug":"a","status":"ok","rc":0,"duration_ms":1,"ended_at":"08:45:03"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Step == nil || ev.Step.EndedAt != "08:45:03" {
		t.Fatalf("got %+v", ev.Step)
	}
}
