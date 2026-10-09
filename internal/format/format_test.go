package format

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/rengotaku/myroutine/internal/eventlog"
	"github.com/rengotaku/myroutine/internal/render"
	"github.com/rengotaku/myroutine/internal/textwidth"
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
	if !strings.Contains(got, "timeout（600 秒）") {
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
	if !strings.Contains(got, "timeout（5 秒）") {
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

	want := "  ▶ [5/24] Brew Update" + strings.Repeat(" ", nameCol-len("Brew Update")) + colSep + "開始 08:38\n"
	if got != want {
		t.Fatalf("開始行が違う。got %q want %q", got, want)
	}
}

// 開始行の「開始」と結果行の一言は、名前の長さ（全角・列幅超えを含む）に
// かかわらず同じカラムから始まる。
func TestStartWordAlignsWithResultWord(t *testing.T) {
	names := []string{"AWS SSO Login", "ネット疎通確認", "Dotfiles 差分チェック＋同期", "とても長いステップ名がここに入って列幅を超えるケース"}
	for _, name := range names {
		st := NewState(false)
		st.Run(eventlog.RunEvent{Total: 23})
		start := renderBlock(st.Start(eventlog.StartEvent{N: 3, Pos: 3, Name: name, At: "08:55:00"}), 200, false)
		result := renderBlock(st.Step(eventlog.StepEvent{N: 3, Pos: 3, Name: name, Status: "ok", Summary: "問題なし", DurationMs: 1600}), 200, false)

		startCol := textwidth.Width(start[:strings.Index(start, "開始")])
		resultCol := textwidth.Width(result[:strings.Index(result, "問題なし")])
		if startCol != resultCol {
			t.Errorf("%q: 開始の位置 %d と一言の位置 %d がずれている\nstart:  %q\nresult: %q", name, startCol, resultCol, start, result)
		}
	}
}

// 名前列を固定幅にしても、狭い端末で開始時刻を切り捨てずに折り返す。
func TestStartLineWrapsOnNarrowTerminal(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 24})

	got := renderBlock(st.Start(eventlog.StartEvent{N: 7, Pos: 5, Name: "Brew Update", At: "08:38:41"}), 40, false)

	if !strings.Contains(got, "開始 08:38") {
		t.Fatalf("幅 40 で開始時刻が消えた: %q", got)
	}
	for _, l := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if w := textwidth.Width(l); w > 40 {
			t.Errorf("幅 40 を超える行がある（%d）: %q", w, l)
		}
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

func TestStepEndTimeNotShown(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 2})

	got := renderBlock(st.Step(eventlog.StepEvent{N: 1, Pos: 1, Name: "A", Status: "ok", Summary: "対象なし", DurationMs: 8100, EndedAt: "08:45:09"}), 100, false)

	// 終了時刻は「開始＋所要時間」で求まり、新しい情報が無いので出さない
	if strings.Contains(got, "終了") {
		t.Fatalf("終了時刻を出してはいけない。got:\n%s", got)
	}
	if !strings.Contains(got, "対象なし  8.1s") {
		t.Fatalf("一言の直後に所要時間が続くべき。got:\n%s", got)
	}
}

// TestResultLineFormatIsUniform は成功・失敗・skip の結果行が
// 「記号 [位置/全件] 名前  一言  所要時間」の同じ書式で出ることを確認する。
func TestResultLineFormatIsUniform(t *testing.T) {
	cases := []struct {
		ev   eventlog.StepEvent
		want string
	}{
		{eventlog.StepEvent{N: 1, Pos: 1, Name: "A", Status: "ok", Summary: "3 件更新", DurationMs: 1200}, "✓ [1/9] A" + strings.Repeat(" ", 31) + "  3 件更新  1.2s"},
		{eventlog.StepEvent{N: 2, Pos: 2, Name: "B", Status: "fail", RC: 2, DurationMs: 300}, "✗ [2/9] B" + strings.Repeat(" ", 31) + "  失敗（終了コード 2）  0.3s"},
		{eventlog.StepEvent{N: 3, Pos: 3, Name: "C", Status: "skip", Note: "コマンド: tmux"}, "⊘ [3/9] C" + strings.Repeat(" ", 31) + "  skip（tmux が無い）"},
		{eventlog.StepEvent{N: 4, Pos: 4, Name: "D", Status: "skip", Note: "コマンド: git, コマンド: zsh"}, "skip（git・zsh が無い）"},
		{eventlog.StepEvent{N: 5, Pos: 5, Name: "E", Status: "skip", Note: "declined (destructive)"}, "skip（確認で N）"},
		{eventlog.StepEvent{N: 6, Pos: 6, Name: "F", Status: "skip", Note: "dry-run (destructive)"}, "skip（dry-run）"},
	}
	for _, c := range cases {
		st := NewState(false)
		st.Run(eventlog.RunEvent{Total: 9})
		got := renderBlock(st.Step(c.ev), 120, false)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q が無い。got:\n%s", c.want, got)
		}
	}
}

func TestSkipReasonShortensHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("HOME が取れない")
	}
	if got := skipReason("パス: " + home + "/.oh-my-zsh"); got != "~/.oh-my-zsh が無い" {
		t.Fatalf("got %q", got)
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

func TestCadenceSkipsGatheredAndOtherSkipsShownInline(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 25})
	st.Step(eventlog.StepEvent{N: 6, Name: "Brew Update", Status: "skip", Note: "cadence: weekly（月曜のみ）", Cadence: "weekly・月曜のみ", NextDue: "10-12"})
	st.Step(eventlog.StepEvent{N: 15, Name: "外部出力ガード advisory の棚卸し", Status: "skip", Note: "cadence: weekly（前回 2026-09-30）", Cadence: "weekly", LastRun: "09-30", NextDue: "10-07"})
	inline := renderBlock(st.Step(eventlog.StepEvent{N: 9, Pos: 9, Name: "Progress", Status: "skip", Note: "コマンド: claude"}), 120, false)

	got := renderBlock(st.FlushSkips(), 120, false)

	for _, want := range []string{
		"今日は対象外 2 件（実行間隔による skip）",
		"[6]   Brew Update                       weekly・月曜のみ  次回 10-12",
		"[15]  外部出力ガード advisory の棚卸し  weekly            次回 10-07（前回 09-30）",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が無い。got:\n%s", want, got)
		}
	}
	// 前提不足の skip は末尾にまとめず、その場で結果行として出す（二重に出さない）
	if strings.Contains(got, "Progress") {
		t.Errorf("前提不足の skip を末尾に出してはいけない。got:\n%s", got)
	}
	if !strings.Contains(inline, "⊘ [9/25] Progress") || !strings.Contains(inline, "skip（claude が無い）") {
		t.Errorf("前提不足の skip がその場で出ていない。got:\n%s", inline)
	}
}

// TestResultLineWrapsInsteadOfTruncating は 80 カラムでも一言と所要時間が
// 切り捨てられず、折り返して残ることを確認する。
func TestResultLineWrapsInsteadOfTruncating(t *testing.T) {
	st := NewState(false)
	st.Run(eventlog.RunEvent{Total: 25})
	summary := strings.Repeat("あ", 19) + "い" // 40 カラム（上限ちょうど）
	got := renderBlock(st.Step(eventlog.StepEvent{N: 1, Pos: 1, Name: "Worktree 同期", Status: "ok", Summary: summary, DurationMs: 1200}), 80, false)
	flat := strings.ReplaceAll(strings.ReplaceAll(got, "\n", ""), " ", "")
	if !strings.Contains(flat, "い1.2s") {
		t.Fatalf("一言の末尾と所要時間が残っていない。got:\n%s", got)
	}
}
