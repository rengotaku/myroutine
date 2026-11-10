package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFile(t *testing.T) {
	es, skipped, err := Load(filepath.Join(t.TempDir(), FileName))
	if err != nil || skipped != 0 || len(es) != 0 {
		t.Fatalf("es=%v skipped=%d err=%v", es, skipped, err)
	}
}

func TestLoadFold(t *testing.T) {
	p := write(t,
		`{"ev":"seen","at":"2026-11-01T09:00:00+09:00","id":"a.x","slug":"a","step_name":"A","text":"古い"}`,
		`{"ev":"seen","at":"2026-11-02T09:00:00+09:00","id":"a.x","slug":"a","step_name":"A","text":"新しい"}`,
		`{"ev":"seen","at":"2026-11-02T09:00:00+09:00","id":"b.y","slug":"b","step_name":"B","text":"b"}`,
		`{"ev":"done","at":"2026-11-03T09:00:00+09:00","id":"a.x"}`,
		`{"ev":"cleared","at":"2026-11-03T10:00:00+09:00","id":"zzz"}`,
	)
	es, skipped, err := Load(p)
	if err != nil || skipped != 0 {
		t.Fatalf("skipped=%d err=%v", skipped, err)
	}
	if len(es) != 2 {
		t.Fatalf("entries = %+v", es)
	}
	a := es[0]
	if a.ID != "a.x" || a.Status != StatusDone || a.Seen != 2 || a.Text != "新しい" ||
		a.FirstAt.Day() != 1 || a.LastAt.Day() != 2 || a.ClosedAt.Day() != 3 {
		t.Errorf("a = %+v", a)
	}
	if es[1].Status != StatusOpen || !es[1].ClosedAt.IsZero() {
		t.Errorf("b = %+v", es[1])
	}
	if got := Open(es); len(got) != 1 || got[0].ID != "b.y" {
		t.Errorf("Open = %+v", got)
	}
}

func TestLoadReopen(t *testing.T) {
	p := write(t,
		`{"ev":"seen","at":"2026-11-01T09:00:00Z","id":"a.x","slug":"a","step_name":"A","text":"1"}`,
		`{"ev":"cleared","at":"2026-11-02T09:00:00Z","id":"a.x"}`,
		`{"ev":"seen","at":"2026-11-05T09:00:00Z","id":"a.x","slug":"a","step_name":"A","text":"2"}`,
	)
	es, _, err := Load(p)
	if err != nil || len(es) != 2 {
		t.Fatalf("es=%+v err=%v", es, err)
	}
	if es[0].Status != StatusCleared || es[0].Seen != 1 {
		t.Errorf("first = %+v", es[0])
	}
	if es[1].Status != StatusOpen || es[1].Seen != 1 || es[1].FirstAt.Day() != 5 || es[1].Text != "2" {
		t.Errorf("second = %+v", es[1])
	}
}

func TestLoadSkipsBrokenLines(t *testing.T) {
	p := write(t,
		`{"ev":"seen","at":"2026-11-01T09:00:00Z","id":"a.x","slug":"a","step_name":"A","text":"1"}`,
		`not json`,
		`{"ev":"seen","at":"yesterday","id":"a.y"}`,
		`{"ev":"mystery","at":"2026-11-01T09:00:00Z","id":"a.z"}`,
		`{"ev":"seen","at":"2026-11-01T09:00:00Z"}`,
	)
	es, skipped, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 4 || len(es) != 1 {
		t.Fatalf("skipped=%d es=%+v", skipped, es)
	}
}

func TestAppendAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), FileName)
	for _, id := range []string{"a.x", "b.y"} {
		if err := Append(p, []Event{{Ev: EvSeen, At: "2026-11-01T09:00:00Z", ID: id, Slug: "s", StepName: "S", Text: "t"}}); err != nil {
			t.Fatal(err)
		}
	}
	es, _, err := Load(p)
	if err != nil || len(es) != 2 {
		t.Fatalf("es=%+v err=%v", es, err)
	}
}
