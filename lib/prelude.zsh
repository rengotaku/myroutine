# prelude.zsh — Go の runner（cmd/myroutine）がステップを動かすときに挟む zsh の層
#
# runner のバイナリに埋め込まれ（lib/embed.go）、実行のたびに一時ディレクトリへ
# 書き出されてから使われる。ステップの書き方とプリミティブの名前・挙動は zsh 版の
# runner（bin/myroutine）と同じで、中身もそこから移したもの（ADR 0006）。
#
#   zsh prelude.zsh meta <step.sh>      ステップを source し、step_* の宣言を書き出す
#   zsh prelude.zsh requires <step.sh>  ステップを source し、欠けている前提を書き出す（ゲート時点）
#   zsh prelude.zsh exec <step.sh>      ステップを source し、step_run（と step_verify）を実行する
#
# 境界は「環境変数で渡し、fd 3 の行で返す」だけにする。ここから Go の側へ呼び戻す
# 経路（ソケット、サブコマンド呼び出し）を足さないこと。
#
# 入力（環境変数。読んだら消し、ステップが起動する外部コマンドには渡さない）:
#   MR_DRY_RUN / MR_YES / MR_IN_PARALLEL   0 / 1
#   MR_STEP_NUM / MR_STEP_SLUG             ステップの番号（order 上の位置）と slug
#   MR_NO_COLOR                            非空なら NO_COLOR=1 と同じ扱い（--json / --jsonl）
#
# 出力（fd 3、1 行 1 レコード。本文は % → %25、改行 → %0A、タブ → %09 で符号化）:
#   exec:  A / T / I   kind<TAB>num<TAB>slug.id<TAB>text   add_action / add_todo / add_info
#          S           S<TAB>num<TAB>slug.summary<TAB>text  emit_summary
#          V           V<TAB>num<TAB>run_rc<TAB>verify_rc   step_verify があるとき
#          E           E<TAB>num<TAB>source_failed | step_run_undefined
#   meta:  M           M<TAB>name<TAB>scalar<TAB>value
#                      M<TAB>name<TAB>array[<TAB>elem...]  （要素ごとに符号化）
#                      M<TAB>name<TAB>assoc
#          E           E<TAB>0<TAB>source_failed
#          D           D  （最後まで書けたことの印）
#   requires:
#          Q           Q<TAB>cmd<TAB>name / Q<TAB>path<TAB>path  （欠けている前提 1 つ）
#          E           E<TAB>num<TAB>source_failed
#          D           D
#
# 内部エラーは終了コードで伝えない（ステップ自身が 10 や 11 を返しうる）。E レコードで伝える。

emulate -R zsh
setopt pipefail
zmodload zsh/datetime 2>/dev/null

typeset -g _mr_prelude_mode=${1:-} _mr_prelude_file=${2:-}
# ステップには引数を見せない（zsh 版でも runner の引数は消費済みだった）
set --

# zsh 版と同じ既定値。ステップがシェル変数として参照することがある。
: ${MYROUTINE_STEPS_DIR:=${XDG_CONFIG_HOME:-$HOME/.config}/myroutine/steps.d}
: ${MYROUTINE_LOG_DIR:=${XDG_STATE_HOME:-$HOME/.local/state}/myroutine}
: ${MYROUTINE_TIMEOUT:=600}
: ${MYROUTINE_STREAK_WARN:=3}
: ${MYROUTINE_PARALLEL_MAX:=4}

# zsh 版のグローバル名のまま持つ。ステップが (( _MR_DRY_RUN )) のように直接読むことがある。
typeset -g _MR_DRY_RUN=${MR_DRY_RUN:-0} _MR_YES=${MR_YES:-0} _MR_IN_PARALLEL=${MR_IN_PARALLEL:-0}
typeset -g _MR_STEP_NUM=${MR_STEP_NUM:-0} _MR_STEP_SLUG=${MR_STEP_SLUG:-}
[[ -n ${MR_NO_COLOR:-} ]] && NO_COLOR=1
unset MR_DRY_RUN MR_YES MR_IN_PARALLEL MR_STEP_NUM MR_STEP_SLUG MR_NO_COLOR

if [[ -z ${NO_COLOR:-} ]]; then
  _MR_C_RESET=$'\e[0m'; _MR_C_BOLD=$'\e[1m'; _MR_C_DIM=$'\e[2m'
  _MR_C_RED=$'\e[31m'; _MR_C_GREEN=$'\e[32m'; _MR_C_YELLOW=$'\e[33m'; _MR_C_CYAN=$'\e[36m'
else
  _MR_C_RESET=""; _MR_C_BOLD=""; _MR_C_DIM=""
  _MR_C_RED=""; _MR_C_GREEN=""; _MR_C_YELLOW=""; _MR_C_CYAN=""
fi

# ─────────────────────────────────────────────────────────────────
# ステップに公開するプリミティブ（zsh 版 bin/myroutine から移したもの）
# ─────────────────────────────────────────────────────────────────

ok()   { print -r -- "  ${_MR_C_GREEN}✓${_MR_C_RESET} $1" }
warn() { print -r -- "  ${_MR_C_YELLOW}${_MR_C_BOLD}▸ $1${_MR_C_RESET}" }
err()  { print -r -- "  ${_MR_C_RED}✗ $1${_MR_C_RESET}" }
info() { print -r -- "  $1" }

# 本文の符号化。結果は REPLY に入れる（要素ごとにサブシェルを起こさないため）。
# バックスラッシュ方式（\ → \\、改行 → \n）は復号が順序依存になり、本文に
# リテラルの "\n" が含まれると二重化した後半を改行に戻してしまう。
# % を先に退避するパーセント方式なら、%25 が %0A / %09 を部分文字列として
# 含みえないため、どちらの順で戻しても一意に復元できる。
_mr_esc() {
  REPLY=${1//\%/%25}
  REPLY=${REPLY//$'\n'/%0A}
  REPLY=${REPLY//$'\t'/%09}
}

# ステップは runner と別プロセスで動くため、結果は fd 3 のチャネルへ 1 行ずつ書く。
_mr_emit() {
  local kind=$1 id=$2 text=$3
  if [[ -z $id || -z $text ]]; then
    print -ru2 -- "${kind}: id と本文の両方が必要です"
    return 1
  fi
  if [[ ! $id =~ '^[A-Za-z0-9_.-]+$' ]]; then
    print -ru2 -- "${kind}: id に使えるのは英数字と . _ - だけです: ${id}"
    return 1
  fi
  # チャネルは 1 行 1 レコード。本文に改行が混ざると 2 行目以降が別レコードとして
  # 捨てられ、サマリと JSON から欠落する。タブも列を割るのでまとめて逃がす。
  local REPLY=""
  _mr_esc "$text"
  print -r -- "$kind"$'\t'"${_MR_STEP_NUM}"$'\t'"${_MR_STEP_SLUG}.${id}"$'\t'"$REPLY" >&3
}
add_action() { _mr_emit A "$1" "$2" }
add_todo()   { _mr_emit T "$1" "$2" }
add_info()   { _mr_emit I "$1" "$2" }
# --jsonl の step イベントに載る 1 行要約。id は固定でよい（連続日数の追跡対象ではない）。
emit_summary() { _mr_emit S summary "$1" }

# 判定に使う値を fail-open で取らせないための唯一の入口。
#   local x=$(cmd) は代入の終了ステータスが local のものになり、cmd の失敗を
#   検知できない。capture は非 0 のとき変数を設定せず、そのまま非 0 を返す。
#   「失敗したら既定値でよい」ケースは -d で明示させ、|| echo "0" を書かせない。
capture() {
  local __def="" __has_def=0
  if [[ ${1:-} == -d ]]; then __def=$2; __has_def=1; shift 2; fi
  local __var=${1:-}
  if [[ -z $__var ]]; then
    print -ru2 -- "capture: 変数名が指定されていません"
    return 2
  fi
  shift
  if (( $# == 0 )); then
    print -ru2 -- "capture: コマンドが指定されていません"
    return 2
  fi
  # zsh の status / path のような特殊変数や readonly 変数への代入は、
  # 呼び出し側で検知できない致命エラーになり関数ごと即座に中断する
  # （`if ! typeset -g ...` では捕まえられない）。サブシェルで先に試して弾く。
  if ! ( typeset -g "$__var"="" ) 2>/dev/null; then
    err "変数に代入できません: ${__var}（予約変数・readonly の可能性）"
    return 2
  fi
  local __out="" __rc=0
  __out=$("$@") || __rc=$?
  if (( __rc != 0 )); then
    if (( __has_def )); then
      typeset -g "$__var"="$__def"
      return 0
    fi
    err "コマンドが失敗しました (rc=${__rc}): $*"
    return $__rc
  fi
  typeset -g "$__var"="$__out"
  return 0
}

# 確認プロンプト。tty が取れない環境 (パイプ実行・CI・スケジューラ経由) では
# 必ず中止側に倒す。dry-run では尋ねず、実行しない側の答えを返す。
# 並列グループの中では複数ステップが同時に端末を読みうる。プロンプトが混線して
# 「どのステップの確認か」が分からない答えを受け取るより、tty が取れない環境と同じく
# 中止側へ倒す。ステップが確認を求めたこと自体は見えるように警告として残す。
# 逐次ステップは runner が端末の前景を渡しているので /dev/tty を読める。
confirm() {
  local msg=${1:-"続行しますか?"}
  if (( _MR_IN_PARALLEL )); then
    warn "並列実行中は確認を求められません（中止側に倒します）: ${msg}"
    return 1
  fi
  if (( _MR_DRY_RUN )); then
    print -r -- "  ${_MR_C_DIM}[dry-run] 確認を求める: ${msg}${_MR_C_RESET}"
    return 1
  fi
  (( _MR_YES )) && return 0
  local prompt="  ${_MR_C_YELLOW}${_MR_C_BOLD}${msg} [y/N]:${_MR_C_RESET} "
  # stdout はファイル（ログ）につながっていて画面に出ない。問いは端末にも直接書く。
  print -rn -- "$prompt"
  local ans="" on_tty=0
  # /dev/tty が開けない環境ではリダイレクト自体がシェルのエラーになる。read の
  # stderr だけを潰しても漏れるため、ブロックごと黙らせたうえで中止側へ倒す。
  { print -rn -- "$prompt" >/dev/tty } 2>/dev/null && on_tty=1
  if (( on_tty )); then
    { read -k 1 ans </dev/tty } 2>/dev/null || ans="n"
    { print -r -- "" >/dev/tty } 2>/dev/null
  else
    ans="n"
  fi
  print -r -- ""
  [[ $ans == [yY] ]]
}

# 副作用は必ずこれを通す。dry-run を「ステップの礼儀」ではなく構造で担保する。
run() {
  if (( $# == 0 )); then
    print -ru2 -- "run: コマンドが指定されていません"
    return 2
  fi
  if (( _MR_DRY_RUN )); then
    print -r -- "  ${_MR_C_DIM}[dry-run] $*${_MR_C_RESET}"
    return 0
  fi
  "$@"
}

# ─────────────────────────────────────────────────────────────────
# モード
# ─────────────────────────────────────────────────────────────────

# meta: step_* に当たる変数をすべて（未知のものも含めて）書き出す。検証は Go の側で行う。
# source は関数の中で行う（zsh 版と同じく、ステップのトップレベルの typeset は
# この関数のローカルになる）。stdout は契約違反の出力が混ざらないよう捨てる。
_mr_prelude_meta() {
  local __mr_f=$1 __mr_n="" __mr_t="" __mr_e="" __mr_line="" REPLY=""
  # 環境から継いだ同名の変数を宣言と取り違えない
  unset -m 'step_*'
  source "$__mr_f" >/dev/null || { print -r -- "E"$'\t'"0"$'\t'"source_failed" >&3; return 0 }
  for __mr_n in ${(ok)parameters}; do
    [[ $__mr_n == step_* ]] || continue
    __mr_t=${parameters[$__mr_n]}
    case $__mr_t in
      association*)
        print -r -- "M"$'\t'"$__mr_n"$'\t'"assoc" >&3 ;;
      array*)
        __mr_line="M"$'\t'"$__mr_n"$'\t'"array"
        for __mr_e in "${(@P)__mr_n}"; do
          _mr_esc "$__mr_e"
          __mr_line+=$'\t'"$REPLY"
        done
        print -r -- "$__mr_line" >&3 ;;
      *)
        _mr_esc "${(P)__mr_n}"
        print -r -- "M"$'\t'"$__mr_n"$'\t'"scalar"$'\t'"$REPLY" >&3 ;;
    esac
  done
  print -r -- "D" >&3
}

# requires: ゲートの時点でステップを source してから前提を確かめる。ステップ内で
# 定義した関数やトップレベルで足した PATH も、前のステップが入れたコマンドも見える。
_mr_prelude_requires() {
  local __mr_f=$1 __mr_c="" REPLY=""
  step_requires=()
  step_requires_path=()
  source "$__mr_f" >/dev/null || { print -r -- "E"$'\t'"${_MR_STEP_NUM}"$'\t'"source_failed" >&3; return 0 }
  for __mr_c in $step_requires; do
    command -v "$__mr_c" >/dev/null 2>&1 && continue
    _mr_esc "$__mr_c"
    print -r -- "Q"$'\t'"cmd"$'\t'"$REPLY" >&3
  done
  for __mr_c in $step_requires_path; do
    [[ -e $__mr_c ]] && continue
    _mr_esc "$__mr_c"
    print -r -- "Q"$'\t'"path"$'\t'"$REPLY" >&3
  done
  print -r -- "D" >&3
}

# exec: step_run と、あれば step_verify を実行する。
# 終了コード: step_verify があればその rc、無ければ step_run の rc。
_mr_prelude_exec() {
  local __mr_f=$1
  source "$__mr_f" || { print -r -- "E"$'\t'"${_MR_STEP_NUM}"$'\t'"source_failed" >&3; exit 10 }
  (( ${+functions[step_run]} )) || { print -r -- "E"$'\t'"${_MR_STEP_NUM}"$'\t'"step_run_undefined" >&3; exit 11 }
  local run_rc=0 v_rc=0
  step_run || run_rc=$?
  if (( ${+functions[step_verify]} )); then
    step_verify || v_rc=$?
    print -r -- "V"$'\t'"${_MR_STEP_NUM}"$'\t'"$run_rc"$'\t'"$v_rc" >&3
    exit $v_rc
  fi
  exit $run_rc
}

case $_mr_prelude_mode in
  meta) _mr_prelude_meta "$_mr_prelude_file"; exit 0 ;;
  requires) _mr_prelude_requires "$_mr_prelude_file"; exit 0 ;;
  exec) _mr_prelude_exec "$_mr_prelude_file" ;;
  *)    print -ru2 -- "prelude: 不明なモード: ${_mr_prelude_mode}"; exit 2 ;;
esac
