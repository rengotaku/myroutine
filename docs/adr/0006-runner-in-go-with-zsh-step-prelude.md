---
adr: 0006
title: runner を Go で書き直し、ステップは zsh の prelude 越しに実行する
status: accepted
date: 2026-10-05
description: ADR 0001 で捨てた Go 案を採り直す判断。境界を「環境変数で渡し、fd 3 の行で返す」既存の契約に固定し、プリミティブは zsh の prelude に閉じ込める
issues:
  - 44
tags:
  - runner
  - go
  - zsh
  - timeout
  - lock
---
# ADR 0006: runner を Go で書き直し、ステップは zsh の prelude 越しに実行する

## 背景

ADR 0001 では Go 案を「技術的には有利」と認めたうえで、次の 3 つの理由で捨てた。

1. Go の親から shell の子へプリミティブ（`capture` / `run` / `confirm`）を提供する層が要り、その層が本体より複雑になりうる
2. 既存のステップをそのまま `steps.d/` に置ける
3. 導入に Go のツールチェーンが要らない

その後の 2 か月で、状況が 2 点変わった。

- 3 は成り立たなくなった。2026-09-18 にレンダラ（`cmd/myroutine-render`）を Go で足したので、導入にはもう Go が要る
- zsh で回避策を書く箇所が増えた。trap が前景のパイプラインで保留される問題、SIGPIPE を書き込みの間だけ無視する処理、`step_interactive` の `tail -f` による追随、ステップ 1 本あたり 4 回の source、JSON の手組み。直近の修正（#38 / #40 / #42）もこの領域だった

1 について調べ直すと、懸念していた層は今の zsh 実装の中にもう存在している。プリミティブ（`bin/myroutine` の 190〜314 行）は runner の内部状態に呼び戻していない。読んでいるのは次の 3 つだけである。

- 環境変数・グローバル: `_MR_DRY_RUN` / `_MR_YES` / `_MR_IN_PARALLEL` / `_MR_STEP_NUM` / `_MR_STEP_SLUG` / `NO_COLOR`
- `/dev/tty`（`confirm` の入力）
- fd 3（`add_action` / `add_todo` / `add_info` / `emit_summary` / `step_verify` の結果を、`kind<TAB>num<TAB>slug.id<TAB>text` の 1 行で書く）

結果は今も「サブシェルが fd 3 に書いた行を、親が `_mr_drain` で読む」形で返っている。つまり境界の契約は今すでにプロセス間のものになっている。

## 決定

**runner を Go で書き直す。ステップ定義は zsh のまま残し、Go の runner はステップを zsh の prelude 越しに実行する。**

### 境界: prelude（zsh）

- `lib/prelude.zsh` を runner のバイナリに埋め込み（`embed`）、実行時に一時ディレクトリへ書き出して使う
- prelude の中身は、今のプリミティブ（190〜314 行）と `_mr_exec_step`（649〜667 行）を、ほぼそのまま移したもの
- prelude には 2 つのモードを持たせる
  - `meta`: ステップを source し、`step_*` に当たる変数をすべて（未知のものも含めて）書き出す。`step_requires` / `step_requires_path` / `step_timeout` もここで一度に取る（今は 3 回に分けて source している）
  - `exec`: ステップを source し、`step_run` と、あれば `step_verify` を実行する。結果は今と同じく fd 3 に書く
- 入力は環境変数で渡す（`MR_DRY_RUN` / `MR_YES` / `MR_IN_PARALLEL` / `MR_STEP_NUM` / `MR_STEP_SLUG`）。出力は fd 3 の行プロトコルのまま変えない
- ステップの source は 4 回から 2 回（meta と exec）に減る

fd 3 のレコードは次の形に固定する。本文は今と同じく `%`→`%25`、改行→`%0A`、タブ→`%09` で符号化する。

**表1: fd 3 のレコード**

| kind | 形 | 書き手 |
|---|---|---|
| `A` / `T` / `I` | `kind<TAB>num<TAB>slug.id<TAB>text` | `add_action` / `add_todo` / `add_info` |
| `S` | `S<TAB>num<TAB>slug.summary<TAB>text` | `emit_summary` |
| `V` | `V<TAB>num<TAB>run_rc<TAB>verify_rc` | prelude（`step_verify` があるとき） |
| `E` | `E<TAB>num<TAB>source_failed` または `E<TAB>num<TAB>step_run_undefined` | prelude |

`E` は、今の `chan.N.srcfail` / `chan.N.nofunc` のマーカーファイルを置き換える。ステップ自身が終了コード 10 や 11 を返すことがあるので、内部エラーを終了コードで伝えない、という今の規則（ARCHITECTURE.md）は維持する。

この形なら、プリミティブを Go の側に実装し直す必要が無い。ADR 0001 が懸念した「本体より複雑な層」は、今ある約 140 行の zsh を別ファイルに移したものになる。

### Go の runner が持つもの

この ADR は #43（番号をやめ、`steps.d/order` に書いた順で実行する）がマージされた後の状態を前提にする。#43 より前に着手しない。

**表2: zsh での実装と Go での置き換え**

| 責務 | 今の zsh（#43 マージ後） | Go |
|---|---|---|
| ステップの発見と順序 | `steps.d/order` に書いた順。`<slug>.sh` | 同じ `order` ファイルと同じ検証（未宣言・未配置・重複はエラー） |
| 宣言の検証 | 7 種のエラー（`_mr_read_meta`） | 既存の 7 種に加え、未知の `step_*` 変数、bool が `true`/`false` 以外、`step_timeout` が非負整数でない場合をエラーにする |
| タイムアウト | watchdog サブシェル＋マーカーファイル | `context.WithTimeout` |
| プロセスの停止 | `pgrep -P` で木を列挙してから一括で TERM → KILL | 今と同じく、停止の直前に子孫の PID を親子関係から全部列挙し、その集合とステップのプロセスグループの両方に TERM → 2 秒待って KILL を送る。`setsid` で別のグループに移っても親が生きている子は、列挙で拾える。パイプを掴んだまま残る孤児は `cmd.WaitDelay` で待ち時間を打ち切る |
| ロック | `mkdir lock.d`＋pid＋stale の回収 | zsh の runner と共存する間は、今と同じ `mkdir lock.d`＋pid＋stale の回収の手順を Go でも使う（新旧が互いを排他できるようにするため）。zsh の runner を撤去した後に `flock(2)` へ切り替えるかは、そのとき別に決める。取れなければ今どおり中止する |
| シグナル | `trap` と `_mr_on_signal`、前景パイプラインを避ける回避策 | `os/signal` で受け、実行中のプロセスグループを止めて後片付けしてから、今と同じ終了コード（130 / 143 / 129）で終わる |
| SIGPIPE（`--jsonl`） | 書き込みの間だけ無視 | `signal.Notify` で SIGPIPE を受け、書き込みエラーを「読み手が消えた」として扱う。子プロセスには既定の扱いのまま渡る（Go は子に SIG_IGN を継承させない） |
| `step_interactive` | `tail -f` で stderr へ追随 | ステップの出力はファイルに直接つなぐ。表示は別の goroutine がそのファイルを追って stderr に書く。表示先が詰まっても閉じても、ログの保存とステップの進行を止めない（`io.MultiWriter` にすると表示先の障害がステップの書き込みを止めるため、使わない） |
| 端末入力（ステップ内の `confirm`） | ステップは runner と同じプロセスグループで動き、`/dev/tty` を読める | 逐次ステップは、runner が端末の前景にいるときに限り、ステップのプロセスグループに前景を渡し（`SysProcAttr.Foreground` / `Ctty`）、終了・タイムアウト・中断のどの場合も runner に前景を戻す。runner が背景にいるときは前景を奪わない。並列ステップは前景を渡さない（並列中の `confirm` は端末を読まずに中止へ倒れる） |
| 並列 | 背景ジョブ＋`res.N` ファイル、番号順に flush | goroutine と同時実行数のセマフォ（`MYROUTINE_PARALLEL_MAX`）。gate と記録は親で行い、出力は番号順に flush する（ADR 0004 を維持） |
| 確認（gate の destructive・abort_on_fail） | `confirm` を親で呼ぶ | Go で `/dev/tty` を読む。判定順（並列中は中止 → dry-run は中止 → `-y` なら続行 → tty で y/N）は変えない |
| JSONL / JSON | 文字列の手組み | `internal/eventlog` の型をレンダラと共有して `encoding/json` で書く |
| 人が読む出力 | runner が自前で整形（`--jsonl` 無し）／レンダラ（`--jsonl` あり） | runner にレンダラを取り込み、既定の出力をレンダラの形にする。`--jsonl` と `--json` は機械向けに残す。dry-run（`-n`）のときは、`run` が出す `[dry-run] <コマンド>` の行を含むステップの出力を、成否に関係なくそのまま表示する（レンダラは成功時に出力を畳むので、そのままだと実行予定が見えなくなるため） |

### 変えない契約

- ステップの書き方: `step_*` の宣言と `step_run()`、プリミティブの名前と挙動
- `history.tsv` の 5 列、`last-run/<slug>`、`last-events.jsonl`、日次ログの置き場所と形式
- ADR 0002〜0005 の決定（チャネルと安定 id、cadence は skip、並列は連続グループ、todo は第 3 チャネル）
- ADR 0001 の「壊すと危ない前提」: ロックが取れなければ中止すること、タイムアウト時は子孫を全部列挙してから一括で落とすこと

### 移行

1. Go の runner を `cmd/myroutine` として足す。zsh の `bin/myroutine` は残す
2. `tests/myroutine.test.zsh` を Go の runner にも流す。このテストは CLI を外から叩くだけで内部関数を呼ばない（36 グループ）ので、そのまま適合テストとして使える。人が読む出力の文言を比べている箇所は、出力をレンダラの形に変えるのに合わせて直す
3. 適合テストに次を足す
   - 新旧の排他: zsh の runner が実行中に Go の runner を起動すると中止されること、その逆も中止されること
   - 別グループの子の停止: ステップが `setsid` で別グループの子を起動し、その子が TERM を無視しても、タイムアウトと Ctrl-C の後に残らないこと
   - 端末入力: PTY 上で、ステップ内の `confirm` が y/N を受け取れること。タイムアウトと Ctrl-C の後に、端末の前景が runner（の親シェル）に戻ること
   - 表示先の障害: `step_interactive` の表示先（stderr）が読まれない・途中で閉じる場合も、ログが残りステップが終わること
   - dry-run: `-n` で、各ステップの `[dry-run] <コマンド>` が画面に出ること
4. 適合テストが緑になったら、`darwin/zsh/.zsh_aliases` の `myroutine()` と README の導入手順を Go の runner に切り替える
5. 1 週間問題が出なければ、zsh の runner と ADR 0001 を退役させる（ADR 0001 の status を superseded にする）

## 捨てた案

- **プリミティブを Go のサブコマンドにする（`myroutine-prim capture ...`）**: ステップから毎回別プロセスを起動することになる。`capture` は呼び出し側のシェル変数に代入するので、外部コマンドでは実現できない。結局 zsh の関数が要る
- **ステップの契約を「実行ファイル＋JSON で結果を返す」に変える**: ステップを任意の言語で書けるようになるが、約 25 本の書き換えになる。困っているのは runner 側で、ステップ側ではない
- **zsh のまま回避策を足し続ける**: 今の回避策はそれぞれ正しく動いている。ただ、新しい要件（#43 の宣言順・開始イベント、宣言の検証）のたびに同じ領域を触ることになり、壊れやすさは減らない
- **Rust で書く**: レンダラと型・ビルド・テストを共有できない。速度と安全性の利点は、所要時間のほとんどが Brew や AWS SSO の待ちであるこの用途では効かない

## 変えてよい前提 / 壊すと危ない前提

- **変えてよい**:
  - prelude の書き出し先、KILL までの待ち時間、`WaitDelay` の値
  - 人が読む出力の細部（レンダラの責務）
- **壊すと危ない**:
  - 境界は「環境変数で渡し、fd 3 の行で返す」だけにする。prelude から Go の側へ呼び戻す経路（ソケット、サブコマンド呼び出し）を足すと、ADR 0001 が懸念した複雑さがそのまま戻ってくる
  - ステップは必ず独自のプロセスグループで起動する。親と同じグループで起動すると、タイムアウトや Ctrl-C のときに runner 自身を巻き込む
  - 停止はプロセスグループだけに頼らず、子孫の列挙と併用する。グループだけにすると、`setsid` で別グループに移った子（親はまだ生きている）を取りこぼす。親が先に死んで里子に出た子は、列挙でも拾えない。これは今の実装と同じ限界で、`WaitDelay` で runner が終わらなくなることだけは防ぐ
  - 逐次ステップに端末の前景を渡したら、どの終わり方でも必ず戻す。戻し忘れると、runner の後に親シェルが端末を読めなくなる。逆に前景を渡さないと、ステップ内の `confirm` が `/dev/tty` を読んだ時点で SIGTTIN で止まる
  - ロックが取れないときの既定は中止。続行に倒すと、多重起動で Brew と git を同時に触る。zsh の runner と共存する間は、ロックの手順を変えない
  - `confirm` は `/dev/tty` を読み、開けなければ中止側に倒す。stdin を読むように変えると、`--jsonl | myroutine-render` の構成で入力が読めなくなる

## 未決事項

- prelude を一時ディレクトリへ書き出すか、`zsh -c` に文字列で渡すか。一時ディレクトリに書くと、EDR が監視している `/tmp` 配下から zsh スクリプトを source することになる（実行するのは署名済みの `/bin/zsh` なので、バイナリ実行の検知には当たらない想定）
- `last` サブコマンドは、レンダラを取り込んだ後も `last-events.jsonl` の再生として残す
