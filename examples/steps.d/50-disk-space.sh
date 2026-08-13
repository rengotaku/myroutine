# ディスク空き容量の確認。
# 「失敗ではないが人間に対応してほしい」典型例。ステップ自体は成功で返し、
# add_action だけを積んで末尾の要対応サマリに載せる。
step_name="ディスク空き容量"

step_run() {
  local threshold="${MYROUTINE_DISK_THRESHOLD:-85}"
  local used
  used=$(df -P / | awk 'NR==2 {gsub(/%/, "", $5); print $5}') || {
    err "使用率を取得できませんでした"
    return 1
  }
  info "ルートボリューム使用率: ${used}%"
  if (( used >= threshold )); then
    warn "しきい値 ${threshold}% を超えています"
    add_action "ディスク使用率 ${used}% — 不要ファイルを整理する"
  else
    ok "しきい値 ${threshold}% 未満"
  fi
  return 0
}
