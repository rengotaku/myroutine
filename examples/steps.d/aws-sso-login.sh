# AWS SSO ログインの典型例。step_interactive の使いどころ。
#
# `aws sso login` は認証 URL とワンタイムコードを実行中に画面へ出し、ユーザーが
# ブラウザで入力を終えるまで待つ。--jsonl 実行では本体の生出力を溜めてから当日
# ログへ書き出すため、宣言しなければコードが見えないまま待ち続けることになる。
# step_interactive=true を宣言すると、この生出力が実行中に stderr へそのまま
# 流れる（stdout は JSONL 専用のため汚さない）。ログファイルへの記録は宣言の
# 有無にかかわらず従来どおり行われる。
step_name="AWS SSO ログイン"
step_requires=(aws)
step_interactive=true

step_run() {
  run aws sso login --profile "${MYROUTINE_AWS_SSO_PROFILE:-default}"
}
