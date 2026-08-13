# ネット疎通の確認。
# 最初に置くステップの典型例。ここが落ちていると後続がすべて無意味に失敗するため、
# step_abort_on_fail で「続けますか?」を尋ねる。
step_name="ネット疎通確認"
step_requires=(curl)
step_abort_on_fail=true

step_run() {
  local target="${MYROUTINE_NET_TARGET:-https://example.com}"
  if run curl -fsS --max-time 5 -o /dev/null "$target"; then
    ok "疎通あり ($target)"
    return 0
  fi
  err "疎通なし ($target)"
  add_action down "ネットワークが不通 — 接続を確認する"
  return 1
}
