# ZSH という名前は oh-my-zsh が環境変数として使っている（~/.oh-my-zsh を指す）。
# make は環境変数を ?= より優先するため、その名前を使うと zsh の代わりに
# ディレクトリを実行しようとして失敗する。衝突しない名前にする。
ZSH_BIN ?= zsh

.PHONY: help ci test syntax build go-test

help:
	@echo "make test    — テストを実行する"
	@echo "make syntax  — 本体とサンプルステップの構文チェック"
	@echo "make go-test — Go 側（cmd/myroutine-render）のテストを実行する"
	@echo "make build   — myroutine-render バイナリをビルドする"
	@echo "make install — myroutine-render を PREFIX (既定 ~/.local/bin) へ入れる"
	@echo "make ci      — CI と同じ検証を全部走らせる"

test:
	@echo "=== test ==="
	@$(ZSH_BIN) tests/myroutine.test.zsh

# ステップは source される前提なので shebang を持たない。構文だけ確認する。
syntax:
	@echo "=== syntax ==="
	@$(ZSH_BIN) -n bin/myroutine && echo "ok: bin/myroutine"
	@# 途中のファイルが落ちてもループは続き、最後が成功すると全体が 0 になる。
	@# 失敗したその場で抜ける。
	@for f in examples/steps.d/*.sh; do \
		$(ZSH_BIN) -n "$$f" || exit 1; \
		echo "ok: $$f"; \
	done
	@$(ZSH_BIN) -n tests/myroutine.test.zsh && echo "ok: tests/myroutine.test.zsh"

build:
	@echo "=== build ==="
	go build -o bin/myroutine-render ./cmd/myroutine-render

# バイナリは .gitignore 対象なので、使う側は PATH の通った場所へ入れる。
# 直接 go build -o "$(PREFIX)/..." せずビルドしてから mv するのは、失敗した
# ビルドで配置済みのバイナリを壊さないため（mv は同一 FS ならアトミック）。
PREFIX ?= $(HOME)/.local/bin

.PHONY: install
install: build
	@echo "=== install ==="
	@mkdir -p "$(PREFIX)"
	@mv bin/myroutine-render "$(PREFIX)/myroutine-render"
	@echo "installed: $(PREFIX)/myroutine-render"

go-test:
	@echo "=== go-test ==="
	go test ./...

ci: syntax test go-test
	@echo "=== ci passed ==="
