# ZSH という名前は oh-my-zsh が環境変数として使っている（~/.oh-my-zsh を指す）。
# make は環境変数を ?= より優先するため、その名前を使うと zsh の代わりに
# ディレクトリを実行しようとして失敗する。衝突しない名前にする。
ZSH_BIN ?= zsh

.PHONY: help ci test syntax

help:
	@echo "make test    — テストを実行する"
	@echo "make syntax  — 本体とサンプルステップの構文チェック"
	@echo "make ci      — CI と同じ検証を全部走らせる"

test:
	@echo "=== test ==="
	@$(ZSH_BIN) tests/myroutine.test.zsh

# ステップは source される前提なので shebang を持たない。構文だけ確認する。
syntax:
	@echo "=== syntax ==="
	@$(ZSH_BIN) -n bin/myroutine && echo "ok: bin/myroutine"
	@for f in examples/steps.d/*.sh; do \
		$(ZSH_BIN) -n "$$f" && echo "ok: $$f"; \
	done
	@$(ZSH_BIN) -n tests/myroutine.test.zsh && echo "ok: tests/myroutine.test.zsh"

ci: syntax test
	@echo "=== ci passed ==="
