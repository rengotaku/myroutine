# 自動で片付けを済ませ、その事実だけを事後報告する典型例。
#
# ファイルを消すので step_destructive を宣言する。これにより dry-run では
# 実行されず、--yes が無ければ本体が確認を強制する。
# 人間の対応は不要なので add_action ではなく add_info を使う。
# 要対応ボックスに混ぜると、本当に対応が必要な項目が埋もれる。
step_name="一時ファイルの掃除"
step_destructive=true

step_run() {
  local dir="${MYROUTINE_SCRATCH_DIR:-$HOME/.cache/myroutine-scratch}"
  [[ -d $dir ]] || { ok "掃除対象なし"; return 0 }

  local -a stale=($dir/*(N.md+7))
  (( ${#stale} == 0 )) && { ok "掃除対象なし"; return 0 }

  local f=""
  for f in $stale; do
    run rm -f "$f" || { err "削除に失敗: $f"; return 1 }
    ok "削除: ${f:t}"
  done
  add_info purged "7 日より古い一時ファイル ${#stale} 件を削除した"
  return 0
}
