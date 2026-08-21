# 手元のリポジトリを origin と同期する。
# step_parallel の典型例。所要時間のほとんどがネットワーク待ちで、CPU も他ステップの
# ファイルも競合しない。実行順で隣接する parallel ステップ（ここでは 60 と 70）が
# 1 グループとして同時に走る。
#
# 並列ステップは確認を求められない（confirm は中止側に倒れる）。破壊的な操作や
# 「失敗したら止まるか尋ねる」が必要なステップは逐次側に置くこと。
step_name="リポジトリ同期"
step_parallel=true
step_requires=(git)
step_timeout=120

step_run() {
  local root="${MYROUTINE_REPO_ROOT:-$HOME/repos}"
  if [[ ! -d $root ]]; then
    info "同期対象がありません ($root)"
    return 0
  fi
  local d="" failed=()
  for d in $root/*/.git(N/); do
    d=${d:h}
    run git -C "$d" fetch --quiet origin || failed+=("${d:t}")
  done
  if (( ${#failed} )); then
    err "fetch に失敗: ${(j:, :)failed}"
    add_action fetch-failed "fetch に失敗したリポジトリがある（${(j:, :)failed}）— 認証と remote を確認する"
    return 1
  fi
  ok "同期完了"
}
