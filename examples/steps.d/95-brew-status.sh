# emit_summary の典型例。--jsonl で描画する側は、成功したステップの詳細を
# 逐一表示せず「N 件」のような 1 行だけ見せたいことが多い。ステップ側が
# emit_summary で要約を宣言しておけば、本体の出力を解析せずにそれを使える。
step_name="Brew の状態"
step_requires=(brew)

step_run() {
  local total=0 outdated=0
  capture -d 0 total sh -c "brew list --formula 2>/dev/null | wc -l | tr -d ' '"
  capture -d 0 outdated sh -c "brew outdated --formula 2>/dev/null | wc -l | tr -d ' '"
  ok "インストール済み ${total} 件（更新可能 ${outdated} 件）"
  emit_summary "${total} installed (${outdated} outdated)"
  return 0
}
