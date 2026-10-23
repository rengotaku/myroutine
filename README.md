# myroutine

安全にコマンドを実行し、**放置された要対応を可視化する** routine runner（zsh）。

毎朝叩く自動化スクリプトが育って手に負えなくなる、という問題のための道具です。

```
━━━━━ [3/6] Brew Update ━━━━━━━━━━━━━━━━━━━━━            12s
  ✓ 自動更新: 15 件
  ▸ 未分類 3 件

════════════════════════════════════════════
  ⚠ 要対応 3 件

  ▸ [3] 未分類パッケージ 3 件の分類                  NEW
  ▸ [5] 退避済みディレクトリ 1 件の確認              (14 日連続)
  ▸ [6] 取りこぼし 12 件の棚卸し                     (9 日連続)

  📋 きょう回すもの 1 件

  □ [4] /routine.progress-report を回す

  ℹ 情報 1 件（対応不要）

  · [5] 古いファイル 2 件を自動削除した
  · 解消: network.down
════════════════════════════════════════════
  所要 1m24s
```

## task runner ではありません

| | task runner (make / just / task) | routine runner |
|---|---|---|
| 失敗したら | 止まる | **止まらない**（認証が落ちてもパッケージ更新はしたい） |
| 出力 | 各タスクのログ | **「人間がこれから何をすべきか」を末尾に集約** |
| critical | 中断フラグ | **続行するが要対応として記録するフラグ** |

そして、要対応を出すだけでは足りません。**同じ要対応が毎日出続けていることに気づけない**
のが実際の失敗モードなので、連続掲示日数を数えて表示します。

## インストール

```sh
git clone https://github.com/rengotaku/myroutine.git
ln -s "$PWD/myroutine/bin/myroutine" ~/bin/myroutine   # PATH の通った場所へ
mkdir -p ~/.config/myroutine/steps.d
```

出力を整形する `myroutine-render` は別バイナリです。`--jsonl` をこれに繋ぐと、実行に入ったステップは開始行（`▶ [5/24] Brew Update  開始 08:38`）が出ます。結果行は「記号 [位置/全件] 名前  一言  所要時間」の書式で、成功したステップは 1 行に畳まれ、実行間隔による skip は末尾の「今日は対象外」にまとめて出ます（繋がなければ従来どおりの出力です）。

```sh
make -C myroutine install PREFIX=~/bin   # 上の ln -s と同じ、PATH の通った場所へ
myroutine --jsonl | myroutine-render
```

`PREFIX` の既定は `~/.local/bin` です。そこが PATH に入っていない環境（macOS の標準構成など）では、上のように PATH の通った場所を指定するか、`~/.local/bin` を PATH へ追加してください。

### Go 版 runner（移行中）

runner を Go で書き直した版が `cmd/myroutine` にあります（[ADR 0006](docs/adr/0006-runner-in-go-with-zsh-step-prelude.md)）。
ステップの書き方・CLI・状態ファイルは zsh 版と同じで、ステップは埋め込みの zsh の層（`lib/prelude.zsh`）越しに動きます。
`make install` で `myroutine-render` と一緒に `$(PREFIX)/myroutine` として入ります。
移行期間中は zsh 版の `bin/myroutine` も残り、上の `ln -s` で入れた呼び出しはそのまま zsh 版を指します。

```sh
make -C myroutine install PREFIX=~/.local/bin
~/.local/bin/myroutine          # 既定の出力が myroutine-render の形になる（パイプ不要）
~/.local/bin/myroutine --jsonl  # 機械向け。--json も同じく使える
```

zsh 版との違い:

- 既定の出力がレンダラの形になります。成功したステップの出力は畳まれ、全文は日次ログに残ります。dry-run（`-n`）ではステップの出力（`[dry-run] <コマンド>`）をそのまま表示します
- `myroutine last` は既定で直近の実行をレンダラの形で描きます。保存済みの JSONL は `myroutine --jsonl last` で取り出せます
- 宣言の検証が増えました。未知の `step_*` 変数、bool（`step_critical` / `step_parallel` / `step_interactive` / `step_abort_on_fail` / `step_destructive`）に `true` / `false` 以外、`step_timeout` に 0 以上の整数以外を書くとエラーになります
- 逐次ステップは、runner が端末の前景にいるときだけ端末を受け取ります（ステップ内の `confirm` が読めるように）。終わり方にかかわらず端末は runner に戻ります
- ロックは zsh 版と同じ `lock.d` を使うので、新旧を同時に起動すると後から起動した方が中止されます

ステップは同梱していません。`examples/steps.d/` を参考に自分で書きます。

```sh
cp examples/steps.d/* ~/.config/myroutine/steps.d/   # 動かしてみる場合（order も一緒にコピーする）
```

## 使い方

```
myroutine                全ステップを順に実行
myroutine ls             ステップ一覧を表示
myroutine brew,omz       指定した slug のステップのみを指定順で実行（slug は ls で確認）

  -n, --dry-run          副作用を実行せず、実行予定を表示する
  -y, --yes              確認プロンプトを自動承認する
      --json             結果を機械可読な JSON で出力する
      --timeout N        ステップの既定タイムアウト秒（0 で無制限）
      --no-lock          多重起動ロックを取らない（テスト用）
```

## ステップの書き方

`~/.config/myroutine/steps.d/` に `<slug>.sh` 形式で 1 ファイル 1 ステップ置きます。
slug はファイル名から `.sh` を除いたものです（英数字と `_` `-` だけ。数字だけの名前と `ls` / `last` は使えません）。

実行順は `steps.d/order` に 1 行 1 slug で書いた順です。`#` 以降はコメント、空行は無視します。
**順序を変えたいときは `order` の行を入れ替えるだけ**で、ファイル名は変わりません。

```
# ~/.config/myroutine/steps.d/order
network
brew-update   # 週次の更新
cleanup
```

次のどれも、黙って読み飛ばさずエラーで止まります。

- ファイルがあるのに `order` に無い slug
- `order` にあるのにファイルが無い slug
- `order` 内の重複
- `order` ファイル自体が無い

旧形式（`NN-name.sh`）から移行するときは、ファイル名から `NN-` を外して `name.sh` にし、`order` に `name` を旧番号の順に書きます。
slug は旧形式でも `NN-` を除いた名前だったので、こうすれば `last-run/<slug>` と要対応の履歴がそのまま引き継がれます。
`NN-name` のまま `order` に書くと slug が変わり、週次・月次のステップが初回扱いで走り直します。

指定実行は slug で行います（`myroutine brew-update`、`myroutine brew,omz`、`myroutine brew omz`）。
数字を渡すとエラーになります。slug は `myroutine ls` で確認できます。

```zsh
# ~/.config/myroutine/steps.d/brew-update.sh
step_name="Brew Update"
step_critical=true
step_requires=(brew)

step_run() {
  local outdated=""
  capture outdated brew outdated --quiet || return 1
  [[ -z $outdated ]] && { emit_summary "更新なし"; return 0 }

  local -a pkgs=(${(f)outdated})
  run brew upgrade ${pkgs}
  emit_summary "${#pkgs} 件更新"
  add_action outdated "${#pkgs} 件のパッケージを確認する" --cmd "brew outdated"
}

# brew は pin された依存があると、更新に成功していても非 0 を返すことがある。
# 終了コードではなく状態を見て判定する。
step_verify() { [[ -z $(brew outdated --quiet) ]] }
```

### メタデータ

| 変数 | 既定 | 意味 |
|---|---|---|
| `step_name` | **必須** | 一覧とヘッダーに出る名前 |
| `step_critical` | `false` | 失敗しても後続は続行するが、要対応に critical として記録する |
| `step_abort_on_fail` | `false` | 失敗したら続行可否を尋ねる |
| `step_destructive` | `false` | dry-run では実行せず、`--yes` が無ければ確認を強制する |
| `step_parallel` | `false` | 実行順で連続する parallel ステップを 1 グループとして同時に実行する。`step_destructive` / `step_abort_on_fail` とは併用できない |
| `step_interactive` | `false` | 実行中の出力を人が見る必要があるステップに宣言する。`--jsonl` 実行中でも生出力を stderr へそのまま流す（ログへの記録は宣言の有無にかかわらず従来どおり）。`step_parallel` とは併用できない |
| `step_requires` | `()` | 無いコマンドがあれば失敗ではなく skip |
| `step_requires_path` | `()` | 無いパスがあれば失敗ではなく skip |
| `step_timeout` | `600` | 秒。`0` で無制限 |
| `step_cadence` | `daily` | `daily` / `weekly` / `biweekly` / `monthly`。前回成功日からの間隔が足りなければ失敗ではなく skip |
| `step_cadence_weekday` | （なし） | `mon`..`sun` / `1`..`7`。指定すると「間隔を満たし、かつその曜日」のときだけ実行する |
| `step_disabled` | 空文字列 | 非空文字列（無効化の理由）を入れると実行しない。slug を指定しても実行しない。実行総数・ヘッダー番号から外れ、結果は `disabled` として記録され要対応の解消判定・連続日数・履歴に影響しない。`true`/`false` はエラーになる（bool ではなく理由の文字列） |

### 関数

| 関数 | 意味 |
|---|---|
| `step_run` | **必須**。終了コード 0 が成功 |
| `step_verify` | 任意。定義されていれば `step_run` の終了コードより優先される「真の成否」 |

### ステップから使えるもの

| | |
|---|---|
| `emit_summary "<一言>"` | 成功したときの結果行の一言（下の「結果行の書式」のルールに従う） |
| `ok` / `warn` / `err` / `info` | 出力ヘルパー（本文。ログに残り、失敗時は末尾が画面に出る） |
| `add_action <id> "<やること>" --cmd "<コマンド>"` | 要対応サマリに載る（劣化検知の対象）。`--cmd` は必須で、複数回指定できる（下の「要対応のコマンド」） |
| `add_todo <id> "<きょう回すもの>"` | ルーティンセクションに載る（劣化検知の対象外） |
| `add_info <id> "<事後報告>"` | 情報セクションに載る |
| `capture [-d 既定値] VAR cmd...` | コマンド失敗を検知できる形で出力を受け取る |
| `confirm "<問い>"` | 確認プロンプト（tty 不在なら中止側へ倒れる） |
| `run cmd...` | 副作用の実行（dry-run では実行しない） |

### 要対応のコマンド

要対応は「人が次に打つもの」を出す場所なので、打つコマンドを `--cmd` で必ず渡します。
本文の後ろに `→ $ <コマンド>` の行として付き、要対応の表示で色が付き、端末幅で折り返されません。

```zsh
add_action sso-login "AWS SSO のセッションが切れている" --cmd "aws sso login --profile ${profile}"
add_action review "advisory の棚卸しが溜まっている" --cmd "/routine.guard-promote"
```

- 画面操作しか無いときも、開くためのコマンドを書く（`open -a "Google Chrome" chrome://settings/help` など）
- `--cmd` の無い要対応は、Go 版の runner が `<id>.__cmd` の要対応として積みます（終了コードは変えない。
  `MYROUTINE_ACTION_CHECK=0` で切れる）。実行されない分岐は実行時には見つからないので、
  steps.d を管理するリポジトリ側の CI で「全ての `add_action` に `--cmd` があるか」を静的に検査してください

### 結果行の書式

結果行は状態によらず次の 1 つの書式で出ます（[ADR 0007](docs/adr/0007-uniform-result-line.md)）。

```
  ✓ [4/25] Worktree 同期                     25/25 同期（5 件更新）  21.6s
  ✗ [3/25] Dotfiles 差分チェック＋同期       失敗（終了コード 1）  6.7s
  ⊘ [19/25] 放置 tmux セッションの回収       skip（tmux が無い）
```

失敗と skip の一言は runner が作ります。ステップが書くのは**成功したときの一言だけ**で、
次のルールに従います。

- `emit_summary` を必ず呼ぶ。対象が 0 件で何もしなかったときも `対象なし` のように書く
- 何がどうなったかを件数か状態で書く（`3 件更新` / `対象なし` / `差分なし`）
- 「完了」「成功」「OK」は書かない（`✓` と同じことを言うだけになる）
- 日本語で書く。英単語（3 文字以上）は使わない。`AWS` のような大文字だけの略語は可
- 全角 20 文字（40 カラム）以内。詳しいことは本文の `ok` / `info` に書く

ルールに反した成功ステップは、実行のたびに要対応に `<slug>.__summary` として積まれます
（一言が無いときは結果行に `要約なし` と出ます）。dry-run では検査しません。

> **ステップファイルはトップレベルで副作用を持たせないでください。**
> 本体はメタデータの読み取りと実行で 2 回 source します。トップレベルに置いてよいのは
> `step_*` 変数の代入と関数定義だけです。

## 安全のしくみ

各ステップに自前で書かせず、本体が引き受けます。

- **`capture`** — `local x=$(cmd)` は代入の終了ステータスが `local` のものになるため、
  コマンドの失敗を検知できません（fail-open）。`capture` は非 0 のとき変数を設定せず、
  そのまま非 0 を返します。「失敗したら既定値でよい」ケースは `-d` で明示させます
- **`step_verify`** — 終了コードが嘘をつくコマンドがあります。状態を再取得した判定を
  終了コードより優先します
- **`confirm`** — tty が取れない環境（パイプ実行・CI・スケジューラ経由）では必ず中止側へ
  倒します。並列グループの中も同じ扱いです（誰の問いか分からない答えを受け取りうるため）
- **`run` + `--dry-run`** — 副作用を本体経由に強制するので、dry-run が「ステップの礼儀」
  ではなく構造で担保されます
- **`step_destructive`** — 破壊的操作を本体から見えるようにし、dry-run での実行禁止と
  確認の強制を効かせます
- **多重起動ロック** — 2 つ目の実行は中止します。`flock` は macOS に無いので `mkdir` の
  アトミック性で排他し、残留ロックは pid の生存確認で回収します
- **`step_timeout`** — ハングしたステップを打ち切り、子孫プロセスごと落とします
- **`step_cadence`** — 週次・月次のステップを「毎日走らせて中で日付を見る」形にしません。
  本体が前回成功日（`$MYROUTINE_LOG_DIR/last-run/<slug>`）を見て skip します。skip は
  「見ていない」側なので、要対応の解消判定と連続日数からも自動的に外れます。
  slug を指定した実行（`myroutine brew`）は明示指定なので間隔判定をせず、
  `--force-cadence` で全ステップの間隔判定を無視できます

## 可視化のしくみ

- **連続掲示日数** — 同じ要対応が何日続けて出ているかを表示します
- **NEW / 解消** — 前回の実行と比べて増えたもの・消えたものを出します
- **所要時間** — ステップごとと合計
- **`--json`** — 通知や別ツールへ渡すための機械可読な出力（進捗は stderr へ逃がします）

要対応の追跡キーは `add_action` の `<id>` です。**件数・日付・パスのような可変値を
id に含めないでください。** 含めると毎回別物として扱われ、滞留を検知できなくなります。

skip・失敗・タイムアウトしたステップの要対応は「今回出なかった」のではなく
「見ていない」ものとして扱い、解消判定と連続日数の対象から外します。
ステップの slug（ファイル名の `.sh` を除いた部分）にはドットを使えません
（`<slug>.<id>` から slug を一意に復元するため）。

## 設定

| 環境変数 | 既定 |
|---|---|
| `MYROUTINE_STEPS_DIR` | `${XDG_CONFIG_HOME:-~/.config}/myroutine/steps.d` |
| `MYROUTINE_LOG_DIR` | `${XDG_STATE_HOME:-~/.local/state}/myroutine` |
| `MYROUTINE_TIMEOUT` | `600` |
| `MYROUTINE_STREAK_WARN` | `3` |
| `MYROUTINE_PARALLEL_MAX` | `4` |
| `MYROUTINE_SUMMARY_CHECK` | 未設定（有効）。`0` で成功ステップの一言の検査を切る（Go 版のみ。テスト向け） |
| `NO_COLOR` | 設定すると ANSI カラーを無効化 |

ログは `$MYROUTINE_LOG_DIR/YYYY-MM-DD.txt` に**追記**されます（端末には色付き、
ファイルには ANSI を除去して書きます）。要対応 id の履歴は `history.tsv` です。

## 前提

- **zsh 必須**（`${(f)}` などの zsh 固有機能を使っています）
- 動作確認: macOS / Linux（CI で両方を回しています）
- 外部依存: `mktemp` / `pgrep` / `sed` / `date`

## 開発

```sh
make ci        # 構文チェック + zsh 版のテスト + Go 版への適合テスト + go test
make test      # zsh 版 runner のテスト
make test-go   # 同じテストを Go 版 runner に流す
make syntax
```

テストは外部コマンド（パッケージ管理・git・クラウド CLI・ネットワーク）に一切依存せず、
振る舞いを固定した fixture ステップだけで本体の全挙動を検証します。

設計判断の経緯は [docs/adr/](docs/adr/)、構成は
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) を参照してください。

## ライセンス

Apache License 2.0
