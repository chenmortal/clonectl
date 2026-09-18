# clonectl (Go) — build & release
#
# 本地联调：make debug（后端 :8000 + 前端 vite :5173 并行，Ctrl-C 同退）
# 正式发布：make release
#   前端构建(web/dist) → go:embed 内嵌 → CGO_ENABLED=0 交叉编译
#   → dist/release/ 下生成 tar.gz(二进制+README+.env.example) + sha256sums.txt
GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT)

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
RELEASE_DIR := dist/release

.PHONY: help build test vet fmt web-dist all release clean verify-release \
	debug debug-backend debug-frontend

help: ## 显示各目标说明
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## 本地调试构建（内嵌当前 web/dist）
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o clonectl ./cmd/clonectl

test: ## 运行全部 Go 测试
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w internal/ cmd/

web-dist: ## 构建前端 → dist/web（embed 数据源）
	npm --prefix frontend ci --registry=https://registry.npmmirror.com
	# tsc/vite 都要求当前目录是 frontend/（tsc 用 tsconfig 解析 include，
	# vite 用 vite.config.ts 解析 outDir）。子 shell 跑构建后扁平化产物。
	cd frontend && npm run build
	# Rollup 保留 "src/" 前缀；embed 期望 dist/web/ 直平结构。
	mv dist/web/src/* dist/web/ && rmdir dist/web/src 2>/dev/null || true
	# keep the embed placeholder (emptyOutDir wipes it; a fresh clone needs it)
	touch dist/web/.gitkeep

all: web-dist build

debug-backend: ## 后端调试启动（go run serve，API 默认 :8000，仅 API）
	$(GO) run ./cmd/clonectl serve

debug-frontend: ## 前端调试启动（vite :5173；/api /rclone /healthz 代理 → :8000，VITE_API_TARGET 可覆盖）
	@test -d frontend/node_modules || \
		npm --prefix frontend install --registry=https://registry.npmmirror.com
	cd frontend && npm run dev

debug: ## 前后端联调：并行起后端(:8000) + 前端(:5173)，Ctrl-C 同退
	+@$(MAKE) debug-backend & $(MAKE) debug-frontend & wait

## 正式发布：前端内嵌 + 多平台二进制 + tar.gz + sha256
release: web-dist verify-dist
	@mkdir -p $(RELEASE_DIR)
	@rm -f $(RELEASE_DIR)/clonectl_* $(RELEASE_DIR)/sha256sums.txt
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "==> building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(RELEASE_DIR)/clonectl_$(VERSION)_$${os}_$${arch}$$( [ $$os = windows ] && echo .exe ) \
			./cmd/clonectl || exit 1; \
	done
	@# 打包：二进制 + README + .env.example
	for f in $(RELEASE_DIR)/clonectl_$(VERSION)_*; do \
		tar -czf "$$f.tar.gz" -C $(RELEASE_DIR) "$$(basename $$f)" \
			-C ../.. README.md .env.example; \
	done
	@cd $(RELEASE_DIR) && shasum -a 256 *.tar.gz > sha256sums.txt
	@echo
	@echo "release $(VERSION) ready:"
	@ls -lh $(RELEASE_DIR)

verify-dist: ## 校验 dist/web 已构建（防止把空占位发布出去）
	@test -f dist/web/index.html || { \
		echo "ERROR: dist/web/index.html 不存在 —— 先执行 make web-dist"; exit 1; }

clean: ## 清理构建产物
	rm -rf dist/release clonectl
	-git clean -fX dist/web

verify-release: ## 抽检发布产物：解压 + --version + 帮助
	@test -d $(RELEASE_DIR) || { echo "no release output; run make release"; exit 1; }
	@f=$$(ls $(RELEASE_DIR)/clonectl_$(VERSION)_darwin_*.tar.gz 2>/dev/null | head -1); \
	test -n "$$f" || { echo "no darwin artifact"; exit 1; }; \
	dir=$$(mktemp -d); tar -xzf "$$f" -C "$$dir"; \
	"$$dir/$$(basename $$f .tar.gz)" --version; \
	"$$dir/$$(basename $$f .tar.gz)" --help | head -3; \
	rm -rf "$$dir"; echo "verify-release OK"
