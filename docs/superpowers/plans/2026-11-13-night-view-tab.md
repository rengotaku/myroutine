# 夜間実行タブ 実装計画

> issue: rengotaku/myroutine#67。完了条件は issue のコメント（本文が「## 完了条件」で始まるもの）が正典。仕様は issue 本文と、「追加の仕様: 『いまの状態』の帯」のコメント。

**Goal:** `myroutine view` の HTML に「夜間実行」のタブを足し、前の夜の結果（4 区分）と、いまの状態（待機・実行中）を朝に見られるようにする。人の判断が要る件に、理由・進んだところ・再開コマンドのコピーと、「対応済み」にするコマンドのコピーを付ける。

**Architecture:** 夜間実行の入力（`report/latest.json`、`night-run status` の出力、対応済みの記録）を読む処理を新しいパッケージ `internal/night` に置く。`internal/view` は読み込み済みの値を受け取って描画するだけにする。`view` サブコマンド（`internal/runner/viewcmd.go`）が読み込みを呼び、`myroutine night-done <id>...` が対応済みの記録を書く。画面は今までどおり静的な 1 枚の HTML で、ボタンはコマンドをコピーするだけ。

**Tech Stack:** Go（標準ライブラリだけ）、`html/template`、既存の `internal/ledger` と同じ追記専用 JSONL。

**Spec:** https://github.com/rengotaku/myroutine/issues/67

## Global Constraints

- 画面は外部の CSS / JS / フォントを読まない。本文は `html/template` でエスケープする（既存の `internal/view` と同じ）
- ボタンは、コマンドをクリップボードへコピーするだけ。この画面から何も実行しない。結果ファイル（`report/*.json`）は書き換えない
- 入力の場所: `NIGHT_RUN_DIR`（環境変数。無ければ `$HOME/.local/state/night-run`）。結果は `$NIGHT_RUN_DIR/report/latest.json`、いまの状態は `night-run status` の標準出力（JSON）。`night-run` は PATH から探し、5 秒で打ち切る
- 夜間実行のタブは、`NIGHT_RUN_DIR` のディレクトリが存在するときだけ出す。無いとき（夜間実行をしない機械）は、今までの画面と同じ出力にする
- 対応済みの記録: `$MYROUTINE_LOG_DIR/night-ack.jsonl`（`actions.jsonl` と同じ状態ディレクトリ）。1 行 1 件 `{"at":"<RFC3339>","id":"<job id>"}`。追記専用。壊れた行は読み飛ばす
- 対応済みにした件は、結果ファイルに残っていても「対応が要る件」の一覧と件数に出さず、折りたたみの「対応済み」に、記録から 30 日（`ClosedWindow` と同じ）のあいだ出す
- `myroutine night-done <job id>...`: 結果ファイル（`latest.json`）に無い id が 1 つでもあれば、何も書かずに終了コード 1（標準エラーに `ERROR: 結果に無い id: <id>`）。全部あれば追記して終了コード 0。引数なしは usage で終了コード 1
- 古さ: `generated_at` の日付が、画面を作った時刻の日付（`Now` のタイムゾーン）と違う、または読めないとき、「今朝のレポートがありません（前回: ○月○日）」を出す。読めないときの「前回」は「不明」
- 帯（いまの状態）の 3 つの状態と文言は、issue のコメントの表1 のとおり。`night-run status` が見つからない・失敗・JSON が壊れているときは、帯の位置に「状態を読めません: <理由>」を出し、前の夜の結果はそのまま出す
- 既存のテストの削除・変更・アサーションの緩和をしない。既存の「未対応」「対応済み・解消済み」の表示の出力は変えない
- 外部パッケージを足さない（`go.mod` を変えない）

## Review Focus

- RF-1: `latest.json` が無い・空・壊れた JSON のとき、夜間実行のタブは「結果を読めません」を出し、今日の要対応のタブは今までどおり描画される
- RF-2: `progress` `resume_cmd` `room` `pr` `merged_sha` `reason` が `null` や空のとき、「null」という文字を出さず、`resume_cmd` が無ければコピーのボタンを出さない
- RF-3: `reason` `progress` `resume_cmd` に `<script>` や引用符が入っていても、エスケープされ、コピーのボタンの `data-cmd` が壊れない
- RF-4: `night-run status` が、見つからない・終了コード 1・5 秒を超える・壊れた JSON のどれでも、帯に理由が出て、画面全体は描画される
- RF-5: `night-ack.jsonl` に壊れた行や、結果に無い id があっても落ちない。対応済みの id の件は、一覧と件数から外れ、折りたたみに出る
- RF-6: `generated_at` が `+09:00` で、`Now` が UTC のとき、日付の比較を `Now` のタイムゾーンでなく、`generated_at` を `Now` のタイムゾーンへ直して行う。読めない値のとき落ちない
- RF-7: `jobs` が空（0 件）のとき、4 区分の件数は 0 で、「前の夜に動いた件はありません」を出す。`category` が 4 区分と `running` 以外の値のとき、その件は落とさず「その他」として一覧の最後に出す

## ファイルの構成

| ファイル | 役割 |
|---|---|
| `internal/night/night.go` | 結果・状態・対応済みの読み込みと型（新規） |
| `internal/night/night_test.go` | 上の読み込みのテスト（新規） |
| `internal/view/view.go` / `view.html.tmpl` | タブと夜間実行の描画（変更） |
| `internal/view/view_test.go` | 描画のテスト（追記だけ） |
| `internal/runner/viewcmd.go` | 読み込みを呼ぶ（変更）。`night-done` は `internal/runner/nightdone.go`（新規） |
| `internal/runner/options.go` | usage と `night-done` の振り分け（変更） |
| `docs/ARCHITECTURE.md` | 夜間実行の節を足す（変更） |

## Task 1: 夜間実行の入力を読む（`internal/night`）

**Files:**
- Create: `internal/night/night.go`、`internal/night/night_test.go`

**Interfaces:**
- Produces:
  - `type Job struct { ID, Repo string; Issue int; State, Category string; Reason, Room, ResumeCmd, Progress, MergedSHA *string; PR *int; StartedAt, FinishedAt *string }`
  - `type Report struct { GeneratedAt string; Counts map[string]int; Jobs []Job }`
  - `type Status struct { GeneratedAt string; ExecutorAlive bool; Running, Queued, StaleRunning []StatusJob; NeedsAttention int }`（`StatusJob` は `ID, Repo string; Issue int; StartedAt, QueuedAt, Room *string`）
  - `func Dir() string`（`NIGHT_RUN_DIR` か既定）
  - `func LoadReport(dir string) (Report, error)`（`dir/report/latest.json`）
  - `func RunStatus(bin string, timeout time.Duration) (Status, error)`（`bin` は実行するコマンド名。空なら `night-run`。失敗の理由を `error` の文言に入れる）
  - `func LoadAcks(path string) (acked map[string]time.Time, skipped int)`
  - `func AppendAck(path string, ids []string, at time.Time) error`（`ledger.Append` と同じく、前回が改行なしで途切れていたら先に改行を足す）
  - `func AckPath(logDir string) string`

- [ ] **Step 1: 失敗するテストを書く**。`TestLoadReport`（正常・無い・空・壊れた）、`TestLoadReportNulls`（RF-2 の `null` の項目）、`TestRunStatus`（偽の `night-run` として一時ディレクトリに置いたシェルスクリプトを `bin` に渡し、正常・終了コード 1・5 秒超過のための短い `timeout`・壊れた JSON・コマンドが無い、の 5 通り。RF-4）、`TestLoadAcks`（壊れた行を数えて飛ばす。RF-5）、`TestAppendAckRepairsTruncated`
- [ ] **Step 2: 実行して失敗を確認**: `go test ./internal/night/` が、パッケージ未定義で落ちる
- [ ] **Step 3: 上の署名で実装**。JSON は `encoding/json`。`null` は `*string` で受ける。`RunStatus` は `exec.CommandContext` と `context.WithTimeout`
- [ ] **Step 4: `go test ./internal/night/` が通る**
- [ ] **Step 5: commit**: `feat: 夜間実行の結果・状態・対応済みを読む (#67)`

## Task 2: 対応済みにするコマンド（`night-done`）

**Files:**
- Create: `internal/runner/nightdone.go`、`internal/runner/nightdone_test.go`
- Modify: `internal/runner/options.go`（usage の 1 行と、`night-done` を `done` と同じ扱いで振り分ける）

**Interfaces:**
- Consumes: Task 1 の `night.LoadReport` `night.AppendAck` `night.AckPath` `night.Dir`
- Produces: `func (r *Runner) runNightDone(args []string) int`

- [ ] **Step 1: 失敗するテストを書く**。`TestNightDone`: 結果に `a1` `a2` がある状態で `night-done a1 a2` が終了コード 0 で 2 行を追記、`night-done a1 zz` が終了コード 1 で何も追記しない（RF-5）、引数なしが終了コード 1。既存の `done` のテストの作り方に合わせる
- [ ] **Step 2: 失敗を確認**
- [ ] **Step 3: `runNightDone` を実装**。先に全 id を検査してから、まとめて 1 回で追記する
- [ ] **Step 4: 通る**: `go test ./internal/runner/`
- [ ] **Step 5: commit**: `feat: myroutine night-done で夜間実行の件を対応済みにする (#67)`

## Task 3: タブと夜間実行の描画（`internal/view`）

**Files:**
- Modify: `internal/view/view.go`、`internal/view/view.html.tmpl`、`internal/runner/viewcmd.go`
- Test: `internal/view/view_test.go`（追記）

**Interfaces:**
- Consumes: Task 1 の型と関数
- Produces: `Input` に `Night *NightInput` を足す。`type NightInput struct { Report *night.Report; ReportErr string; Status *night.Status; StatusErr string; Acks map[string]time.Time }`。`Night == nil` のときは、今までと同じ出力（タブなし）

- [ ] **Step 1: 失敗するテストを書く**。名前と検証: `TestNightTabHiddenWhenNil`（既存の出力とバイトで同じ）、`TestNightFourCategories`（件数の 4 区分と、区分ごとの一覧）、`TestNightCardsAndCopyButtons`（stopped / error / timeout の件に、理由・進んだところ・`resume_cmd` のコピー・`myroutine night-done <id>` のコピー。done の件には出さない。RF-2・RF-3）、`TestNightAckedHidden`（RF-5）、`TestNightStale`（RF-6 を含む。今日の日付・前日・読めない値）、`TestNightBand`（帯の 3 つの状態と「状態を読めません」。RF-4）、`TestNightEmptyAndUnknownCategory`（RF-7）、`TestNightReportErr`（RF-1。今日の要対応が描画される）、`TestTodayTabNightCount`（今日の要対応のタブに、夜間実行の要対応の件数だけが出る。対応済みの件は数えない）
- [ ] **Step 2: 失敗を確認**
- [ ] **Step 3: 実装**。タブは 2 つ（「今日の要対応」「夜間実行」。バッジに件数）。表示の切替は URL のハッシュ（`#night`）で、JS が無くても今日の要対応が見える作りにする。色と配置は、issue のコメントと、画面見本の「4 区分の色分け・件ごとのカード・コピーのボタン・ダークテーマ」に従う。実行中の色は 4 区分と別に持ち、`prefers-reduced-motion` で点滅を止める。`viewcmd.go` は、`night.Dir()` が存在するときだけ Task 1 の関数を呼んで `Night` を作る
- [ ] **Step 4: `go test ./...` が通る**
- [ ] **Step 5: 実際に開く**: `go run ./cmd/myroutine view --no-open` が出したパスの HTML を、手元で 1280px と 390px、ライトとダークで開き、横スクロールが無いことを確かめる
- [ ] **Step 6: commit**: `feat: 夜間実行のタブを view に足す (#67)`

## Task 4: 地図の更新

**Files:**
- Modify: `docs/ARCHITECTURE.md`

- [ ] **Step 1: 夜間実行の節を足す**: 入力 3 つの場所、`night-done` と `night-ack.jsonl`、タブを出す条件、「どこを触れば何が変わるか」の表に 3 行（入力を変える / 画面を変える / 対応済みの扱いを変える）
- [ ] **Step 2: `make ci` が通る**
- [ ] **Step 3: commit**: `docs: ARCHITECTURE に夜間実行のタブを書く (#67)`

## 夜間実行

未決の質問: 0 件
承認が要る操作: なし。この計画の範囲に、実費・外部公開・不可逆な操作は無い。実機の `~/.local/state/night-run` は読むだけで、書かない
外部SaaS課金: 触れない
停止条件: 差し戻しが 3 回、または同じ失敗が 2 連続、または質問が 3 回に達したら止めて人に報告する
権限範囲: 対象の repo の作業ツリーと、実行用の room だけ
