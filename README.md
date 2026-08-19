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

ステップは同梱していません。`examples/steps.d/` を参考に自分で書きます。

```sh
cp examples/steps.d/*.sh ~/.config/myroutine/steps.d/   # 動かしてみる場合
```

## 使い方

```
myroutine                全ステップを順に実行
myroutine ls             ステップ一覧を表示
myroutine 1,3,5          指定したステップのみを指定順で実行

  -n, --dry-run          副作用を実行せず、実行予定を表示する
  -y, --yes              確認プロンプトを自動承認する
      --json             結果を機械可読な JSON で出力する
      --timeout N        ステップの既定タイムアウト秒（0 で無制限）
      --no-lock          多重起動ロックを取らない（テスト用）
```

## ステップの書き方

`~/.config/myroutine/steps.d/` に `NN-name.sh` 形式で 1 ファイル 1 ステップ置きます。
実行順はファイル名順で、**順序を変えたいときはファイルをリネームするだけ**です。

```zsh
# ~/.config/myroutine/steps.d/50-brew-update.sh
step_name="Brew Update"
step_critical=true
step_requires=(brew)

step_run() {
  local outdated=""
  capture outdated brew outdated --quiet || return 1
  [[ -z $outdated ]] && { ok "全て最新"; return 0 }

  local -a pkgs=(${(f)outdated})
  run brew upgrade ${pkgs}
  add_action outdated "${#pkgs} 件のパッケージを確認する"
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
| `step_requires` | `()` | 無いコマンドがあれば失敗ではなく skip |
| `step_requires_path` | `()` | 無いパスがあれば失敗ではなく skip |
| `step_timeout` | `600` | 秒。`0` で無制限 |
| `step_cadence` | `daily` | `daily` / `weekly` / `biweekly` / `monthly`。前回成功日からの間隔が足りなければ失敗ではなく skip |
| `step_cadence_weekday` | （なし） | `mon`..`sun` / `1`..`7`。指定すると「間隔を満たし、かつその曜日」のときだけ実行する |

### 関数

| 関数 | 意味 |
|---|---|
| `step_run` | **必須**。終了コード 0 が成功 |
| `step_verify` | 任意。定義されていれば `step_run` の終了コードより優先される「真の成否」 |

### ステップから使えるもの

| | |
|---|---|
| `ok` / `warn` / `err` / `info` | 出力ヘルパー |
| `add_action <id> "<やること>"` | 要対応サマリに載る |
| `add_info <id> "<事後報告>"` | 情報セクションに載る |
| `capture [-d 既定値] VAR cmd...` | コマンド失敗を検知できる形で出力を受け取る |
| `confirm "<問い>"` | 確認プロンプト（tty 不在なら中止側へ倒れる） |
| `run cmd...` | 副作用の実行（dry-run では実行しない） |

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
  倒します
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
  番号を指定した実行（`myroutine 5`）は明示指定なので間隔判定をせず、
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
ステップのファイル名（`NN-` を除いた部分）にはドットを使えません
（`<slug>.<id>` から slug を一意に復元するため）。

## 設定

| 環境変数 | 既定 |
|---|---|
| `MYROUTINE_STEPS_DIR` | `${XDG_CONFIG_HOME:-~/.config}/myroutine/steps.d` |
| `MYROUTINE_LOG_DIR` | `${XDG_STATE_HOME:-~/.local/state}/myroutine` |
| `MYROUTINE_TIMEOUT` | `600` |
| `MYROUTINE_STREAK_WARN` | `3` |
| `NO_COLOR` | 設定すると ANSI カラーを無効化 |

ログは `$MYROUTINE_LOG_DIR/YYYY-MM-DD.txt` に**追記**されます（端末には色付き、
ファイルには ANSI を除去して書きます）。要対応 id の履歴は `history.tsv` です。

## 前提

- **zsh 必須**（`${(f)}` などの zsh 固有機能を使っています）
- 動作確認: macOS / Linux（CI で両方を回しています）
- 外部依存: `mktemp` / `pgrep` / `sed` / `date`

## 開発

```sh
make ci        # 構文チェック + テスト（121 件）
make test
make syntax
```

テストは外部コマンド（パッケージ管理・git・クラウド CLI・ネットワーク）に一切依存せず、
振る舞いを固定した fixture ステップだけで本体の全挙動を検証します。

設計判断の経緯は [docs/adr/](docs/adr/)、構成は
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) を参照してください。

## ライセンス

Apache License 2.0
