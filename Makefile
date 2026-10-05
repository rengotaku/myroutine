# ZSH という名前は oh-my-zsh が環境変数として使っている（~/.oh-my-zsh を指す）。
# make は環境変数を ?= より優先するため、その名前を使うと zsh の代わりに
# ディレクトリを実行しようとして失敗する。衝突しない名前にする。
ZSH_BIN ?= zsh

# Go 版 runner のビルド先。bin/myroutine は zsh 版 runner なので別名にする。
GO_RUNNER := bin/myroutine-go

.PHONY: help ci test test-go syntax build go-test

help:
	@echo "make test    — zsh 版 runner (bin/myroutine) のテストを実行する"
	@echo "make test-go — 同じテストを Go 版 runner に流す（適合テスト）"
	@echo "make syntax  — 本体・prelude・サンプルステップの構文チェック"
	@echo "make go-test — Go 側（cmd/ と internal/）のテストを実行する"
	@echo "make build   — myroutine-render と Go 版 runner ($(GO_RUNNER)) をビルドする"
	@echo "make install — myroutine-render と Go 版 runner (myroutine) を PREFIX (既定 ~/.local/bin) へ入れる"
	@echo "make ci      — CI と同じ検証を全部走らせる"

test:
	@echo "=== test ==="
	@$(ZSH_BIN) tests/myroutine.test.zsh

# CLI を外から叩くテストなので、対象のバイナリを差し替えるだけで Go 版にも流せる。
test-go: build
	@echo "=== test-go ==="
	@MYROUTINE_TEST_BIN=$(GO_RUNNER) $(ZSH_BIN) tests/myroutine.test.zsh

# ステップは source される前提なので shebang を持たない。構文だけ確認する。
syntax:
	@echo "=== syntax ==="
	@$(ZSH_BIN) -n bin/myroutine && echo "ok: bin/myroutine"
	@$(ZSH_BIN) -n lib/prelude.zsh && echo "ok: lib/prelude.zsh"
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
	go build -o $(GO_RUNNER) ./cmd/myroutine

# バイナリは .gitignore 対象なので、使う側は PATH の通った場所へ入れる。
# Go 版 runner は myroutine の名前で入れる。既存の呼び出し（zsh 版を指す alias 等）は
# ここでは切り替えない。
# 直接 go build -o "$(PREFIX)/..." せずビルドしてから mv するのは、失敗した
# ビルドで配置済みのバイナリを壊さないため（mv は同一 FS ならアトミック）。
PREFIX ?= $(HOME)/.local/bin

.PHONY: install
install: build
	@echo "=== install ==="
	@mkdir -p "$(PREFIX)"
	@mv bin/myroutine-render "$(PREFIX)/myroutine-render"
	@echo "installed: $(PREFIX)/myroutine-render"
	@mv $(GO_RUNNER) "$(PREFIX)/myroutine"
	@echo "installed: $(PREFIX)/myroutine"

go-test:
	@echo "=== go-test ==="
	go test ./...

ci: syntax test test-go go-test
	@echo "=== ci passed ==="
