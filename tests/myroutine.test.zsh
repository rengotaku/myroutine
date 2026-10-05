#!/bin/zsh
#
# myroutine 本体のテスト。
#
# 外部コマンド（brew / git / クラウド CLI / ネットワーク）に一切依存しない。
# 振る舞いを固定した fixture ステップだけで FW の全挙動を検証する。
#
#   zsh tests/myroutine.test.zsh
#

emulate -R zsh
zmodload zsh/datetime
setopt pipefail

typeset -g MR=${0:A:h}/../bin/myroutine
typeset -g PASS=0 FAIL=0
typeset -ga _TMPDIRS=()

export NO_COLOR=1

cleanup() {
  local d=""
  for d in $_TMPDIRS; do [[ -n $d ]] && rm -rf "$d"; done
}
trap cleanup EXIT INT TERM

# ─────────────────────────────────────────────────────────────────
# アサーション
# ─────────────────────────────────────────────────────────────────

group() { print -r -- ""; print -r -- "── $1" }
pass()  { PASS=$((PASS+1)); print -r -- "   ok   $1" }
fail()  {
  FAIL=$((FAIL+1))
  print -ru2 -- "   FAIL $1"
  [[ -n ${2:-} ]] && print -ru2 -- "        $2"
}

assert_contains() {
  local hay=$1 needle=$2 label=$3
  if [[ $hay == *"$needle"* ]]; then pass "$label"
  else fail "$label" "「${needle}」が出力に含まれない"; fi
}
assert_not_contains() {
  local hay=$1 needle=$2 label=$3
  if [[ $hay != *"$needle"* ]]; then pass "$label"
  else fail "$label" "「${needle}」が出力に含まれてしまった"; fi
}
assert_eq() {
  local got=$1 want=$2 label=$3
  if [[ $got == $want ]]; then pass "$label"
  else fail "$label" "期待=${want} 実際=${got}"; fi
}

# ─────────────────────────────────────────────────────────────────
# fixture ヘルパー
# ─────────────────────────────────────────────────────────────────

newenv() {
  local td=$(mktemp -d -t mrtest-XXXXXX) || exit 1
  _TMPDIRS+=("$td")
  mkdir -p "$td/steps.d" "$td/log"
  export MYROUTINE_STEPS_DIR="$td/steps.d" MYROUTINE_LOG_DIR="$td/log"
  export MR_TD=$td
}

# ステップを置く。name は「NN-slug.sh」で渡す（NN は実行順を決めるためのテスト用の
# 並べ替えキーで、本体には渡らない）。ファイルは <slug>.sh として置き、order は
# NN の昇順に作り直す。本体の契約（order の宣言順）をテストの書きやすさと両立させる。
mkstep() {
  local name=$1; shift
  local slug=${${name#*-}%.sh}
  print -r -- "$*" > "$MYROUTINE_STEPS_DIR/${slug}.sh"
  local keys="$MYROUTINE_STEPS_DIR/.keys"
  { [[ -f $keys ]] && grep -v -F -x -e "${name%%-*} $slug" "$keys"; print -r -- "${name%%-*} $slug" } > "$keys.new"
  mv "$keys.new" "$keys"
  sort -s -k1,1n "$keys" | awk '{print $2}' > "$MYROUTINE_STEPS_DIR/order"
}

# 標準入力を切って実行し、stdout+stderr をまとめて返す。
# tty を持たない環境で走らせることで、確認プロンプトの fail-closed も同時に検証できる。
mr() { "$MR" --no-lock "$@" </dev/null 2>&1 }
mr_rc() { "$MR" --no-lock "$@" </dev/null >/dev/null 2>&1; print -r -- $? }
# stdout だけを返す（--jsonl / --json は stdout と stderr を厳密に分けているため、
# 「stdout に人間向け出力が混ざっていないか」の検証には 2>&1 で混ぜてはいけない）。
mr_stdout() { "$MR" --no-lock "$@" 2>/dev/null </dev/null }
# stderr だけを返す（step_interactive の出力先を検証するため、stdout と混ぜない）。
mr_stderr() { "$MR" --no-lock "$@" 2>&1 1>/dev/null </dev/null }

# ─────────────────────────────────────────────────────────────────
group "ステップ探索とプラグイン性"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-alpha.sh 'step_name="アルファ"
step_run() { ok run }'
mkstep 20-bravo.sh 'step_name="ブラボー"
step_run() { ok run }'
out=$(mr ls)
assert_contains "$out" "1. alpha  アルファ" "ls: 位置番号と slug が出る (1)"
assert_contains "$out" "2. bravo  ブラボー" "ls: 位置番号と slug が出る (2)"
assert_contains "$out" "alpha.sh" "ls: ファイル名も出る"

mkstep 15-charlie.sh 'step_name="チャーリー"
step_run() { ok run }'
out=$(mr ls)
assert_contains "$out" "2. charlie  チャーリー" "ファイルを置いて order に足すとステップが増える"

print -l alpha bravo charlie > "$MYROUTINE_STEPS_DIR/order"
out=$(mr ls)
assert_contains "$out" "2. bravo" "order の行を入れ替えるだけで順序が変わる (bravo)"
assert_contains "$out" "3. charlie" "order の行を入れ替えるだけで順序が変わる (charlie)"

# order のコメント・空行・前後の空白
print -r -- "# 実行順
alpha   # 先頭

  bravo
charlie" > "$MYROUTINE_STEPS_DIR/order"
out=$(mr ls)
assert_contains "$out" "1. alpha" "order: # 以降のコメントを無視する"
assert_contains "$out" "2. bravo" "order: 空行と前後の空白を無視する"
assert_contains "$out" "3. charlie" "order: コメント行・空行で位置がずれない"

# order に無いファイル
newenv
mkstep 10-alpha.sh 'step_name="A"
step_run() { : }'
mkstep 20-bravo.sh 'step_name="B"
step_run() { : }'
print -l alpha > "$MYROUTINE_STEPS_DIR/order"
out=$(mr ls)
assert_contains "$out" "order に書かれていないステップがあります: bravo.sh" "order に無い .sh をエラーにする（黙って無視しない）"
assert_eq "$(mr_rc ls)" "1" "order に無い .sh は終了コード 1"
assert_eq "$(mr_rc)" "1" "order に無い .sh では全体実行も止まる"

# order にあるがファイルが無い
print -l alpha bravo ghost > "$MYROUTINE_STEPS_DIR/order"
out=$(mr ls)
assert_contains "$out" "ファイルがありません: ghost.sh" "order にあるのにファイルが無い slug をエラーにする"
assert_eq "$(mr_rc ls)" "1" "ファイル欠落は終了コード 1"

# order 内の重複
print -l alpha bravo alpha > "$MYROUTINE_STEPS_DIR/order"
out=$(mr ls)
assert_contains "$out" "order で slug が重複しています: alpha" "order 内の重複をエラーにする"
assert_eq "$(mr_rc ls)" "1" "order の重複は終了コード 1"

# order ファイル自体が無い
rm "$MYROUTINE_STEPS_DIR/order"
out=$(mr ls)
assert_contains "$out" "実行順の宣言ファイルがありません" "order ファイルが無ければエラーにする"
assert_eq "$(mr_rc)" "1" "order ファイルが無いと終了コード 1"

# サブコマンドと衝突する slug / 数字だけの slug
newenv
mkstep 10-ls.sh 'step_name="L"
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "サブコマンドと衝突しています: ls" "slug が ls / last と衝突したらエラーにする"
newenv
mkstep 10-42.sh 'step_name="N"
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "slug を数字だけにはできません: 42" "数字だけの slug をエラーにする"

# 複数の問題は 1 回で全部出す
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { : }'
mkstep 20-b.sh 'step_name="B"
step_run() { : }'
print -l a ghost > "$MYROUTINE_STEPS_DIR/order"
out=$(mr ls)
assert_contains "$out" "ghost.sh" "複数の不整合: ファイル欠落を報告する"
assert_contains "$out" "b.sh" "複数の不整合: order 漏れも同時に報告する"

newenv
mkstep 10-noname.sh 'step_run() { : }'
out=$(mr ls)
assert_contains "$out" "step_name が定義されていません" "step_name 未定義をエラーにする"

newenv
mkstep 10-norun.sh 'step_name="run なし"'
out=$(mr)
assert_contains "$out" "step_run が定義されていません" "step_run 未定義をエラーにする"

# ─────────────────────────────────────────────────────────────────
group "critical と実行継続"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-crit.sh 'step_name="critical で失敗"
step_critical=true
step_run() { err "わざと失敗"; return 7 }'
mkstep 20-after.sh 'step_name="後続"
step_run() { ok "後続が実行された" }'
out=$(mr)
assert_contains "$out" "後続が実行された"        "critical 失敗でも後続が実行される"
assert_contains "$out" "（critical）— 要確認"    "critical 失敗が要対応に載る"
assert_contains "$out" "(rc=7)"                  "失敗した終了コードが表示される"
assert_eq "$(mr_rc)" "1" "critical 失敗で全体の終了コードが 1 になる"

newenv
# ステップ名に "critical" を含めない。ヘッダーに名前が出るため、
# 「critical と記録されていないこと」の検証が自分の名前で誤検知する。
mkstep 10-soft.sh 'step_name="ふつうに失敗"
step_run() { return 3 }'
mkstep 20-after.sh 'step_name="後続"
step_run() { ok done }'
out=$(mr)
assert_not_contains "$out" "critical" "非 critical の失敗は critical として記録されない"
assert_eq "$(mr_rc)" "0" "非 critical の失敗は全体の終了コードに影響しない"

# ─────────────────────────────────────────────────────────────────
group "要対応 / 情報の集約と自動採番"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-act.sh 'step_name="要対応"
step_run() { add_action pending "やること A" }'
mkstep 20-noop.sh 'step_name="なにもしない"
step_run() { ok done }'
mkstep 30-inf.sh 'step_name="事後報告"
step_run() { add_info purged "自動で片付けた" }'
out=$(mr)
assert_contains "$out" "⚠ 要対応 1 件"        "要対応の件数が出る"
assert_contains "$out" "▸ [1] やること A"     "要対応にステップ番号が自動で付く"
assert_contains "$out" "ℹ 情報 1 件"          "情報が別枠で出る"
assert_contains "$out" "· [3] 自動で片付けた" "情報にステップ番号が自動で付く"

newenv
mkstep 10-clean.sh 'step_name="クリーン"
step_run() { ok done }'
out=$(mr)
assert_contains "$out" "✓ 要対応なし" "要対応 0 件ならクリーン表示になる"

newenv
mkstep 10-badid.sh 'step_name="不正 id"
step_run() { add_action "bad id!" "本文" }'
out=$(mr)
assert_contains "$out" "id に使えるのは" "id の文字種を検証する"

# ─────────────────────────────────────────────────────────────────
group "ステップ間のスコープ分離"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-define.sh 'step_name="定義する"
step_run() { LEAKED_VAR=leaked; leaked_fn() { : }; ok defined }'
mkstep 20-check.sh 'step_name="漏れを確認"
step_run() {
  local bad=0
  [[ -n ${LEAKED_VAR:-} ]] && { err "変数が漏れている"; bad=1 }
  (( ${+functions[leaked_fn]} )) && { err "関数が漏れている"; bad=1 }
  (( bad == 0 )) && ok "漏れなし"
  return $bad
}'
out=$(mr)
assert_contains "$out" "漏れなし" "変数・関数が次のステップへ漏れない"

out=$(zsh -c "PATH=${MR:A:h}:\$PATH; MYROUTINE_STEPS_DIR=$MYROUTINE_STEPS_DIR MYROUTINE_LOG_DIR=$MYROUTINE_LOG_DIR myroutine ls >/dev/null 2>&1; print -r -- \${+functions[add_action]}\${+functions[ok]}")
assert_eq "$out" "00" "実行後に呼び出し元シェルへ関数が残らない"

# ─────────────────────────────────────────────────────────────────
group "capture — 判定値の fail-open 防止"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-cap.sh 'step_name="capture"
step_run() {
  local v=""
  capture v printf hello || { err "成功時に失敗を返した"; return 1 }
  [[ $v == hello ]] || { err "値が取れていない: $v"; return 1 }
  ok "成功時に値が入る"

  UNSET_MARK=sentinel
  if capture UNSET_MARK sh -c "exit 4"; then
    err "失敗を検知できていない"
    return 1
  fi
  [[ $UNSET_MARK == sentinel ]] || { err "失敗時に変数が書き換わった"; return 1 }
  ok "失敗時は非 0 を返し変数を書き換えない"

  local d=""
  capture -d fallback d sh -c "exit 9" || { err "既定値指定で非 0 が返った"; return 1 }
  [[ $d == fallback ]] || { err "既定値が入っていない: $d"; return 1 }
  ok "-d で既定値を明示できる"
  return 0
}'
out=$(mr)
assert_contains "$out" "成功時に値が入る"                     "capture: 成功時に値を取れる"
assert_contains "$out" "失敗時は非 0 を返し変数を書き換えない" "capture: 失敗を検知し変数を汚さない"
assert_contains "$out" "-d で既定値を明示できる"              "capture: 既定値を明示できる"

# zsh の status / path のような特殊変数は readonly 相当で代入に失敗する。
# ここで握り潰すと「値が入っていないのに成功」になり、capture の存在意義が崩れる。
newenv
mkstep 10-capro.sh 'step_name="予約変数への capture"
step_critical=true
step_run() {
  if capture status printf hello; then
    err "代入に失敗したのに成功を返した"
    return 1
  fi
  ok "代入失敗を検知した"
  return 0
}'
out=$(mr)
assert_contains "$out" "代入失敗を検知した"     "capture: 予約変数への代入失敗を検知する"
assert_contains "$out" "変数に代入できません" "capture: 代入できない理由を示す"

newenv
mkstep 10-caprc.sh 'step_name="capture の rc"
step_run() { local v=""; capture v sh -c "exit 5"; return $? }'
out=$(mr)
assert_contains "$out" "(rc=5)" "capture: 失敗したコマンドの rc がそのまま伝わる"

# ─────────────────────────────────────────────────────────────────
group "step_verify — exit code を信用しない検証"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-vv.sh 'step_name="run 成功 verify 成功"
step_run() { return 0 }
step_verify() { return 0 }'
assert_eq "$(mr_rc vv)" "0" "verify: run 成功 / verify 成功 → 成功"

newenv
mkstep 10-vf.sh 'step_name="run 成功 verify 失敗"
step_critical=true
step_run() { return 0 }
step_verify() { return 1 }'
out=$(mr)
assert_contains "$out" "step_run は成功したが step_verify が失敗した" "verify: 嘘の成功を検証で失敗にする"
assert_eq "$(mr_rc)" "1" "verify: 検証失敗が全体の終了コードに反映される"

newenv
mkstep 10-fv.sh 'step_name="run 失敗 verify 成功"
step_critical=true
step_run() { return 1 }
step_verify() { return 0 }'
out=$(mr)
assert_contains "$out" "step_verify は通過した" "verify: 嘘の失敗を検証で成功にする"
assert_eq "$(mr_rc)" "0" "verify: 検証成功なら全体の終了コードも 0"

newenv
mkstep 10-ff.sh 'step_name="run 失敗 verify 失敗"
step_critical=true
step_run() { return 1 }
step_verify() { return 1 }'
assert_eq "$(mr_rc)" "1" "verify: run 失敗 / verify 失敗 → 失敗"

# ─────────────────────────────────────────────────────────────────
group "confirm — 非対話環境で fail-closed"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-conf.sh 'step_name="確認"
step_run() {
  if confirm "実行しますか?"; then ok "承認された"; else ok "中止側に倒れた"; fi
  return 0
}'
out=$(mr)
assert_contains "$out" "中止側に倒れた"    "confirm: tty 不在なら中止側に倒れる"
assert_not_contains "$out" "device not configured" "confirm: tty のエラーを出力に漏らさない"
out=$(mr --yes)
assert_contains "$out" "承認された"        "confirm: --yes で承認される"
out=$(mr --dry-run)
assert_contains "$out" "[dry-run] 確認を求める" "confirm: dry-run では尋ねずに予定を表示する"

# ─────────────────────────────────────────────────────────────────
group "run / --dry-run — 副作用の構造的ゲート"
# ─────────────────────────────────────────────────────────────────

newenv
touch "$MR_TD/victim"
mkstep 10-side.sh "step_name=\"副作用\"
step_run() { run rm -f \"$MR_TD/victim\"; ok done }"
out=$(mr --dry-run)
assert_contains "$out" "[dry-run] rm -f" "dry-run: 実行予定を表示する"
if [[ -f "$MR_TD/victim" ]]; then pass "dry-run: 副作用が発生しない"
else fail "dry-run: 副作用が発生しない" "ファイルが消えた"; fi
mr >/dev/null
if [[ ! -f "$MR_TD/victim" ]]; then pass "通常実行: 副作用が発生する"
else fail "通常実行: 副作用が発生する" "ファイルが残っている"; fi

newenv
mkstep 10-dryhist.sh 'step_name="要対応"
step_run() { add_action x "やること" }'
mr --dry-run >/dev/null
if [[ ! -s "$MYROUTINE_LOG_DIR/history.tsv" ]]; then pass "dry-run: 履歴を汚さない"
else fail "dry-run: 履歴を汚さない" "history.tsv に記録された"; fi

# ─────────────────────────────────────────────────────────────────
group "step_destructive — 破壊的操作のゲート"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-dest.sh 'step_name="破壊的"
step_destructive=true
step_run() { ok "実行された" }'
out=$(mr --dry-run)
assert_contains "$out" "破壊的ステップのため実行しません" "destructive: dry-run では実行しない"
assert_not_contains "$out" "実行された"                   "destructive: dry-run で step_run が動かない"

out=$(mr)
assert_contains "$out" "実行を見送りました" "destructive: --yes 無し非対話では実行しない"
assert_not_contains "$out" "実行された"      "destructive: 確認できない環境で step_run が動かない"
assert_eq "$(mr_rc)" "0" "destructive: 見送りは失敗ではない"

out=$(mr --yes)
assert_contains "$out" "実行された" "destructive: --yes で実行される"

# ─────────────────────────────────────────────────────────────────
group "step_requires — 前提条件と skip"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-req.sh 'step_name="前提あり"
step_requires=(this-command-does-not-exist-9c3f)
step_run() { err "実行されてしまった"; return 1 }'
mkstep 20-after.sh 'step_name="後続"
step_run() { ok "後続が実行された" }'
out=$(mr)
assert_contains "$out" "skip: 前提が足りません"  "requires: 欠落時は skip になる"
assert_contains "$out" "コマンド: this-command"  "requires: 欠けている前提を具体的に示す"
assert_not_contains "$out" "実行されてしまった"  "requires: skip 時に step_run が動かない"
assert_contains "$out" "後続が実行された"        "requires: skip でも後続は実行される"
assert_eq "$(mr_rc)" "0" "requires: skip は終了コードに影響しない"

newenv
mkstep 10-reqpath.sh 'step_name="パス前提"
step_requires_path=(/no/such/path/9c3f)
step_run() { return 1 }'
out=$(mr)
assert_contains "$out" "パス: /no/such/path" "requires: パスの欠落も skip 理由に出る"

# ─────────────────────────────────────────────────────────────────
group "多重起動ロック"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-slow.sh 'step_name="遅い"
step_run() { sleep 3 }'
"$MR" </dev/null >/dev/null 2>&1 &
bg_pid=$!
sleep 1
out=$("$MR" </dev/null 2>&1)
rc=$?
assert_contains "$out" "既に実行中です" "lock: 2 つ目の実行が中止される"
assert_eq "$rc" "1" "lock: 2 つ目の終了コードが 1"
wait $bg_pid 2>/dev/null

# 残留ロック（プロセスが存在しない pid）の回収
newenv
mkstep 10-quick.sh 'step_name="速い"
step_run() { ok done }'
mkdir -p "$MYROUTINE_LOG_DIR/lock.d"
print -r -- "999999" > "$MYROUTINE_LOG_DIR/lock.d/pid"
out=$("$MR" </dev/null 2>&1)
assert_contains "$out" "残留ロックを回収します" "lock: stale ロックを回収する"
assert_contains "$out" "✓ 要対応なし"           "lock: 回収後に実行できる"

# ─────────────────────────────────────────────────────────────────
group "ステップタイムアウト"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-hang.sh 'step_name="ハングする"
step_critical=true
step_timeout=1
step_run() { sleep 30 }'
mkstep 20-after.sh 'step_name="後続"
step_run() { ok "後続が実行された" }'
t0=$(date +%s)
out=$(mr)
t1=$(date +%s)
assert_contains "$out" "タイムアウトしました" "timeout: タイムアウトを検出する"
assert_contains "$out" "後続が実行された"     "timeout: タイムアウト後も後続が実行される"
if (( t1 - t0 < 15 )); then pass "timeout: 指定秒で打ち切られる ($(( t1 - t0 ))s)"
else fail "timeout: 指定秒で打ち切られる" "$(( t1 - t0 ))s かかった"; fi
if [[ -z $(pgrep -f "sleep 30" 2>/dev/null) ]]; then pass "timeout: 子プロセスが残らない"
else fail "timeout: 子プロセスが残らない" "sleep 30 が生存している"; fi

# ─────────────────────────────────────────────────────────────────
group "劣化検知（連続日数 / NEW / 解消）"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-stale.sh 'step_name="滞留"
step_run() { add_action stuck "ずっと残っている"; add_action fresh "今日出た" }'
hf="$MYROUTINE_LOG_DIR/history.tsv"
{
  print -r -- "2026-08-09"$'\t'"09:00:00"$'\t'"stale.stuck,stale.gone"
  print -r -- "2026-08-10"$'\t'"09:00:00"$'\t'"stale.stuck,stale.gone"
  print -r -- "2026-08-11"$'\t'"09:00:00"$'\t'"stale.stuck,stale.gone"
} > "$hf"
out=$(mr)
assert_contains "$out" "(4 日連続)"      "劣化検知: 連続掲示日数を数える"
assert_contains "$out" "NEW"             "劣化検知: 新規の要対応に NEW を付ける"
assert_contains "$out" "解消: stale.gone" "劣化検知: 消えた要対応を解消として出す"

# 途切れた履歴では連続日数がリセットされる
newenv
mkstep 10-stale.sh 'step_name="滞留"
step_run() { add_action stuck "残っている" }'
{
  print -r -- "2026-08-09"$'\t'"09:00:00"$'\t'"stale.stuck"
  print -r -- "2026-08-10"$'\t'"09:00:00"$'\t'""
  print -r -- "2026-08-11"$'\t'"09:00:00"$'\t'"stale.stuck"
} > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_contains "$out" "(2 日連続)" "劣化検知: 途切れた履歴で連続日数がリセットされる"

# 部分実行は履歴を汚さない
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { add_action a "A" }'
mkstep 20-b.sh 'step_name="B"
step_run() { add_action b "B" }'
mr a >/dev/null
if [[ ! -s "$MYROUTINE_LOG_DIR/history.tsv" ]]; then pass "劣化検知: 部分実行は履歴に記録しない"
else fail "劣化検知: 部分実行は履歴に記録しない" "記録された"; fi

# ─────────────────────────────────────────────────────────────────
group "ルーティン (add_todo)"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-t.sh 'step_name="定常"
step_run() { add_todo run "/routine.progress-report を回す" }'
out=$(mr)
assert_contains "$out" "きょう回すもの 1 件" "todo: 専用セクションに出る"
assert_contains "$out" "[1] /routine.progress-report を回す" "todo: ステップ番号と本文が出る"
assert_contains "$out" "要対応なし" "todo: 要対応には混ざらない"
assert_not_contains "$out" "対応不要" "todo: 情報枠にも混ざらない"

# 定常のやることなので、何日続いても劣化として強調されない
mr >/dev/null; mr >/dev/null
out=$(mr)
assert_not_contains "$out" "日連続" "todo: 連続日数が付かない"
assert_not_contains "$out" "NEW"    "todo: NEW が付かない"
if ! grep -q "t.run" "$MYROUTINE_LOG_DIR/history.tsv"; then
  pass "todo: 履歴に記録しない"
else
  fail "todo: 履歴に記録しない" "$(cat "$MYROUTINE_LOG_DIR/history.tsv")"
fi

# 要対応が消えても todo は残る（枠が独立している）
newenv
mkstep 10-both.sh 'step_name="両方"
step_run() { add_action bad "劣化した"; add_todo run "毎日やる" }'
out=$(mr)
assert_contains "$out" "劣化した" "todo: 要対応と併存する (1)"
assert_contains "$out" "毎日やる" "todo: 要対応と併存する (2)"

json=$("$MR" --no-lock --json </dev/null 2>/dev/null)
assert_contains "$json" '"todos":[{"step":1,"id":"both.run","text":"毎日やる"}]' "todo: --json に todos として出る"

# ─────────────────────────────────────────────────────────────────
group "所要時間と --json"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-a.sh 'step_name="A"
step_run() { add_action a "やること" }'
mkstep 20-b.sh 'step_name="B"
step_run() { add_info b "報告" }'
out=$(mr)
assert_contains "$out" "所要 " "所要時間の合計が表示される"

json=$("$MR" --no-lock --json </dev/null 2>/dev/null)
if print -r -- "$json" | python3 -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null; then
  pass "--json: 妥当な JSON を出力する"
else
  fail "--json: 妥当な JSON を出力する" "$json"
fi
assert_not_contains "$json" "━" "--json: 人間向けの装飾を stdout に混ぜない"
assert_contains "$json" '"status":"ok"'      "--json: ステップの状態を含む"
assert_contains "$json" '"duration_ms"'      "--json: 所要時間を含む"
assert_contains "$json" '"id":"a.a"'         "--json: 要対応 id を slug 付きで含む"
assert_contains "$json" '"streak_days"'      "--json: 連続日数を含む"

# ─────────────────────────────────────────────────────────────────
group "ログ（追記と ANSI 除去）"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok done }'
NO_COLOR="" "$MR" --no-lock </dev/null >/dev/null 2>&1
NO_COLOR="" "$MR" --no-lock </dev/null >/dev/null 2>&1
logf=("$MYROUTINE_LOG_DIR"/[0-9]*.txt(N))
assert_eq "${#logf}" "1" "ログ: 日付ごとに 1 ファイル"
assert_eq "$(grep -c '^=== myroutine' "${logf[1]}")" "2" "ログ: 同日 2 回目の実行で追記される"
assert_eq "$(LC_ALL=C grep -c $'\e' "${logf[1]}")" "0" "ログ: ANSI エスケープが除去される"

# ─────────────────────────────────────────────────────────────────
group "abort_on_fail"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-gate.sh 'step_name="ゲート"
step_abort_on_fail=true
step_run() { return 1 }'
mkstep 20-after.sh 'step_name="後続"
step_run() { ok "後続が実行された" }'
out=$(mr)
assert_contains "$out" "中断します"                "abort: tty 不在なら中断側に倒れる"
assert_not_contains "$out" "後続が実行された"      "abort: 中断後は後続を実行しない"
assert_contains "$out" "の失敗により中断した"      "abort: 中断を要対応として記録する"
assert_eq "$(mr_rc)" "1" "abort: 中断は終了コード 1"

out=$(mr --yes)
assert_contains "$out" "後続が実行された" "abort: --yes なら続行する"

# ─────────────────────────────────────────────────────────────────
group "引数解釈"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok A }'
mkstep 20-b.sh 'step_name="B"
step_run() { ok B }'
mkstep 30-c.sh 'step_name="C"
step_run() { ok C }'

out=$(mr a,c)
assert_contains "$out" "[1/3] A" "引数: カンマ区切りで部分実行できる"
assert_contains "$out" "[3/3] C" "引数: カンマ区切りの 2 つ目も実行される"
assert_not_contains "$out" "[2/3] B" "引数: 指定していないステップは実行されない"

out=$(mr a c)
assert_contains "$out" "[1/3] A" "引数: スペース区切りの 1 つ目が実行される"
assert_contains "$out" "[3/3] C" "引数: スペース区切りの 2 つ目も黙って捨てられない"

out=$(mr c,a)
first=${out%%C*}
if [[ $first != *"A"* ]]; then pass "引数: 指定した順で実行される"
else fail "引数: 指定した順で実行される" "$out"; fi

out=$(mr foo)
assert_contains "$out" "存在しないステップです: foo" "引数: 存在しない slug をエラーにする"
assert_eq "$(mr_rc foo)" "1" "引数: 存在しない slug の終了コードが 1"

out=$(mr a abc)
assert_contains "$out" "存在しないステップです: abc" "引数: 混在した存在しない slug も見逃さない"
assert_not_contains "$out" "[1/3] A" "引数: 存在しない slug があれば 1 つも実行しない"

out=$(mr 1)
assert_contains "$out" "番号ではなく slug で指定してください: 1" "引数: 数字を渡したら slug で指定するよう案内する"
assert_contains "$out" "myroutine ls" "引数: 数字エラーで ls で slug を確認できると案内する"
assert_eq "$(mr_rc 1)" "1" "引数: 数字指定の終了コードが 1"

out=$(mr a,2)
assert_contains "$out" "番号ではなく slug で指定してください: 2" "引数: slug に混ざった数字も見逃さない"

out=$(mr 99)
assert_contains "$out" "番号ではなく slug で指定してください: 99" "引数: 範囲外の数字も同じエラーにする"

out=$("$MR" --bogus </dev/null 2>&1)
assert_contains "$out" "不明なオプション" "引数: 不明なオプションをエラーにする"

out=$("$MR" --timeout abc </dev/null 2>&1)
assert_contains "$out" "--timeout には秒数" "引数: --timeout の値を検証する"

# ─────────────────────────────────────────────────────────────────
group "終了コードの衝突（内部事情を rc で伝えない）"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-rc124.sh 'step_name="124 を返す"
step_critical=true
step_run() { return 124 }'
out=$(mr)
assert_contains "$out" "に失敗しました (rc=124)" "rc 衝突: 124 を返しても素直に失敗として扱う"
assert_not_contains "$out" "タイムアウトしました" "rc 衝突: 124 をタイムアウトと誤表示しない"

newenv
mkstep 10-rc10.sh 'step_name="10 を返す"
step_critical=true
step_run() { return 10 }'
out=$(mr)
assert_not_contains "$out" "読み込みに失敗" "rc 衝突: 10 を読み込み失敗と誤表示しない"

newenv
mkstep 10-rc11.sh 'step_name="11 を返す"
step_critical=true
step_run() { return 11 }'
out=$(mr)
assert_not_contains "$out" "step_run が定義されていません" "rc 衝突: 11 を関数未定義と誤表示しない"

# ─────────────────────────────────────────────────────────────────
group "中断・シグナル時の一貫性"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-gate.sh 'step_name="ゲート"
step_abort_on_fail=true
step_run() { add_action stuck "残るはずの要対応"; return 1 }'
mkstep 20-after.sh 'step_name="後続"
step_run() { add_action other "後続の要対応" }'
mr >/dev/null
if [[ ! -s "$MYROUTINE_LOG_DIR/history.tsv" ]]; then pass "中断: 未完走の実行を履歴に記録しない"
else fail "中断: 未完走の実行を履歴に記録しない" "$(cat "$MYROUTINE_LOG_DIR/history.tsv")"; fi

newenv
mkstep 10-slow.sh 'step_name="遅い"
step_run() { sleep 20 }'
mkstep 20-after.sh 'step_name="後続"
step_run() { ok "後続が実行された" }'
"$MR" </dev/null >"$MR_TD/sig.out" 2>&1 &
sig_pid=$!
sleep 1
kill -TERM $sig_pid 2>/dev/null
sig_rc=0
wait $sig_pid 2>/dev/null || sig_rc=$?
sleep 1
if (( sig_rc != 0 )); then pass "シグナル: TERM で非 0 終了する (rc=${sig_rc})"
else fail "シグナル: TERM で非 0 終了する" "rc=0 で正常終了した"; fi
assert_not_contains "$(cat "$MR_TD/sig.out")" "後続が実行された" "シグナル: 受信後に後続を実行しない"
if [[ ! -d "$MYROUTINE_LOG_DIR/lock.d" ]]; then pass "シグナル: ロックを解放する"
else fail "シグナル: ロックを解放する" "lock.d が残っている"; fi
if [[ -z $(pgrep -f "sleep 20" 2>/dev/null) ]]; then pass "シグナル: 実行中のステップごと落とす"
else fail "シグナル: 実行中のステップごと落とす" "sleep 20 が生存している"; fi

# ─────────────────────────────────────────────────────────────────
group "履歴の日付集計"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-s.sh 'step_name="滞留"
step_run() { add_action stuck "残っている" }'
# 同じ日に 2 行あり、1 回目は要対応ゼロ。2 行を別々の実行日として数えてはいけない。
{
  print -r -- "2026-08-11"$'\t'"08:00:00"$'\t'""
  print -r -- "2026-08-11"$'\t'"18:00:00"$'\t'"s.stuck"
  print -r -- "2026-08-12"$'\t'"09:00:00"$'\t'"s.stuck"
} > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_contains "$out" "(3 日連続)"     "履歴: 同日の複数行を 1 日として数える"
assert_not_contains "$out" "(4 日連続)" "履歴: 同日の複数行で日数が水増しされない"

# ─────────────────────────────────────────────────────────────────
group "評価していないステップを解消扱いしない"
# ─────────────────────────────────────────────────────────────────

# 前提コマンドが（一時的にでも）無くて skip されたステップの要対応は
# 「今回出なかった」のではなく「見ていない」。解消にしてはいけない。
newenv
mkstep 10-gone.sh 'step_name="前提なし"
step_requires=(this-command-does-not-exist-7b21)
step_run() { add_action stuck "見ていない要対応" }'
mkstep 20-live.sh 'step_name="動く"
step_run() { add_action other "今日の要対応" }'
print -r -- "2026-08-12"$'\t'"09:00:00"$'\t'"gone.stuck,live.other"$'\t'"gone,live" \
  > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_not_contains "$out" "解消: gone.stuck" "skip: 評価していないステップを解消扱いしない"

# 逆に、評価したうえで消えたものは解消として出る
newenv
mkstep 10-live.sh 'step_name="動く"
step_run() { ok done }'
print -r -- "2026-08-12"$'\t'"09:00:00"$'\t'"live.stuck"$'\t'"live" \
  > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_contains "$out" "解消: live.stuck" "skip: 評価したうえで消えたものは解消として出る"

# 評価していない日は連続日数を切りも伸ばしもしない
newenv
mkstep 10-s.sh 'step_name="滞留"
step_run() { add_action stuck "残っている" }'
{
  print -r -- "2026-08-10"$'\t'"09:00:00"$'\t'"s.stuck"$'\t'"s"
  print -r -- "2026-08-11"$'\t'"09:00:00"$'\t'""$'\t'"other"
  print -r -- "2026-08-12"$'\t'"09:00:00"$'\t'"s.stuck"$'\t'"s"
} > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_contains "$out" "(3 日連続)" "履歴: 評価していない日は連続を切らない（その日は数に入らない）"

# 部分実行では解消判定そのものを行わない
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { add_action a "A" }'
mkstep 20-b.sh 'step_name="B"
step_run() { add_action b "B" }'
print -r -- "2026-08-12"$'\t'"09:00:00"$'\t'"a.a,b.b"$'\t'"a,b" \
  > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr a)
assert_not_contains "$out" "解消: b.b" "部分実行: 未選択ステップを解消扱いしない"

# ─────────────────────────────────────────────────────────────────
group "チャネルの頑健性"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-multi.sh 'step_name="複数行"
step_run() { add_action multi "1 行目
2 行目	タブ入り" }'
out=$(mr)
assert_contains "$out" "1 行目"        "チャネル: 複数行本文の 1 行目が残る"
assert_contains "$out" "2 行目"        "チャネル: 複数行本文の 2 行目も欠落しない"
assert_contains "$out" "⚠ 要対応 1 件" "チャネル: 複数行本文が複数レコードに割れない"

newenv
mkstep 10-esc2.sh 'step_name="リテラルのエスケープ列"
step_run() { add_action lit "literal\nend と literal\ttab" }'
out=$(mr)
assert_contains "$out" 'literal\nend' "チャネル: 本文中のリテラル \\n を改行に化けさせない"
assert_contains "$out" 'literal\ttab' "チャネル: 本文中のリテラル \\t をタブに化けさせない"

newenv
mkstep '10-bad,slug.sh' 'step_name="不正 slug"
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "slug に使えるのは" "slug: 履歴の区切り文字を含む slug を拒否する"

# slug にドットがあると action id "<slug>.<id>" から slug を復元できず、
# 連続日数・NEW・解消の判定対象から漏れる
newenv
mkstep '10-dot.slug.sh' 'step_name="ドット付き slug"
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "slug に使えるのは" "slug: ドットを含む slug を拒否する"

# ─────────────────────────────────────────────────────────────────
group "評価しきれなかったステップの扱い"
# ─────────────────────────────────────────────────────────────────

# 失敗したステップは要対応の有無を確認しきれていない。skip と同じく
# 「見ていない」側に置かないと、判定前に落ちただけで解消と誤表示される。
newenv
mkstep 10-broken.sh 'step_name="途中で落ちる"
step_critical=true
step_run() { return 1 }'
print -r -- "2026-08-12"$'\t'"09:00:00"$'\t'"broken.stuck"$'\t'"broken" \
  > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_not_contains "$out" "解消: broken.stuck" "失敗: 評価しきれなかったステップを解消扱いしない"

newenv
mkstep 10-hang2.sh 'step_name="打ち切られる"
step_timeout=1
step_run() { sleep 20 }'
print -r -- "2026-08-12"$'\t'"09:00:00"$'\t'"hang2.stuck"$'\t'"hang2" \
  > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_not_contains "$out" "解消: hang2.stuck" "タイムアウト: 評価しきれなかったステップを解消扱いしない"

# ─────────────────────────────────────────────────────────────────
group "同日 2 回目の NEW / 解消"
# ─────────────────────────────────────────────────────────────────

# NEW / 解消 は「前回の実行」との比較。当日の先行実行を一律に無視すると、
# 朝に出た要対応が午後も NEW と表示され、午後に消えても解消が出ない。
newenv
mkstep 10-s.sh 'step_name="滞留"
step_run() { add_action stuck "残っている" }'
print -r -- "$(date +%Y-%m-%d)"$'\t'"08:00:00"$'\t'"s.stuck"$'\t'"s" \
  > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_not_contains "$out" "NEW" "同日 2 回目: 朝に出ていた要対応を NEW にしない"

newenv
mkstep 10-s.sh 'step_name="解消済み"
step_run() { ok done }'
print -r -- "$(date +%Y-%m-%d)"$'\t'"08:00:00"$'\t'"s.gone"$'\t'"s" \
  > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_contains "$out" "解消: s.gone" "同日 2 回目: 午後に消えたものを解消として出す"

# ─────────────────────────────────────────────────────────────────
group "残留ロックの原子的な回収"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-slow.sh "step_name=\"遅い\"
step_run() { print -r -- ran >> \"$MR_TD/ran.txt\"; sleep 2 }"
mkdir -p "$MYROUTINE_LOG_DIR/lock.d"
print -r -- "999999" > "$MYROUTINE_LOG_DIR/lock.d/pid"
# 残留ロックがある状態で 2 プロセスが同時に起動しても、実行できるのは 1 つだけ
"$MR" </dev/null >/dev/null 2>&1 &
p1=$!
"$MR" </dev/null >/dev/null 2>&1 &
p2=$!
wait $p1 2>/dev/null
wait $p2 2>/dev/null
ran=$(grep -c ran "$MR_TD/ran.txt" 2>/dev/null || print -r -- 0)
assert_eq "$ran" "1" "lock: 残留ロックの同時回収でも実行は 1 つだけ"

# ─────────────────────────────────────────────────────────────────
group "履歴の所要時間 (5 列目)"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-fast.sh 'step_name="速い"
step_run() { : }'
mkstep 20-slow.sh 'step_name="遅い"
step_requires=(this-command-does-not-exist)
step_run() { : }'
mr >/dev/null 2>&1
hline=$(tail -1 "$MYROUTINE_LOG_DIR/history.tsv")
if [[ $hline == *"	fast="[0-9]* ]]; then pass "履歴: 5 列目に slug=ms を記録する"
else fail "履歴: 5 列目に slug=ms を記録する" "$hline"; fi
if [[ $hline != *slow=* ]]; then pass "履歴: 実行に入らなかったステップは載せない"
else fail "履歴: 実行に入らなかったステップは載せない" "$hline"; fi

# 5 列目が無い旧形式の履歴を読んでも劣化検知が動くこと
newenv
mkstep 10-stale.sh 'step_name="滞留"
step_run() { add_action stuck "ずっと残っている" }'
{
  print -r -- "2026-08-09"$'\t'"09:00:00"$'\t'"stale.stuck"$'\t'"stale"
} > "$MYROUTINE_LOG_DIR/history.tsv"
out=$(mr)
assert_contains "$out" "(2 日連続)" "履歴: 5 列目が無い旧形式も読める"

# ─────────────────────────────────────────────────────────────────
group "実行間隔 (step_cadence)"
# ─────────────────────────────────────────────────────────────────

# 2026-08-19 は火曜、2026-08-21 は金曜。テストは MYROUTINE_NOW_EPOCH で「今」を固定する。
TUE=$(zsh -c 'zmodload zsh/datetime; strftime -r %Y-%m-%d 2026-08-19')
FRI=$(zsh -c 'zmodload zsh/datetime; strftime -r %Y-%m-%d 2026-08-21')

stamp() { mkdir -p "$MYROUTINE_LOG_DIR/last-run"; print -r -- "$2" > "$MYROUTINE_LOG_DIR/last-run/$1" }

newenv
mkstep 10-weekly.sh 'step_name="週次"
step_cadence=weekly
step_run() { print -r -- "週次が走った" }'
out=$(MYROUTINE_NOW_EPOCH=$TUE mr)
assert_contains "$out" "週次が走った" "cadence: 前回実行が無ければ初回として実行する"
assert_eq "$(<$MYROUTINE_LOG_DIR/last-run/weekly)" "2026-08-19" "cadence: 成功したら前回実行日を記録する"

out=$(MYROUTINE_NOW_EPOCH=$TUE mr)
assert_contains "$out" "実行間隔を満たしていません" "cadence: 同日 2 回目は skip する"
assert_not_contains "$out" "週次が走った" "cadence: skip したステップは実行されない"

stamp weekly 2026-08-12
out=$(MYROUTINE_NOW_EPOCH=$TUE mr)
assert_contains "$out" "週次が走った" "cadence: weekly は 7 日経過で実行する"

stamp weekly 2026-08-14
out=$(MYROUTINE_NOW_EPOCH=$TUE mr)
assert_not_contains "$out" "週次が走った" "cadence: weekly は 7 日未満なら skip する"

out=$(MYROUTINE_NOW_EPOCH=$TUE mr --force-cadence)
assert_contains "$out" "週次が走った" "cadence: --force-cadence で間隔判定を無視する"

out=$(MYROUTINE_NOW_EPOCH=$TUE mr weekly)
assert_contains "$out" "週次が走った" "cadence: 番号を指定した実行は間隔判定をしない"

newenv
mkstep 10-dry.sh 'step_name="dry"
step_cadence=weekly
step_run() { print -r -- ok }'
MYROUTINE_NOW_EPOCH=$TUE mr -n >/dev/null
if [[ -f "$MYROUTINE_LOG_DIR/last-run/dry" ]]; then
  fail "cadence: dry-run は前回実行日を進めない" "$(<$MYROUTINE_LOG_DIR/last-run/dry)"
else
  pass "cadence: dry-run は前回実行日を進めない"
fi

newenv
mkstep 10-bi.sh 'step_name="隔週金曜"
step_cadence=biweekly
step_cadence_weekday=fri
step_run() { print -r -- "隔週が走った" }'
stamp bi 2026-08-07
out=$(MYROUTINE_NOW_EPOCH=$TUE mr)
assert_contains "$out" "金曜のみ" "cadence: 曜日指定は間隔を満たしていても他の曜日を skip する"
assert_not_contains "$out" "隔週が走った" "cadence: 曜日が違う日は実行されない"

out=$(MYROUTINE_NOW_EPOCH=$FRI mr)
assert_contains "$out" "隔週が走った" "cadence: 指定曜日かつ 14 日経過で実行する"

stamp bi 2026-08-14
out=$(MYROUTINE_NOW_EPOCH=$FRI mr)
assert_not_contains "$out" "隔週が走った" "cadence: biweekly は 7 日後の同じ曜日では実行しない"

newenv
mkstep 10-monthly.sh 'step_name="月次"
step_cadence=monthly
step_run() { print -r -- "月次が走った" }'
stamp monthly 2026-08-01
out=$(MYROUTINE_NOW_EPOCH=$TUE mr)
assert_not_contains "$out" "月次が走った" "cadence: monthly は同じ月なら skip する"
stamp monthly 2026-07-31
out=$(MYROUTINE_NOW_EPOCH=$TUE mr)
assert_contains "$out" "月次が走った" "cadence: monthly は月が変わったら実行する"

newenv
mkstep 10-bad.sh 'step_name="不正"
step_cadence=fortnightly
step_run() { : }'
out=$(mr)
assert_contains "$out" "step_cadence に使えるのは" "cadence: 不正な値をエラーにする"

newenv
mkstep 10-badwd.sh 'step_name="不正曜日"
step_cadence=weekly
step_cadence_weekday=funday
step_run() { : }'
out=$(mr)
assert_contains "$out" "step_cadence_weekday に使えるのは" "cadence: 不正な曜日をエラーにする"

newenv
mkstep 10-mark.sh 'step_name="表示"
step_cadence=biweekly
step_cadence_weekday=fri
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "(biweekly/fri)" "cadence: ls に間隔と曜日が出る"

# skip されたステップの要対応は「見ていない」ので解消扱いにしない
newenv
mkstep 10-act.sh 'step_name="要対応を出す"
step_cadence=weekly
step_run() { add_action pending "残っている" }'
MYROUTINE_NOW_EPOCH=$TUE mr >/dev/null
out=$(MYROUTINE_NOW_EPOCH=$TUE mr)
assert_not_contains "$out" "解消" "cadence: skip した回で前回の要対応が解消扱いにならない"

# ─────────────────────────────────────────────────────────────────
group "JSON のエスケープ"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-esc.sh 'step_name="制御文字"
step_run() { add_action esc "色付き$(printf "\033")[31m出力$(printf "\010")と\"引用符\"と\\バックスラッシュ" }'
json=$("$MR" --no-lock --json </dev/null 2>/dev/null)
if print -r -- "$json" | python3 -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null; then
  pass "JSON: 制御文字を含む本文でも妥当な JSON"
else
  fail "JSON: 制御文字を含む本文でも妥当な JSON" "$json"
fi


# ─────────────────────────────────────────────────────────────────
group "並列実行 (step_parallel)"
# ─────────────────────────────────────────────────────────────────

# 実測で並列性を確認する。逐次なら 2 秒以上かかる組を 1.8 秒未満で終える。
newenv
mkstep 10-p1.sh 'step_name="並列1"
step_parallel=true
step_run() { sleep 1; ok done1 }'
mkstep 20-p2.sh 'step_name="並列2"
step_parallel=true
step_run() { sleep 1; ok done2 }'
t0=$EPOCHREALTIME
out=$(mr)
elapsed=$(( EPOCHREALTIME - t0 ))
if (( elapsed < 1.8 )); then
  pass "parallel: 連続する parallel ステップが同時に走る (${elapsed}s)"
else
  fail "parallel: 連続する parallel ステップが同時に走る" "${elapsed}s かかった（逐次のまま）"
fi
assert_contains "$out" "done1" "parallel: 片方の出力が回収される"
assert_contains "$out" "done2" "parallel: もう片方の出力も回収される"

# 出力は完了順ではなくステップ番号順。遅い方を先番号に置いて確かめる。
newenv
mkstep 10-slow.sh 'step_name="遅い方"
step_parallel=true
step_run() { sleep 0.6; ok AAAMARK }'
mkstep 20-fast.sh 'step_name="速い方"
step_parallel=true
step_run() { ok BBBMARK }'
out=$(mr)
if (( ${#${out%%AAAMARK*}} < ${#${out%%BBBMARK*}} )); then
  pass "parallel: 出力は完了順ではなくステップ番号順"
else
  fail "parallel: 出力は完了順ではなくステップ番号順" "$out"
fi

# 要対応・情報は両方のステップから集約される
newenv
mkstep 10-pa.sh 'step_name="要対応A"
step_parallel=true
step_run() { add_action left "左の要対応" }'
mkstep 20-pb.sh 'step_name="要対応B"
step_parallel=true
step_run() { add_action right "右の要対応"; add_info note "情報" }'
out=$(mr)
assert_contains "$out" "左の要対応" "parallel: 要対応が両方集約される (1)"
assert_contains "$out" "右の要対応" "parallel: 要対応が両方集約される (2)"
assert_contains "$out" "情報" "parallel: 情報も集約される"

# グループは逐次ステップを跨がない
newenv
mkstep 10-g1.sh 'step_name="P前半"
step_parallel=true
step_run() { ok first }'
mkstep 20-mid.sh 'step_name="逐次"
step_run() { ok middle }'
mkstep 30-g2.sh 'step_name="P後半"
step_parallel=true
step_run() { ok last }'
out=$(mr)
if (( ${#${out%%first*}} < ${#${out%%middle*}} && ${#${out%%middle*}} < ${#${out%%last*}} )); then
  pass "parallel: グループは逐次ステップを跨がない"
else
  fail "parallel: グループは逐次ステップを跨がない" "$out"
fi

# 失敗・critical の扱いは逐次と同じ
newenv
mkstep 10-pf.sh 'step_name="並列で失敗"
step_parallel=true
step_critical=true
step_run() { err "わざと失敗"; return 7 }'
mkstep 20-pok.sh 'step_name="並列で成功"
step_parallel=true
step_run() { ok fine }'
out=$(mr)
assert_contains "$out" "rc=7" "parallel: 失敗の rc が記録される"
assert_contains "$out" "critical" "parallel: critical の失敗が要対応に載る"
assert_contains "$out" "fine" "parallel: 片方が失敗しても他方は完走する"
assert_eq "$(mr_rc)" "1" "parallel: critical の失敗で終了コードが 1"

# タイムアウトはステップ単位で効く
newenv
mkstep 10-pto.sh 'step_name="並列でタイムアウト"
step_parallel=true
step_timeout=1
step_run() { sleep 30 }'
mkstep 20-pquick.sh 'step_name="並列で即完了"
step_parallel=true
step_run() { ok quick }'
out=$(mr)
assert_contains "$out" "タイムアウト" "parallel: タイムアウトがステップ単位で効く"
assert_contains "$out" "quick" "parallel: 他方はタイムアウトに巻き込まれない"

# step_verify は並列でも step_run より優先される
newenv
mkstep 10-pv.sh 'step_name="並列で検証失敗"
step_parallel=true
step_run() { return 0 }
step_verify() { return 5 }'
mkstep 20-pv2.sh 'step_name="並列の相方"
step_parallel=true
step_run() { ok mate }'
out=$(mr)
assert_contains "$out" "step_verify が失敗" "parallel: step_verify が優先される"

# 前提不足の skip はグループ内でも効き、他方の実行を止めない
newenv
mkstep 10-pskip.sh 'step_name="前提なし"
step_parallel=true
step_requires=(mr-no-such-command-xyz)
step_run() { ok never }'
mkstep 20-prun.sh 'step_name="走る方"
step_parallel=true
step_run() { ok ran }'
out=$(mr)
assert_contains "$out" "前提が足りません" "parallel: 前提不足の skip がグループ内で効く"
assert_not_contains "$out" "never" "parallel: skip したステップは実行されない"
assert_contains "$out" "ran" "parallel: skip は他方の実行を止めない"

# 確認を求めるステップは中止側に倒れる（プロンプトの混線を避ける）
newenv
mkstep 10-pc.sh 'step_name="確認を求める"
step_parallel=true
step_run() { if confirm "やる?"; then ok yes; else ok no; fi }'
mkstep 20-pc2.sh 'step_name="相方"
step_parallel=true
step_run() { ok mate }'
out=$(mr -y)
assert_contains "$out" "並列実行中は確認を求められません" "parallel: confirm は中止側に倒れる"
assert_contains "$out" "no" "parallel: confirm は --yes でも承認しない"

# 宣言の矛盾は discover の時点で落とす
newenv
mkstep 10-pd.sh 'step_name="破壊的かつ並列"
step_parallel=true
step_destructive=true
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "step_parallel と step_destructive は併用できません" "parallel: destructive との併用をエラーにする"
assert_eq "$(mr_rc ls)" "1" "parallel: destructive との併用で終了コードが 1"

newenv
mkstep 10-pab.sh 'step_name="中断確認かつ並列"
step_parallel=true
step_abort_on_fail=true
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "step_parallel と step_abort_on_fail は併用できません" "parallel: abort_on_fail との併用をエラーにする"

newenv
mkstep 10-pi.sh 'step_name="対話かつ並列"
step_parallel=true
step_interactive=true
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "step_parallel と step_interactive は併用できません" "parallel: interactive との併用をエラーにする"
assert_eq "$(mr_rc ls)" "1" "parallel: interactive との併用で終了コードが 1"

# 一覧に印が出る
newenv
mkstep 10-pl.sh 'step_name="並列ステップ"
step_parallel=true
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "(parallel)" "parallel: ls に印が出る"

# 同時実行数 1 でも結果は変わらない（逐次との等価性）
newenv
mkstep 10-pm1.sh 'step_name="上限1のA"
step_parallel=true
step_run() { add_action a "A の要対応" }'
mkstep 20-pm2.sh 'step_name="上限1のB"
step_parallel=true
step_run() { add_action b "B の要対応" }'
out=$(MYROUTINE_PARALLEL_MAX=1 mr)
assert_contains "$out" "A の要対応" "parallel: MYROUTINE_PARALLEL_MAX=1 でも全ステップを実行する (1)"
assert_contains "$out" "B の要対応" "parallel: MYROUTINE_PARALLEL_MAX=1 でも全ステップを実行する (2)"

# 指定の実行でも並列にまとまる
newenv
mkstep 10-ps1.sh 'step_name="指定1"
step_parallel=true
step_run() { sleep 1; ok s1 }'
mkstep 20-ps2.sh 'step_name="指定2"
step_parallel=true
step_run() { sleep 1; ok s2 }'
mkstep 30-ps3.sh 'step_name="指定3"
step_run() { ok s3 }'
t0=$EPOCHREALTIME
out=$(mr ps1,ps2)
elapsed=$(( EPOCHREALTIME - t0 ))
if (( elapsed < 1.8 )); then
  pass "parallel: 指定の実行でも並列にまとまる (${elapsed}s)"
else
  fail "parallel: 指定の実行でも並列にまとまる" "${elapsed}s かかった"
fi
assert_not_contains "$out" "s3" "parallel: 指定の範囲外は実行しない"

# dry-run は並列でも副作用を出さない
newenv
mkstep 10-pdr.sh 'step_name="dry-run A"
step_parallel=true
step_run() { run touch "$MR_TD/side-a" }'
mkstep 20-pdr2.sh 'step_name="dry-run B"
step_parallel=true
step_run() { run touch "$MR_TD/side-b" }'
out=$(mr -n)
if [[ ! -e "$MR_TD/side-a" && ! -e "$MR_TD/side-b" ]]; then
  pass "parallel: dry-run では副作用が出ない"
else
  fail "parallel: dry-run では副作用が出ない" "ファイルが作られた"
fi


# 所要時間の表示は実時間。並列でステップ別の合計を超えることを漏らさない。
newenv
mkstep 10-w1.sh 'step_name="実時間1"
step_parallel=true
step_run() { sleep 1.2 }'
mkstep 20-w2.sh 'step_name="実時間2"
step_parallel=true
step_run() { sleep 1.2 }'
out=$(mr)
assert_contains "$out" "ステップ合計" "parallel: 所要は実時間で出し、ステップ合計を添える"
if [[ $out == *"所要 1s（ステップ合計 "* ]]; then
  pass "parallel: 実時間 < ステップ合計 になる"
else
  fail "parallel: 実時間 < ステップ合計 になる" "$(print -r -- "$out" | grep 所要)"
fi

newenv
mkstep 10-seqw.sh 'step_name="逐次の実時間"
step_run() { : }'
out=$(mr)
assert_not_contains "$out" "ステップ合計" "逐次では内訳を添えない"

# ─────────────────────────────────────────────────────────────────
group "無効化 (step_disabled)"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok A }'
mkstep 20-off.sh 'step_name="無効"
step_disabled="2026-09-17: 検証用に無効化した"
step_run() { touch "'"$MR_TD"'/off-ran" }'
mkstep 30-c.sh 'step_name="C"
step_run() { ok C }'
out=$(mr)
if [[ ! -e "$MR_TD/off-ran" ]]; then
  pass "disabled: 無効ステップは実行されない"
else
  fail "disabled: 無効ステップは実行されない" "step_run が走ってファイルができていた"
fi
assert_eq "$(mr_rc)" "0" "disabled: 無効ステップがあっても全体は成功で終わる"

newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok A }'
mkstep 20-off.sh 'step_name="無効"
step_disabled="無効化した"
step_run() { : }'
mkstep 30-c.sh 'step_name="C"
step_run() { ok C }'
out=$(mr)
assert_contains "$out" "[1/2] A" "disabled: 実行総数から無効ステップを除いて数える (1)"
assert_contains "$out" "[2/2] C" "disabled: 実行総数から無効ステップを除いて数える (2)"
assert_not_contains "$out" "] 無効 ━" "disabled: 無効ステップのヘッダーは全体実行では出ない"

newenv
mkstep 10-off.sh 'step_name="無効"
step_disabled="tmux 運用をやめたため"
step_run() { : }'
out=$(mr)
assert_contains "$out" "ℹ 情報" "disabled: 情報セクションが出る"
assert_contains "$out" "⊘ 無効中: [1] 無効 — tmux 運用をやめたため" "disabled: 情報セクションに理由付きで載る"
assert_not_contains "$out" "⚠ 要対応" "disabled: 要対応セクションには載らない"
assert_contains "$out" "✓ 要対応なし" "disabled: 無効ステップだけなら要対応なしのまま"

newenv
mkstep 10-off.sh 'step_name="無効"
step_disabled="指定でも実行しない"
step_run() { touch "'"$MR_TD"'/off-ran" }'
out=$(mr off)
if [[ ! -e "$MR_TD/off-ran" ]]; then
  pass "disabled: 指定実行でも実行されない"
else
  fail "disabled: 指定実行でも実行されない" "step_run が走ってファイルができていた"
fi
assert_contains "$out" "⊘ 無効: 指定でも実行しない" "disabled: 指定実行では理由を表示する"
assert_contains "$out" "戻すには steps.d/off.sh の step_disabled を消す" "disabled: 指定実行では戻し方を表示する"

newenv
mkstep 10-bad.sh 'step_name="不正"
step_disabled=true
step_run() { : }'
out=$(mr)
assert_contains "$out" "step_disabled には無効化の理由を書いてください（true/false は不可）" "disabled: true/false はエラーにする"
assert_eq "$(mr_rc)" "1" "disabled: true/false のエラーで終了コードが 1 になる"

newenv
mkstep 10-bad.sh 'step_name="不正"
step_disabled=FALSE
step_run() { : }'
out=$(mr)
assert_contains "$out" "step_disabled には無効化の理由を書いてください（true/false は不可）" "disabled: 大文字小文字を問わず true/false を拒否する"

newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok A }'
mkstep 20-off.sh 'step_name="無効"
step_disabled="json 確認用"
step_run() { : }'
json=$("$MR" --no-lock --json </dev/null 2>/dev/null)
assert_contains "$json" '"disabled":[{"n":2,"name":"無効","slug":"off","reason":"json 確認用"}]' "disabled: --json に disabled 配列として出る"

newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok A }'
json=$("$MR" --no-lock --json </dev/null 2>/dev/null)
assert_contains "$json" '"disabled":[]' "disabled: 無効ステップが無ければ空配列になる"

newenv
mkstep 10-off1.sh 'step_name="無効1"
step_disabled="連続性確認"
step_parallel=true
step_run() { : }'
mkstep 20-off2.sh 'step_name="並列1"
step_parallel=true
step_run() { touch "'"$MR_TD"'/par-a" }'
mkstep 30-off3.sh 'step_name="並列2"
step_parallel=true
step_run() { touch "'"$MR_TD"'/par-b" }'
out=$(mr)
if [[ -e "$MR_TD/par-a" && -e "$MR_TD/par-b" ]]; then
  pass "disabled: 並列グループの前に挟まっても後続は同時に走る"
else
  fail "disabled: 並列グループの前に挟まっても後続は同時に走る" "$out"
fi

newenv
mkstep 10-clean.sh 'step_name="クリーン"
step_run() { ok done }'
out=$(mr)
assert_not_contains "$out" "⊘ 無効" "disabled: 無効ステップが 0 件なら出力は従来どおり"
assert_contains "$out" "[1/1] クリーン" "disabled: 無効ステップが 0 件なら総数は変わらない"

# ─────────────────────────────────────────────────────────────────
group "--jsonl"
# ─────────────────────────────────────────────────────────────────

# 各行が単独で妥当な JSON であることを確認するヘルパー。
assert_all_lines_valid_json() {
  local text=$1 label=$2
  local bad=0 line=""
  while IFS= read -r line; do
    [[ -n $line ]] || continue
    print -r -- "$line" | python3 -c 'import json,sys; json.load(sys.stdin)' >/dev/null 2>&1 || bad=1
  done <<< "$text"
  if (( bad == 0 )); then pass "$label"
  else fail "$label" "$text"; fi
}

# 特定の ev を持つ行だけを 1 行返す（無ければ空）
jsonl_line() {
  local text=$1 ev=$2
  print -r -- "$text" | grep -m1 "\"ev\":\"${ev}\""
}

newenv
mkstep 10-ok.sh 'step_name="成功"
step_run() { print -r -- "1 行目"; print -r -- "2 行目"; add_action a "やること"; emit_summary "2 lines printed" }'
mkstep 20-fail.sh 'step_name="失敗"
step_run() { local i=0; for (( i = 1; i <= 25; i++ )); do print -r -- "line ${i}"; done; return 1 }'
out=$(mr_stdout --jsonl)

assert_all_lines_valid_json "$out" "jsonl: 各行が単独で妥当な JSON である"
assert_not_contains "$out" "━" "jsonl: 人間向けの装飾を stdout に混ぜない"
assert_not_contains "$out" "⊘ skip" "jsonl: skip 等の整形出力も stdout に混ぜない"

run_line=$(jsonl_line "$out" run)
assert_contains "$run_line" '"mode":"all"' "jsonl: run イベントに mode が入る"
assert_contains "$run_line" '"started_at":"' "jsonl: run イベントに started_at が入る"
assert_contains "$run_line" '"total":2' "jsonl: run イベントの total が有効ステップ数と一致する"

ok_line=$(print -r -- "$out" | grep '"slug":"ok"')
assert_contains "$ok_line" '"status":"ok"' "jsonl: 成功ステップの status"
assert_contains "$ok_line" '"summary":"2 lines printed"' "jsonl: emit_summary の内容が summary に載る"
assert_contains "$ok_line" '"tail":[]' "jsonl: 成功ステップの tail は空配列"
assert_contains "$ok_line" '"pos":1' "jsonl: pos は今回実行内での通し番号 (1)"

fail_line=$(print -r -- "$out" | grep '"slug":"fail"')
assert_contains "$fail_line" '"status":"fail"' "jsonl: 失敗ステップの status"
assert_contains "$fail_line" '"tail":["line 6"' "jsonl: 失敗ステップの tail は末尾 20 行に切り詰められる"
assert_contains "$fail_line" '"line 25"]' "jsonl: 失敗ステップの tail に最終行まで含まれる"
assert_not_contains "$fail_line" '"line 5"' "jsonl: 失敗ステップの tail は先頭側を含まない（20 行より前）"
assert_contains "$fail_line" '"pos":2' "jsonl: pos は今回実行内での通し番号 (2)"

summary_line=$(jsonl_line "$out" summary)
assert_contains "$summary_line" '"wall_ms":' "jsonl: summary イベントに wall_ms が入る"
assert_contains "$summary_line" '"actions":[{"step":1' "jsonl: summary イベントの actions が --json と同じ構造"

# timeout ステータス（指摘1: timeout が成功として描画される問題の入力側）
newenv
mkstep 10-hang.sh 'step_name="ハング"
step_timeout=1
step_run() { print -r -- "始めた"; sleep 30 }'
out=$(mr_stdout --jsonl)
timeout_line=$(print -r -- "$out" | grep '"slug":"hang"')
assert_contains "$timeout_line" '"status":"timeout"' "jsonl: タイムアウトしたステップの status は timeout（ok/fail と別物）"
assert_contains "$timeout_line" '"note":"1s"' "jsonl: タイムアウトの note に制限秒数が入る"
assert_not_contains "$timeout_line" '"tail":[]' "jsonl: タイムアウトしたステップにも tail が入る"

# 部分実行での pos/total（指摘3: 分母と分子の基準が違う問題）
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok A }'
mkstep 20-b.sh 'step_name="B"
step_run() { ok B }'
mkstep 30-c.sh 'step_name="C"
step_run() { ok C }'
mkstep 40-d.sh 'step_name="D"
step_run() { ok D }'
mkstep 50-e.sh 'step_name="E"
step_run() { ok E }'
mkstep 60-f.sh 'step_name="F"
step_run() { ok F }'
mkstep 70-g.sh 'step_name="G"
step_run() { ok G }'
mkstep 80-h.sh 'step_name="H"
step_run() { ok H }'
mkstep 90-i.sh 'step_name="I"
step_run() { ok I }'
out=$(mr_stdout --jsonl e,i)
run_line=$(jsonl_line "$out" run)
assert_contains "$run_line" '"total":2' "jsonl: 部分実行の total は指定したステップ数（無効ステップ除く）"
pos_e=$(print -r -- "$out" | grep '"slug":"e"')
assert_contains "$pos_e" '"n":5' "jsonl: 部分実行でも n はステップ固有番号のまま (5)"
assert_contains "$pos_e" '"pos":1' "jsonl: 部分実行の pos は total と同じ集合で 1 から数える"
pos_i=$(print -r -- "$out" | grep '"slug":"i"')
assert_contains "$pos_i" '"n":9' "jsonl: 部分実行でも n はステップ固有番号のまま (9)"
assert_contains "$pos_i" '"pos":2' "jsonl: 部分実行の pos は total と同じ集合で数える（2番目）"

# disabled ステップの step イベント（指摘2: 即時描画で二重に出る問題の入力側）
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok A }'
mkstep 20-off.sh 'step_name="無効"
step_disabled="jsonl pos 確認用"
step_run() { : }'
out=$(mr_stdout --jsonl)
run_line=$(jsonl_line "$out" run)
assert_contains "$run_line" '"total":1' "jsonl: run の total は無効ステップを含まない"
off_line=$(print -r -- "$out" | grep '"slug":"off"')
assert_contains "$off_line" '"status":"disabled"' "jsonl: 無効ステップの status は disabled"
assert_not_contains "$off_line" '"pos"' "jsonl: disabled ステップには pos を振らない（total の分母に含まれないため）"

# 並列グループの後方が skip でも、先頭（実行するステップ）の pos は 1 になる。
# ゲート判定は group 内を並び順で先に全部済ませるため、skip は自分のゲートで
# 即座にイベントを発行する一方、実行するステップは実行完了後にまとめて発行
# される。発行順（gate 判定順）で pos を振ると、seq 上は後ろの skip が先に
# pos:1 を取ってしまい、先頭の実行ステップが pos:2 になる（今回の指摘）。
newenv
mkstep 10-prun.sh 'step_name="走る方"
step_parallel=true
step_run() { ok ran }'
mkstep 20-pskip.sh 'step_name="前提なし"
step_parallel=true
step_requires=(mr-no-such-command-xyz)
step_run() { ok never }'
out=$(mr_stdout --jsonl)
run_step_line=$(print -r -- "$out" | grep '"slug":"prun"')
skip_line=$(print -r -- "$out" | grep '"slug":"pskip"')
assert_contains "$skip_line" '"status":"skip"' "jsonl: 並列グループ後方の前提不足は skip になる"
assert_contains "$run_step_line" '"pos":1' "jsonl: 並列グループの先頭（実行するステップ）の pos は 1 (発行順の加算ではなく seq 上の位置)"
assert_contains "$skip_line" '"pos":2' "jsonl: 並列グループの後方（skip）の pos は 2 (seq 上の位置どおり)"

newenv
mkstep 10-a.sh 'step_name="A"
step_run() { ok A }'
out=$(mr_stdout --jsonl)
noemit_line=$(print -r -- "$out" | grep '"slug":"a"')
assert_contains "$noemit_line" '"summary":""' "jsonl: emit_summary を呼ばなければ summary は空文字"

# 既存の --json は構造を変えていないこと（回帰確認）
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { add_action a "やること" }'
json_out=$(mr_stdout --json)
if print -r -- "$json_out" | python3 -c 'import json,sys; json.load(sys.stdin)' >/dev/null 2>&1; then
  pass "jsonl: --json は従来どおり単独で妥当な JSON を出す"
else
  fail "jsonl: --json は従来どおり単独で妥当な JSON を出す" "$json_out"
fi
assert_contains "$json_out" '"steps":['   "jsonl: --json は steps 配列を持つ（従来どおり）"
assert_contains "$json_out" '"streak_days"' "jsonl: --json は streak_days を持つ（従来どおり）"
assert_not_contains "$json_out" '"ev":' "jsonl: --json には ev フィールドが無い（従来どおり）"

# --json / --jsonl の同時指定
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { : }'
out=$(mr --json --jsonl)
assert_contains "$out" "--json と --jsonl は併用できません" "jsonl: --json --jsonl の同時指定はエラーになる"
assert_eq "$(mr_rc --json --jsonl)" "1" "jsonl: --json --jsonl の同時指定は終了コード 1"

# myroutine last
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { : }'
out=$(mr last)
assert_contains "$out" "直近の実行記録がありません" "jsonl: last はファイル不在時にメッセージを出す"
assert_eq "$(mr_rc last)" "1" "jsonl: last はファイル不在時に rc=1"

mr_stdout --jsonl >/dev/null
if [[ -f "$MYROUTINE_LOG_DIR/last-events.jsonl" ]]; then
  pass "jsonl: --jsonl 実行で last-events.jsonl が作られる"
else
  fail "jsonl: --jsonl 実行で last-events.jsonl が作られる"
fi
last_out=$(mr_stdout last)
assert_all_lines_valid_json "$last_out" "jsonl: last が返す内容も各行妥当な JSON である"
assert_contains "$last_out" '"ev":"run"' "jsonl: last の内容に run イベントが含まれる"

# ログファイルへの反映
newenv
mkstep 10-a.sh 'step_name="A"
step_run() { print -r -- "本体の出力"; : }'
mr_stdout --jsonl >/dev/null
log_file="$MYROUTINE_LOG_DIR/$(date +%Y-%m-%d).txt"
if [[ -f $log_file ]] && grep -q "本体の出力" "$log_file"; then
  pass "jsonl: ステップ本体の生出力がログファイルに残る"
else
  fail "jsonl: ステップ本体の生出力がログファイルに残る" "$(cat "$log_file" 2>/dev/null)"
fi

# ─────────────────────────────────────────────────────────────────
group "--jsonl と step_interactive"
# ─────────────────────────────────────────────────────────────────

# step_interactive=true の出力は、--jsonl 実行中でも stderr へそのまま流れる。
# 宣言していないステップの出力は stdout にも stderr にも出ず、ログにだけ残る。
newenv
mkstep 10-quiet.sh 'step_name="宣言なし"
step_run() { print -r -- "quiet の出力"; return 0 }'
mkstep 20-loud.sh 'step_name="対話ステップ"
step_interactive=true
step_run() { print -r -- "対話ステップの出力"; return 0 }'
stdout_out=$(mr_stdout --jsonl)
stderr_out=$(mr_stderr --jsonl)

assert_contains "$stderr_out" "対話ステップの出力" "interactive: 宣言したステップの出力が stderr に出る"
assert_not_contains "$stdout_out" "対話ステップの出力" "interactive: 生出力が stdout の JSONL に混ざらない"
assert_all_lines_valid_json "$stdout_out" "interactive: stdout の全行が妥当な JSON のまま保たれる"

assert_not_contains "$stderr_out" "quiet の出力" "interactive: 宣言していないステップの出力は stderr に出ない"
assert_not_contains "$stdout_out" "quiet の出力" "interactive: 宣言していないステップの出力は stdout にも出ない"

log_file="$MYROUTINE_LOG_DIR/$(date +%Y-%m-%d).txt"
if [[ -f $log_file ]] && grep -q "対話ステップの出力" "$log_file"; then
  pass "interactive: 宣言したステップの出力もログファイルに残る"
else
  fail "interactive: 宣言したステップの出力もログファイルに残る" "$(cat "$log_file" 2>/dev/null)"
fi

# 失敗時の tail は step_interactive の有無にかかわらず従来どおり JSON に載る。
newenv
mkstep 10-loud-fail.sh 'step_name="対話ステップ失敗"
step_interactive=true
step_run() { local i=0; for (( i = 1; i <= 25; i++ )); do print -r -- "line ${i}"; done; return 1 }'
out=$(mr_stdout --jsonl)
fail_line=$(print -r -- "$out" | grep '"slug":"loud-fail"')
assert_contains "$fail_line" '"status":"fail"' "interactive: 失敗ステップの status"
assert_contains "$fail_line" '"tail":["line 6"' "interactive: 失敗時の tail は従来どおり末尾 20 行に切り詰められる"
assert_contains "$fail_line" '"line 25"]' "interactive: 失敗時の tail に最終行まで含まれる"

# ─────────────────────────────────────────────────────────────────
group "--jsonl とパイプの読み手"
# ─────────────────────────────────────────────────────────────────

# 読み手が JSONL を読み切る前に終了すると、fd は壊れたパイプになる。SIGPIPE を
# ハンドラで受けるとハンドラの中から子を畳むことになり waitjobs で止まる
# （TERM も queue されるので kill -9 しか効かない）。write の戻り値で畳むこと。
newenv
mkstep 10-alpha.sh 'step_name="アルファ"
step_run() { ok run }'

# 読み手が即死する構成。ハングしていれば timeout の 10 秒を使い切る。
_t0=$EPOCHSECONDS
( "$MR" --no-lock --jsonl </dev/null 2>/dev/null | true ) &
_pipe_pid=$!
_waited=0
while (( _waited < 100 )); do
  kill -0 $_pipe_pid 2>/dev/null || break
  sleep 0.1
  (( _waited++ ))
done
if kill -0 $_pipe_pid 2>/dev/null; then
  kill -9 $_pipe_pid 2>/dev/null
  assert_eq "hang" "exit" "jsonl: 読み手が先に終わっても実行が終了する"
else
  assert_eq "exit" "exit" "jsonl: 読み手が先に終わっても実行が終了する"
fi
wait $_pipe_pid 2>/dev/null

# 読み手が生きている通常の構成は従来どおり全イベントが届く。
_out=$("$MR" --no-lock --jsonl </dev/null 2>/dev/null | cat)
assert_eq "$(print -r -- "$_out" | grep -c '"ev"')" "4" "jsonl: 読み手が生きていれば run/start/step/summary が揃う"

# ─────────────────────────────────────────────────────────────────
group "--jsonl: start イベントと終了時刻"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-first.sh 'step_name="最初"
step_run() { ok A }'
mkstep 20-slow.sh 'step_name="遅い"
step_run() { ok B }'
out=$(mr_stdout --jsonl)
start_first=$(print -r -- "$out" | grep '"ev":"start"' | grep '"slug":"first"')
assert_contains "$start_first" '"n":1' "start: n を持つ"
assert_contains "$start_first" '"pos":1' "start: pos を持つ"
assert_contains "$start_first" '"name":"最初"' "start: name を持つ"
if [[ $start_first =~ '"at":"[0-9][0-9]:[0-9][0-9]:[0-9][0-9]"' ]]; then pass "start: at は HH:MM:SS"
else fail "start: at は HH:MM:SS" "$start_first"; fi
assert_eq "$(print -r -- "$out" | grep -c '"ev":"start"')" "2" "start: 実行に入った全ステップで流れる"
# 並び: run → start → step の順で、ステップごとに start が step の直前に来る
evs=$(print -r -- "$out" | sed -E 's/.*"ev":"([a-z]+)".*/\1/' | tr '\n' ' ')
assert_eq "$evs" "run start step start step summary " "start: run → start → step → ... → summary の順で出る"
step_first=$(print -r -- "$out" | grep '"ev":"step"' | grep '"slug":"first"')
if [[ $step_first =~ '"ended_at":"[0-9][0-9]:[0-9][0-9]:[0-9][0-9]"' ]]; then pass "step: ended_at は HH:MM:SS"
else fail "step: ended_at は HH:MM:SS" "$step_first"; fi

# start は step より前に出る（ステップ実行中に start だけが見えている状態になる）
newenv
mkstep 10-long.sh 'step_name="長い"
step_run() { sleep 2; ok L }'
_ev1=$(mktemp -t mrtest-ev-XXXXXX); _TMPDIRS+=("$_ev1")
"$MR" --no-lock --jsonl </dev/null 2>/dev/null > "$_ev1" &
_pid=$!
_w=0
until grep -q '"ev":"start"' "$_ev1" 2>/dev/null || (( _w >= 30 )); do sleep 0.1; (( _w++ )); done
assert_contains "$(<$_ev1)" '"ev":"start"' "start: ステップの実行中に start が先に出ている"
assert_not_contains "$(<$_ev1)" '"ev":"step"' "start: ステップ完了前には step がまだ出ていない"
wait $_pid 2>/dev/null

# skip / 無効 / 間隔外 / 前提不足では start を出さない
newenv
mkstep 10-run.sh 'step_name="走る"
step_run() { ok R }'
mkstep 20-need.sh 'step_name="前提なし"
step_requires=(definitely-not-a-command-xyz)
step_run() { ok N }'
mkstep 30-off.sh 'step_name="無効"
step_disabled="start 確認用"
step_run() { ok O }'
mkstep 40-weekly.sh 'step_name="週次"
step_cadence=weekly
step_run() { ok W }'
mkdir -p "$MYROUTINE_LOG_DIR/last-run"
print -r -- "$(date +%Y-%m-%d)" > "$MYROUTINE_LOG_DIR/last-run/weekly"
out=$(mr_stdout --jsonl)
assert_eq "$(print -r -- "$out" | grep -c '"ev":"start"')" "1" "start: skip・無効・間隔外・前提不足のステップでは流れない"
assert_contains "$(print -r -- "$out" | grep '"ev":"start"')" '"slug":"run"' "start: 実行したステップだけに流れる"

# 並列ステップでも各ステップの実行開始時に流れる
newenv
mkstep 10-pa.sh 'step_name="並列A"
step_parallel=true
step_run() { sleep 0.5; ok a }'
mkstep 20-pb.sh 'step_name="並列B"
step_parallel=true
step_run() { sleep 0.5; ok b }'
out=$(mr_stdout --jsonl)
assert_eq "$(print -r -- "$out" | grep -c '"ev":"start"')" "2" "start: 並列ステップも各ステップで流れる"
assert_contains "$(print -r -- "$out" | grep '"ev":"start"' | grep '"slug":"pb"')" '"pos":2' "start: 並列ステップにも pos が付く"
if [[ $(print -r -- "$out" | grep '"ev":"step"' | grep -c '"ended_at":"[0-9][0-9]:[0-9][0-9]:[0-9][0-9]"') == 2 ]]; then pass "step: 並列ステップにも ended_at が付く"
else fail "step: 並列ステップにも ended_at が付く" "$out"; fi

# 指定実行でも流れる
out=$(mr_stdout --jsonl pa)
assert_eq "$(print -r -- "$out" | grep -c '"ev":"start"')" "1" "start: 指定実行でも実行したステップだけ流れる"

# ─────────────────────────────────────────────────────────────────
group "--jsonl と並列ステップの生出力"
# ─────────────────────────────────────────────────────────────────

# 並列グループは出力をまとめて cat し直す経路を持ち、そこだけ --jsonl の
# 捕獲が抜けていた。stdout は JSONL 専用、生出力はログと失敗時の tail へ。
newenv
mkstep 10-par-a.sh 'step_name="並列A"
step_parallel=true
step_run() { print -r -- "並列Aの生出力"; ok done }'
mkstep 20-par-b.sh 'step_name="並列B"
step_parallel=true
step_run() { print -r -- "並列Bの生出力"; ok done }'

_par_out=$(mr_stdout --jsonl)
_par_err=$(mr_stderr --jsonl)
assert_not_contains "$_par_out" "並列Aの生出力" "jsonl 並列: 生出力が stdout に混ざらない"
assert_not_contains "$_par_err" "並列Aの生出力" "jsonl 並列: 生出力が stderr にも出ない"
assert_contains "$(<"$MR_TD/log/$(date +%Y-%m-%d).txt")" "並列Aの生出力" "jsonl 並列: 生出力はログに残る"

# ─────────────────────────────────────────────────────────────────
group "--jsonl とステップ見出し"
# ─────────────────────────────────────────────────────────────────

# --jsonl は fd1 を stderr へ付け替えるため、人間向けの見出しがそのまま画面に
# 残る。レンダラが進捗行を出すので二重になる。ただし step_interactive は生出力が
# stderr に流れるので、どのステップの出力かを示す見出しを残す。
newenv
mkstep 10-alpha.sh 'step_name="アルファ"
step_run() { ok run }'
mkstep 20-talk.sh 'step_name="トーク"
step_interactive=true
step_run() { print -r -- "対話の出力"; ok run }'

_err=$(mr_stderr --jsonl)
assert_not_contains "$_err" "アルファ" "jsonl: 通常ステップの見出しを出さない"
assert_contains "$_err" "トーク" "jsonl: step_interactive のステップは見出しを残す"

# 非 jsonl では人間向け出力は stdout 側に出る（--jsonl だけが fd を付け替える）。
_out_plain=$(mr_stdout)
assert_contains "$_out_plain" "アルファ" "非 jsonl: 見出しは従来どおり出る"

# ─────────────────────────────────────────────────────────────────
print -r -- ""
print -r -- "════════════════════════════════════"
if (( FAIL == 0 )); then
  print -r -- "  すべて成功 (${PASS} 件)"
else
  print -ru2 -- "  失敗 ${FAIL} 件 / 成功 ${PASS} 件"
fi
print -r -- "════════════════════════════════════"
exit $(( FAIL > 0 ))
