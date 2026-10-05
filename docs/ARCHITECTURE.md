# ARCHITECTURE

myroutine の構成と、どこを触れば何が変わるか。

判断の経緯は [adr/](adr/) にある。ここは現在の姿だけを書く。

## 全体像

runner は 2 つある。移行期間中は zsh 版（`bin/myroutine`）が日常の実行に使われ、
Go 版（`cmd/myroutine`、[ADR 0006](adr/0006-runner-in-go-with-zsh-step-prelude.md)）が
同じテストを通る形で並んでいる。Go 版の構成は「[Go 版 runner](#go-版-runner)」節にある。
以下の節は、特に断りが無ければ zsh 版の説明。

```
myroutine (bin/myroutine)          ← FW（zsh 版）。この 1 ファイルで完結する
  │
  ├─ 探索      steps.d/order の宣言順にステップ（<slug>.sh）を読み、位置を番号にする
  │            order とファイルの食い違い・重複はエラーで止める。指定実行は slug
  ├─ 実行      1 ステップ = 1 サブシェル。タイムアウト監視付き
  │            step_parallel の連続範囲は 1 グループとして同時に実行する
  │            step_disabled が非空のステップは実行総数から外れ「居ない」扱いになる
  ├─ 集約      ステップからの報告を fd 3 経由で受け取る
  ├─ 分析      履歴と突き合わせて連続日数・NEW・解消を出す
  └─ 出力      人間向けサマリ / --json / --jsonl（run → start → step → summary）/ 日次ログ

$MYROUTINE_STEPS_DIR/              ← 利用者が書く。本体には同梱しない
  order                          ← 実行順（1 行 1 slug）
  network.sh
  brew-update.sh
  cleanup.sh

$MYROUTINE_LOG_DIR/
  YYYY-MM-DD.txt                   ← 人間可読な日次ログ（追記・ANSI 除去済み）
  history.tsv                      ← 要対応 id の履歴（劣化検知の入力）
  lock.d/                          ← 多重起動ロック（実行中のみ存在）
```

## 境界

**本体はステップを持たない。** 本体が知っているのは契約だけで、何をするかは
`steps.d/` の中身が決める。個人・組織に固有の情報はすべてステップ側に落ちる。

| | 本体 | ステップ |
|---|---|---|
| 知っていること | 実行順・成否の扱い・報告の集め方・安全策 | 何をするか |
| 持たないもの | 具体的なコマンド、パス、認証情報 | 実行制御、番号、サマリの組み立て |

## 実行フロー

```
main
 ├─ オプション解釈           --dry-run / --yes / --json / --timeout / --no-lock / --force-cadence
 ├─ _mr_discover             steps.d/order を読み、各ステップのメタデータを読む
 │    └─ _mr_read_meta       サブシェルで source。stdout は捨て、fd 4 でメタだけ取る
 ├─ _mr_acquire_lock         mkdir で排他。stale なら pid 生存確認のうえ回収
 └─ _mr_run_sequence
      ├─ _mr_handle_disabled  (step_disabled が非空のステップ。実行せず disabled で記録)
      ├─ _mr_run_step  (逐次のステップ)
      │    ├─ _mr_step_gate         実行前のふるい。skip / エラーはここで記録して終わる
      │    │    ├─ _mr_check_requires  前提が欠けていれば skip（失敗ではない）
      │    │    ├─ _mr_cadence_due     前回成功日から間隔が足りなければ skip（週次・月次）
      │    │    └─ 破壊的ゲート         dry-run なら実行しない / --yes 無しなら confirm
      │    ├─ _mr_run_with_timeout
      │    │    ├─ _mr_exec_step &  サブシェルで source → step_run → step_verify
      │    │    └─ 監視プロセス &    時間切れならマーカーを置いて木ごと kill
      │    └─ _mr_step_finalize     _mr_drain で報告を回収し、結果を分類して記録
      ├─ _mr_run_parallel_group  (step_parallel の連続範囲)
      │    ├─ _mr_step_gate         親で全ステップ分。出力はステップ単位に溜める
      │    ├─ _mr_exec_parallel &   実行だけを背景へ。結果はファイルで返す
      │    └─ 番号順に flush        溜めた出力を流し、_mr_step_finalize で記録
      ├─ _mr_maybe_abort            step_abort_on_fail の失敗なら続行可否を尋ねる
      └─ _mr_finish
           ├─ _mr_analyze_history   連続日数・NEW・解消を算出
           ├─ サマリ出力             人間向け or --json
           └─ _mr_append_history    全ステップ実行かつ非 dry-run のときだけ追記
```

## ステップとの通信

ステップはサブシェルで動くため、親の配列を直接は変更できない。chan はステップ単位
（`chan.<番号>`）に分かれており、並列グループでも互いの報告を上書きしない。

```
親                             子（ステップ）
 chan ファイルを空にする
 fd 3 = chan で起動  ─────────▶  add_action / add_todo / add_info が fd 3 へ 1 行書く
                                  "A<TAB>番号<TAB>slug.id<TAB>本文"
                                  本文の % / 改行 / タブは %25 / %0A / %09 へ
 _mr_drain で読み戻す ◀───────
```

ステップ番号は親が `_MR_STEP_NUM` として渡すので、ステップは自分の番号を意識しない。
id には親が `_MR_STEP_SLUG`（ファイル名から `.sh` を除いた部分）を前置する。
slug にドットを許さないのは、`<slug>.<id>` から slug を一意に復元するため。

## 状態ファイル

### `history.tsv`

```
2026-08-11<TAB>09:12:03<TAB>brew.unclassified,cleanup.pending<TAB>network,brew,cleanup<TAB>network=812,brew=664231,cleanup=1203
2026-08-12<TAB>09:08:41<TAB>brew.unclassified<TAB>network,brew<TAB>network=790,brew=531004
```

4 列目は **その実行で成功したステップの slug**。skip されたステップも、失敗・
タイムアウトしたステップも、要対応の有無を確認しきれていない = 「今回出なかった」
のではなく「見ていない」。これが無いと、一時的な skip や失敗だけで前回の要対応が
「解消」になり連続日数も失われる。4 列目が空 or `*` の行は旧形式として
「全ステップを評価した」とみなす。

5 列目は **ステップ別の所要ミリ秒**（`slug=ms` のカンマ区切り）。サマリには合計しか
出ず、ステップ別は `--json` の一過性の出力にしか無かったため、「どのステップが長いか」を
後から集計できるようにここへ残す。実行に入らなかったステップ（skip・前提不足）は載せない。
列を足すだけなので、5 列目が無い旧形式の行もそのまま読める。

実行日単位に畳んで新しい順に見て、当該 id が連続して現れる回数が「N 日連続」になる。
実行しなかった日は連続を切らない（毎日実行するとは限らないため、実行日ベースで数える）。
そのステップを評価していない日は、連続を切りも伸ばしもしない。

**dry-run・部分実行・中断した実行は記録しない。** 記録すると「今日は出なかった」と
誤って解消扱いになり、連続日数が壊れる。部分実行では解消・NEW の判定自体を行わない。

### サマリの 3 枠

要対応・ルーティン・情報を分けているのは、混ぜると読む理由が違うものが同じ列に並ぶため。

| 枠 | 入口 | 意味 | 履歴 |
|---|---|---|---|
| ⚠ 要対応 | `add_action` | 本来は無いはずのものが出ている＝劣化 | 記録する（連続日数・NEW・解消） |
| 📋 きょう回すもの | `add_todo` | 毎日そこにある定常のやること | 記録しない |
| ℹ 情報 | `add_info` | 自動で済ませた事後報告 | 記録しない |

定常のやることを `add_action` に載せると、劣化していないのに連続日数だけが伸び、
強調表示が意味を失う（要対応ボックス全体がオオカミ少年化する）。逆に劣化を
`add_todo` に載せると、放置されていることが誰にも見えなくなる。

### `lock.d/`

ディレクトリの作成がアトミックであることを排他に使う。中の `pid` ファイルで
残留ロックかどうかを判定する。

## どこを触れば何が変わるか

| やりたいこと | 触る場所 |
|---|---|
| ステップを追加する | `steps.d/` に `<slug>.sh` を置き、`order` に slug を足す（本体は触らない） |
| ステップの順序を変える | `steps.d/order` の行を入れ替える（本体も id も変わらない） |
| 出力の見た目を変える | `_mr_summary_human` / `ok` `warn` `err` `info` / `_mr_hdr` |
| JSON の項目を増やす | `_mr_summary_json` |
| `--jsonl` の項目を増やす | `_mr_jsonl_emit_start` / `_mr_jsonl_emit_step`、受け側は `internal/eventlog` と `internal/format` |
| 滞留の強調しきい値 | `MYROUTINE_STREAK_WARN` |
| 連続日数の数え方 | `_mr_analyze_history` / `_mr_history_days` |
| ステップに渡すプリミティブを増やす | 「ステップに公開するプリミティブ」節に関数を足す |
| ステップを並列で走らせる | 対象ステップに `step_parallel=true`。同時実行数は `MYROUTINE_PARALLEL_MAX` |
| 並列グループの区切りを変える | `order` の並び（グループは実行順の連続範囲で決まる） |
| ステップを設定ごと無効化する | 対象ステップに `step_disabled="<理由>"` |

## 触るときに壊しやすいところ

- **タイムアウト時の kill 順序** — `_mr_kill_tree` は木を全部数え上げてから一括で落とす。
  「親から順に辿って kill」に戻すと孫が里子に出て `pgrep` で辿れなくなり、ログ用パイプを
  掴んだ孤児が残って実行全体がハングする
- **監視プロセスの出力** — `>/dev/null 2>&1` を外すとログ用パイプを掴んで同じくハングする
- **ログ用パイプラインの実行方法** — `_mr_with_log` はパイプラインを background にして
  `wait` する。前景で走らせると zsh はパイプラインが終わるまで trap を保留するため、
  途中で届いた TERM / INT が子へ伝わらず、中断したはずの後続ステップが実行される
- **内部エラーを終了コードで伝えない** — 「読み込み失敗」「`step_run` 未定義」「タイムアウト」は
  マーカーとフラグで伝える。`10` / `11` / `124` を終了コードの意味に使うと、ステップ自身が
  同じ値を返したときに誤表示になる
- **`local x=$(cmd)`** — 本体でもステップでも書かない。`capture` を使う
- **`Makefile` の変数名** — `ZSH` は oh-my-zsh が環境変数として使っているため、
  make の `?=` より優先されて壊れる。`ZSH_BIN` を使う
- **ステップのトップレベル** — 副作用を置かない。本体はメタ読み取りと実行で 2 回 source する
- **TSV の読み方** — `IFS=$'\t' read` はタブを「IFS 空白」として扱い、連続タブを畳んで
  空フィールドを失う。「要対応ゼロの実行」の行が 1 列ずれるため、行を丸ごと読んで
  `${(@s:<TAB>:)line}` で割る
- **`capture` の代入先検証** — zsh の `status` / `path` のような特殊変数への代入は
  関数ごと即座に中断させる致命エラーで、`if ! typeset -g ...` では捕まえられない。
  サブシェルで先に試して弾いている
- **チャネルのエンコード** — バックスラッシュ方式（`\` → `\\`、改行 → `\n`）は復号が
  順序依存になり、本文中のリテラル `\n` が改行に化ける。`%` を先に退避する
  パーセント方式なら、`%25` が `%0A` / `%09` を部分文字列として含みえないので
  どちらの順で戻しても一意に復元できる
- **実行間隔の判定を本体に置く** — 週次・月次を各ステップが自分で日付判定すると、
  「今日は何もしない」が成功として記録され、要対応の解消判定と連続日数が汚れる。
  本体が skip に落とすことで「見ていない」側へ自動的に入る
- **「評価できた」の定義** — `ok` のみ。fail / timeout / skip は「見ていない」側に置く。
  ここを緩めると、判定前に落ちただけで前回の要対応が「解消」と誤表示される
- **並列グループで背景に出すもの** — 実行だけ。ゲートと記録（`_mr_record` / `_mr_actions`）を
  背景に出すと、サブシェルからは親の配列を変更できないので要対応が黙って失われる
- **並列グループの出力順** — 溜めた出力をステップ番号順に流す。完了順に流すと日次ログの
  差分が実行のたびに揺れて読めなくなる
- **並列と確認の併用** — `step_parallel` × `step_destructive` / `step_abort_on_fail` は
  discover で弾く。実行時に弾く形にすると、設定ミスが「確認の混線」という分かりにくい
  症状で出る
- **無効ステップの番号体系** — ヘッダーの `[n/N]` は `_mr_enabled_pos` / `_MR_ENABLED_TOTAL`
  （無効ステップを除いた位置と総数）を使うが、`myroutine ls` / 要対応 / `--json` の番号は
  order 上の位置（`_mr_step_files` の添字）のまま。2 つの番号体系を混同すると
  ヘッダーの表示と一覧の番号が食い違って見える

## Go 版 runner

```
cmd/myroutine/main.go          ← 入口。runner.Main を呼ぶだけ
internal/runner/
  options.go                   ← CLI と環境変数（MYROUTINE_*）の解釈
  order.go                     ← steps.d/order の読み取りと食い違いの検出
  meta.go                      ← prelude の meta モードで宣言を取り、検証する
  runner.go                    ← 全体の流れ・ロック・シグナル・出力先の用意
  sequence.go                  ← ゲート・実行・記録・並列グループ・abort・サマリ
  step.go                      ← 1 ステップの起動と待機（タイムアウト・前景・fd 3 の読み戻し）
  proc.go                      ← 子孫の列挙と停止（TERM → 2 秒 → KILL）
  tty.go / tty_*.go            ← 端末の前景の受け渡し・y/N の読み取り
  follow.go                    ← step_interactive の表示（出力ファイルを追って stderr へ）
  history.go                   ← 実行間隔（cadence）と履歴・劣化検知
  output.go                    ← 進捗（stderr）・日次ログ・イベントの配信
  list.go                      ← ls / last / 指定実行の slug の解釈
lib/prelude.zsh                ← ステップを動かす zsh の層（バイナリに埋め込み）
internal/eventlog              ← JSONL イベントと --json の型（runner とレンダラで共有）
internal/format, internal/render ← 人が読む出力（myroutine-render と共有）
```

### 境界: prelude

Go の runner はステップを `zsh <prelude> meta|exec <step.sh>` の形で起動する。prelude は
実行のたびに一時ディレクトリへ書き出す。プリミティブ（`ok` / `capture` / `confirm` /
`run` / `add_action` …）は prelude の中にあり、zsh 版と同じ名前・挙動を持つ。

| 向き | 経路 | 中身 |
|---|---|---|
| runner → ステップ | 環境変数 | `MR_DRY_RUN` / `MR_YES` / `MR_IN_PARALLEL` / `MR_STEP_NUM` / `MR_STEP_SLUG` / `MR_NO_COLOR`（prelude が `_MR_*` に移して消す） |
| ステップ → runner | fd 3 | exec: `A` / `T` / `I` / `S` / `V` / `E`（ADR 0006 表1）。meta: `M`（宣言 1 つ）/ `E` / `D`（最後まで書けた印） |

ステップの source は 2 回（meta と exec）。前提コマンドの有無はゲートの時点で
`zsh -f -c 'command -v …'` で確かめる（前のステップが入れたコマンドも見えるように）。

### 出力の出し分け

| モード | stdout | stderr | 日次ログ / last-events.jsonl |
|---|---|---|---|
| 既定 | レンダラの形（開始行・1 行の結果・サマリ）。dry-run ではステップの出力もそのまま | runner の進捗・エラー、step_interactive の生出力 | 両方書く |
| `--jsonl` | JSONL イベント | 同上 ＋ step_interactive の見出し | 両方書く |
| `--json` | 終了後に JSON を 1 個 | レンダラの形 ＋ 同上 | 両方書く |

ステップの出力は常にファイル（`out.<n>`）へ直接つなぎ、日次ログには
`── [n] 名前 ──` の見出しを付けて ANSI を除いて残す。`myroutine last` は既定で
レンダラの形に描き、`--jsonl` / `--json` を付けると保存済みの JSONL をそのまま流す。

### プロセスと端末

- ステップは必ず独自のプロセスグループで起動する（`Setpgid`）
- 逐次ステップは、runner が端末の前景にいるときだけ前景を渡し（`Foreground` / `Ctty`）、
  終了・タイムアウト・中断のどの場合も runner に戻す。端末設定も渡す前の状態に戻す。
  前景を渡したステップが端末からの Ctrl-C で落ちたら、runner への SIGINT と同じく全体を中断する
- 並列ステップには前景を渡さない（並列中の `confirm` は端末を読まずに中止へ倒れる）
- 停止は `ps` で子孫を列挙した集合とプロセスグループの両方へ TERM → 2 秒 → KILL
- ロックは zsh 版と同じ `mkdir lock.d` ＋ pid ＋ stale 回収で、新旧が互いを排他する

### Go 版で触るときに壊しやすいところ

- **prelude から Go へ呼び戻す経路を足さない** — 境界は環境変数と fd 3 の行だけ
- **ステップの出力に `io.MultiWriter` を使わない** — 表示先の障害でステップの書き込みが止まる。
  表示は `follower` がファイルを追う
- **前景を渡したら必ず戻す** — `handover.restore` を通らない終わり方を作ると、runner の後に
  親シェルが端末を読めなくなる
- **SIGTTOU の無視は `setForeground` の間だけ** — 無視したままにすると以後の子へ SIG_IGN が
  引き継がれる。ハンドラ（Notify）にすると tcsetpgrp が再開され続けて戻らない
- **SIGPIPE は Notify で受ける** — `signal.Ignore` にすると子に SIG_IGN が引き継がれ、
  ステップの外部コマンドが閉じたパイプで終われなくなる

## テスト

`tests/myroutine.test.zsh` が本体の全挙動を検証する。CLI を外から叩くだけなので、
`MYROUTINE_TEST_BIN` で対象を Go 版に差し替えて同じテストを流せる（`make test-go`）。
人が読む出力の文言が実装で違う箇所だけ `pick` で期待値を分けている。

外部コマンド（パッケージ管理・git・クラウド CLI・ネットワーク）には一切依存せず、
振る舞いを固定した fixture ステップをテスト内で生成して使う。ステップを本体から
分離したことで、この形が成立している。
