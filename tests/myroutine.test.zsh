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

mkstep() {
  local name=$1; shift
  print -r -- "$*" > "$MYROUTINE_STEPS_DIR/$name"
}

# 標準入力を切って実行し、stdout+stderr をまとめて返す。
# tty を持たない環境で走らせることで、確認プロンプトの fail-closed も同時に検証できる。
mr() { "$MR" --no-lock "$@" </dev/null 2>&1 }
mr_rc() { "$MR" --no-lock "$@" </dev/null >/dev/null 2>&1; print -r -- $? }

# ─────────────────────────────────────────────────────────────────
group "ステップ探索とプラグイン性"
# ─────────────────────────────────────────────────────────────────

newenv
mkstep 10-alpha.sh 'step_name="アルファ"
step_run() { ok run }'
mkstep 20-bravo.sh 'step_name="ブラボー"
step_run() { ok run }'
out=$(mr ls)
assert_contains "$out" "1. アルファ" "ファイル名順に番号が振られる (1)"
assert_contains "$out" "2. ブラボー" "ファイル名順に番号が振られる (2)"

mkstep 15-charlie.sh 'step_name="チャーリー"
step_run() { ok run }'
out=$(mr ls)
assert_contains "$out" "2. チャーリー" "ファイルを置くだけでステップが増える"

mv "$MYROUTINE_STEPS_DIR/15-charlie.sh" "$MYROUTINE_STEPS_DIR/99-charlie.sh"
out=$(mr ls)
assert_contains "$out" "3. チャーリー" "リネームだけで順序が変わる"

newenv
mkstep 10-dup.sh 'step_name="A"
step_run() { : }'
mkstep 20-dup.sh 'step_name="B"
step_run() { : }'
out=$(mr ls)
assert_contains "$out" "slug が重複" "slug 重複を検出してエラーにする"

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
assert_eq "$(mr_rc 1)" "0" "verify: run 成功 / verify 成功 → 成功"

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
mr 1 >/dev/null
if [[ ! -s "$MYROUTINE_LOG_DIR/history.tsv" ]]; then pass "劣化検知: 部分実行は履歴に記録しない"
else fail "劣化検知: 部分実行は履歴に記録しない" "記録された"; fi

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

out=$(mr 1,3)
assert_contains "$out" "[1/3] A" "引数: カンマ区切りで部分実行できる"
assert_contains "$out" "[3/3] C" "引数: カンマ区切りの 2 つ目も実行される"
assert_not_contains "$out" "[2/3] B" "引数: 指定していないステップは実行されない"

out=$(mr 1 3)
assert_contains "$out" "[1/3] A" "引数: スペース区切りの 1 つ目が実行される"
assert_contains "$out" "[3/3] C" "引数: スペース区切りの 2 つ目も黙って捨てられない"

out=$(mr 3,1)
first=${out%%C*}
if [[ $first != *"A"* ]]; then pass "引数: 指定した順で実行される"
else fail "引数: 指定した順で実行される" "$out"; fi

out=$(mr foo)
assert_contains "$out" "ステップ番号ではありません: foo" "引数: 非数値をエラーにする"
assert_eq "$(mr_rc foo)" "1" "引数: 非数値の終了コードが 1"

out=$(mr 1 abc)
assert_contains "$out" "ステップ番号ではありません: abc" "引数: 混在した非数値も見逃さない"

out=$(mr 99)
assert_contains "$out" "ステップ 99 は存在しません" "引数: 範囲外をエラーにする"

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
print -r -- ""
print -r -- "════════════════════════════════════"
if (( FAIL == 0 )); then
  print -r -- "  すべて成功 (${PASS} 件)"
else
  print -ru2 -- "  失敗 ${FAIL} 件 / 成功 ${PASS} 件"
fi
print -r -- "════════════════════════════════════"
exit $(( FAIL > 0 ))
