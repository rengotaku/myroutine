package format

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rengotaku/myroutine/internal/eventlog"
	"github.com/rengotaku/myroutine/internal/render"
)

func renderBlock(b render.Block, width int, color bool) string {
	var out bytes.Buffer
	render.Render(&out, []render.Block{b}, render.Options{Width: width, Color: color})
	return out.String()
}

// TestTimeoutRendersAsFailure は指摘1（timeout が成功として描画される）の
// 回帰テスト。timeout ステータスは fail と同様に、失敗アイコン・tail・
// rc・全文ログ導線を出し、かつタイムアウトであることが note から読み取れる
// ことを確認する。
func TestTimeoutRendersAsFailure(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 1, Log: "/tmp/x.txt"})

	block := st.Step(eventlog.StepEvent{
		N:          1,
		Name:       "ハングするステップ",
		Status:     "timeout",
		RC:         1,
		DurationMs: 600000,
		Note:       "600s",
		Tail:       []string{"line 1", "line 2"},
	})

	got := renderBlock(block, 100, false)

	if !strings.Contains(got, iconFail) {
		t.Fatalf("timeout ステップは失敗アイコンで描画されるべき。got:\n%s", got)
	}
	if strings.Contains(got, iconOK) {
		t.Fatalf("timeout ステップに成功アイコンが混ざってはいけない。got:\n%s", got)
	}
	if !strings.Contains(got, "timeout 600s") {
		t.Fatalf("note の制限秒数を活かしてタイムアウトと分かる表示にすべき。got:\n%s", got)
	}
	if !strings.Contains(got, "│ line 1") || !strings.Contains(got, "│ line 2") {
		t.Fatalf("timeout ステップでも tail が展開されるべき。got:\n%s", got)
	}
	if !strings.Contains(got, "全文: /tmp/x.txt") {
		t.Fatalf("timeout ステップでも全文ログへの導線が出るべき。got:\n%s", got)
	}
}

// TestTimeoutWithoutRCDoesNotShowStrayRC は rc=0 のタイムアウト（プロセスが
// シグナルで落ちて rc が意味を持たない場合）で "rc=0" のような無意味な表示を
// 付けないことを確認する。
func TestTimeoutWithoutRCDoesNotShowStrayRC(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 1})

	block := st.Step(eventlog.StepEvent{N: 1, Name: "x", Status: "timeout", RC: 0, Note: "5s"})
	got := renderBlock(block, 100, false)

	if strings.Contains(got, "rc=") {
		t.Fatalf("rc=0 のときは rc= を出すべきではない。got:\n%s", got)
	}
	if !strings.Contains(got, "timeout 5s") {
		t.Fatalf("rc が 0 でも timeout であることは表示すべき。got:\n%s", got)
	}
}

// TestDisabledStepNotRenderedImmediately は指摘2（disabled が即時描画される）
// の回帰テスト。disabled の step イベントは skip 同様、即時描画せず空の
// Block を返す（summary の disabled 一覧だけで「⊘ 無効」を出す）ことを
// 確認する。
func TestDisabledStepNotRenderedImmediately(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 1})

	block := st.Step(eventlog.StepEvent{N: 1, Name: "無効ステップ", Status: "disabled", Note: "reason"})
	if len(block.Lines) != 0 {
		t.Fatalf("disabled の step イベントは即時描画してはいけない。got: %+v", block.Lines)
	}

	// pendingSkip にも溜めない（skip とは別の系統）。summary が
	// disabled を含んでいなくても FlushSkips に混入しないことを確認する。
	skipBlock := st.FlushSkips()
	if len(skipBlock.Lines) != 0 {
		t.Fatalf("disabled は pendingSkip に混ざってはいけない。got: %+v", skipBlock.Lines)
	}
}

// TestDisabledOnlyRenderedOnceViaSummary は disabled ステップが、即時描画
// パスとサマリの「⊘ 無効」ブロックの両方で二重に出ないことを、Summary()
// の出力全体で確認する。
func TestDisabledOnlyRenderedOnceViaSummary(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 1})

	// 即時描画は空のはず。
	immediate := st.Step(eventlog.StepEvent{N: 1, Name: "無効ステップ", Status: "disabled", Note: "reason"})
	if len(immediate.Lines) != 0 {
		t.Fatalf("disabled は即時描画してはいけない")
	}

	blocks := st.Summary(eventlog.SummaryEvent{
		Disabled: []eventlog.DisabledItem{{N: 1, Name: "無効ステップ", Reason: "reason"}},
	})
	var out bytes.Buffer
	render.Render(&out, blocks, render.Options{Width: 100})
	got := out.String()

	count := strings.Count(got, "無効ステップ")
	if count != 1 {
		t.Fatalf("disabled ステップは summary で1回だけ出るべき。got %d 回:\n%s", count, got)
	}
}

// TestStepPosOverridesNForProgress は指摘3（[n/total] の分母と分子の基準が
// 違う）の回帰テスト。pos が入力にあれば進捗表示にはそちらを使い、n は
// フィールドとしては消えない（別の場所で対応付けに使えるように）ことを
// 確認する。
func TestStepPosOverridesNForProgress(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 2})

	block := st.Step(eventlog.StepEvent{N: 9, Pos: 2, Name: "B", Status: "ok"})
	got := renderBlock(block, 100, false)

	if !strings.Contains(got, "[2/2]") {
		t.Fatalf("進捗表示は pos/total を使うべき。got:\n%s", got)
	}
	if strings.Contains(got, "[9/2]") {
		t.Fatalf("進捗表示に n をそのまま使ってはいけない（分母とずれる）。got:\n%s", got)
	}
}

// TestStepPosFallsBackToNWhenAbsent は pos を持たない古い入力（zero value）
// でも、従来どおり n をそのまま進捗番号として使えることを確認する
// （後方互換）。
func TestStepPosFallsBackToNWhenAbsent(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 3})

	block := st.Step(eventlog.StepEvent{N: 3, Name: "C", Status: "ok"}) // Pos は zero value
	got := renderBlock(block, 100, false)

	if !strings.Contains(got, "[3/3]") {
		t.Fatalf("pos が無いときは n にフォールバックすべき。got:\n%s", got)
	}
}

// 名前が名前列より長いと Pad が詰める分を持たず、要約とくっついて語の境目が
// 消える（「…棚卸しreview 0」）。名前を切り詰めて空白を必ず残すこと。
func TestLongNameKeepsGapBeforeSummary(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 1, Log: "/tmp/x.txt"})
	block := st.Step(eventlog.StepEvent{
		N: 1, Pos: 1,
		Name:       "外部出力ガード advisory の棚卸し",
		Status:     "ok",
		DurationMs: 1000,
		Summary:    "review 0 / code-ratio 0",
	})
	if len(block.Lines) == 0 {
		t.Fatalf("行が無い")
	}
	got := block.Lines[0].Text
	if strings.Contains(got, "棚卸しreview") {
		t.Errorf("名前と要約がくっついている: %q", got)
	}
	if !strings.Contains(got, " review 0 / code-ratio 0") {
		t.Errorf("要約の前に空白が無い: %q", got)
	}
}

func TestStartLine(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 24})

	got := renderBlock(st.Start(eventlog.StartEvent{N: 7, Pos: 5, Name: "Brew Update", At: "08:38:41"}), 100, false)

	want := "  ▶ [5/24] Brew Update  開始 08:38\n"
	if got != want {
		t.Fatalf("開始行が違う。got %q want %q", got, want)
	}
}

func TestStartLineFallsBackToNAndOmitsMissingTime(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 3})

	got := renderBlock(st.Start(eventlog.StartEvent{N: 2, Name: "B"}), 100, false)

	if got != "  ▶ [2/3] B\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStepEndedAtShownInRightColumn(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 2})

	got := renderBlock(st.Step(eventlog.StepEvent{N: 1, Pos: 1, Name: "A", Status: "ok", DurationMs: 8100, EndedAt: "08:45:09"}), 100, false)

	if !strings.Contains(got, "8.1s  （終了 08:45）") {
		t.Fatalf("終了時刻が出ていない。got:\n%s", got)
	}
}

func TestStepWithoutEndedAtHasNoEndTime(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 2})

	got := renderBlock(st.Step(eventlog.StepEvent{N: 1, Pos: 1, Name: "A", Status: "ok", DurationMs: 8100}), 100, false)

	if strings.Contains(got, "終了") {
		t.Fatalf("ended_at が無い古いイベントに終了時刻を付けてはいけない。got:\n%s", got)
	}
}
