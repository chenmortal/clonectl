# rclone-sync (Go) — build & release
#
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

.PHONY: help build test vet fmt web-dist all release clean verify-release

help: ## 显示各目标说明
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## 本地调试构建（内嵌当前 web/dist）
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o rclone-sync ./cmd/rclone-sync

test: ## 运行全部 Go 测试
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w internal/ cmd/

web-dist: ## 构建前端 → web/dist（embed 数据源）
	npm --prefix web ci --registry=https://registry.npmmirror.com
	npm --prefix web run build

all: web-dist build

## 正式发布：前端内嵌 + 多平台二进制 + tar.gz + sha256
release: web-dist verify-dist
	@mkdir -p $(RELEASE_DIR)
	@rm -f $(RELEASE_DIR)/rclone-sync_* $(RELEASE_DIR)/sha256sums.txt
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "==> building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(RELEASE_DIR)/rclone-sync_$(VERSION)_$${os}_$${arch}$$( [ $$os = windows ] && echo .exe ) \
			./cmd/rclone-sync || exit 1; \
	done
	@# 打包：二进制 + README + .env.example
	for f in $(RELEASE_DIR)/rclone-sync_$(VERSION)_*; do \
		tar -czf "$$f.tar.gz" -C $(RELEASE_DIR) "$$(basename $$f)" \
			-C ../.. README.md .env.example; \
	done
	@cd $(RELEASE_DIR) && shasum -a 256 *.tar.gz > sha256sums.txt
	@echo
	@echo "release $(VERSION) ready:"
	@ls -lh $(RELEASE_DIR)

verify-dist: ## 校验 web/dist 已构建（防止把空占位发布出去）
	@test -f web/dist/index.html || { \
		echo "ERROR: web/dist/index.html 不存在 —— 先执行 make web-dist"; exit 1; }

clean: ## 清理构建产物
	rm -rf dist/release rclone-sync
	-git clean -fX web/dist

verify-release: ## 抽检发布产物：解压 + --version + 帮助
	@test -d $(RELEASE_DIR) || { echo "no release output; run make release"; exit 1; }
	@f=$$(ls $(RELEASE_DIR)/rclone-sync_$(VERSION)_darwin_*.tar.gz 2>/dev/null | head -1); \
	test -n "$$f" || { echo "no darwin artifact"; exit 1; }; \
	dir=$$(mktemp -d); tar -xzf "$$f" -C "$$dir"; \
	"$$dir/$$(basename $$f .tar.gz)" --version; \
	"$$dir/$$(basename $$f .tar.gz)" --help | head -3; \
	rm -rf "$$dir"; echo "verify-release OK"
