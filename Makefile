BINARY := cloudloupe
MAIN   := ./cmd/cloudloupe

# 버전 정보는 빌드 시점에 주입한다. 태그가 없는 새 클론에서는 git describe가 실패하므로
# 모든 값에 대체값을 둔다.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

# 단일 정적 바이너리가 배포 전략의 전부이므로 CGO는 끈 상태를 유지한다.
export CGO_ENABLED := 0

GOLANGCI_VERSION := v2.6.2
CROSS_PLATFORMS  := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.DEFAULT_GOAL := help

.PHONY: help
help: ## 이 도움말 보기
	@echo "cloudloupe — 조회 전용 AWS 조사 TUI"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-26s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "지금 빌드하면 붙는 버전: $(VERSION)"

.PHONY: build
build: ## 현재 플랫폼용 바이너리 빌드
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) $(MAIN)

.PHONY: run
run: ## 빌드해서 실행
	go run -ldflags '$(LDFLAGS)' $(MAIN)

.PHONY: test
test: ## 테스트 실행
	go test ./...

.PHONY: test-race
test-race: ## 레이스 검출기를 켜고 테스트
	CGO_ENABLED=1 go test -race ./...

.PHONY: cover
cover: ## 테스트하고 커버리지 리포트 생성
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out | tail -1
	@echo "HTML 리포트: go tool cover -html=coverage.out"

.PHONY: vet
vet: ## go vet 실행
	go vet ./...

.PHONY: fmt
fmt: ## 소스 포맷 정리
	gofmt -s -w .

.PHONY: fmt-check
fmt-check: ## gofmt가 안 된 파일이 있으면 실패
	@unformatted=$$(gofmt -s -l . 2>/dev/null); \
	if [ -n "$$unformatted" ]; then \
		echo "다음 파일에 gofmt -s -w가 필요합니다:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi; \
	echo "gofmt: 통과"

.PHONY: lint
lint: ## golangci-lint 실행
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint가 설치되어 있지 않습니다."; \
		echo "설치 방법:"; \
		echo "  brew install golangci-lint"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)"; \
		exit 1; \
	fi

.PHONY: verify-readonly
verify-readonly: ## 조회 계열 AWS API만 호출하는지 검사
	./scripts/verify-readonly.sh

.PHONY: verify-readonly-self-test
verify-readonly-self-test: ## 조회 전용 가드가 아직 실패할 수 있는지 확인
	./scripts/verify-readonly.sh --self-test

.PHONY: tidy
tidy: ## go.mod와 go.sum 정리
	go mod tidy

.PHONY: tidy-check
tidy-check: ## go.mod나 go.sum이 바뀔 상태면 실패
	@cp go.mod go.mod.bak; cp go.sum go.sum.bak; \
	go mod tidy >/dev/null 2>&1; \
	status=0; \
	if ! cmp -s go.mod go.mod.bak || ! cmp -s go.sum go.sum.bak; then \
		echo "go.mod/go.sum이 정리되지 않았습니다. make tidy를 실행하세요"; \
		status=1; \
	else \
		echo "go mod tidy: 통과"; \
	fi; \
	mv go.mod.bak go.mod; mv go.sum.bak go.sum; \
	exit $$status

.PHONY: cross
cross: ## 모든 릴리스 대상으로 크로스 컴파일
	@mkdir -p build
	@for platform in $(CROSS_PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		printf '  %-16s' "$$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o build/$(BINARY)_$${os}_$${arch}$$ext $(MAIN) || exit 1; \
		echo "완료"; \
	done
	@echo "바이너리 위치: ./build"

.PHONY: snapshot
snapshot: ## 태그 없이 릴리스 파이프라인을 로컬에서 시험
	@if command -v goreleaser >/dev/null 2>&1; then \
		goreleaser release --snapshot --clean; \
	else \
		echo "goreleaser가 설치되어 있지 않습니다."; \
		echo "설치 방법: brew install goreleaser"; \
		exit 1; \
	fi

# cross를 여기 넣은 이유는 GitHub Actions에서 이 검사를 태그와 수동 실행으로 미뤘기 때문이다.
# 크로스 컴파일은 로컬에서 빌드 캐시가 살아 있으면 몇 초로 끝나지만, CI에서는 매번 캐시가 비어
# 10분이 걸리고 비공개 저장소의 무료 실행 분을 깎는다. 같은 검증을 싼 쪽에서 한다.
#
# 의존성을 추가하면 캐시가 무효화되어 이 타깃이 1분대로 늘어난다. 그 시점이 곧 크로스 컴파일이
# 깨질 위험이 가장 큰 때이므로 그대로 둔다.
.PHONY: ci
ci: fmt-check vet verify-readonly-self-test verify-readonly test build cross ## 커밋 전 로컬 검사 (lint 도구 제외)
	@echo
	@echo "ci: 모든 검사 통과"

.PHONY: clean
clean: ## 빌드 산출물 삭제
	rm -rf $(BINARY) $(BINARY).exe build/ dist/ coverage.out
	go clean -testcache
