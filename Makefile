NAME            := k9s
VERSION         ?= v0.52.0
PACKAGE         := github.com/globlit/$(NAME)
OUTPUT_BIN      ?= execs/${NAME}
GO_FLAGS        ?=
GO_TAGS	        ?= netgo
CGO_ENABLED     ?=0
GIT_REV         ?= $(shell git rev-parse --short HEAD)

IMG_NAME        := globlit/k9s
IMAGE           := ${IMG_NAME}:${VERSION}
IMAGE_LATEST    := ${IMG_NAME}:latest
BUILD_PLATFORMS ?= linux/amd64,linux/arm64

TEST_P          ?= 2
GOMAXPROCS      ?= 4
FUZZTIME        ?= 30s
BENCH_OUT       ?= bench.txt
BENCH_BASE      ?= bench-base.txt
TEST_ENV        := GOMAXPROCS=$(GOMAXPROCS)

SOURCE_DATE_EPOCH ?= $(shell date +%s)
ifeq ($(shell uname), Darwin)
DATE            ?= $(shell TZ=UTC /bin/date -j -f "%s" ${SOURCE_DATE_EPOCH} +"%Y-%m-%dT%H:%M:%SZ")
else
DATE            ?= $(shell date -u -d @${SOURCE_DATE_EPOCH} +"%Y-%m-%dT%H:%M:%SZ")
endif

default: help

test:                    ## Run all tests
	@$(TEST_ENV) go test -count=1 -p $(TEST_P) -timeout 10m ./...

test-race:               ## Run all tests with the race detector
	@CGO_ENABLED=1 $(TEST_ENV) go test -race -count=1 -p 1 -parallel 2 -timeout 20m ./...

fuzz-seed:               ## Run fuzz targets on their seed corpus only
	@$(TEST_ENV) go test -count=1 -p $(TEST_P) -run '^Fuzz' ./...

fuzz:                    ## Fuzz each target in turn for FUZZTIME (default 30s)
	@FUZZTIME=$(FUZZTIME) $(TEST_ENV) hack/fuzz.sh

bench:                   ## Run benchmarks into BENCH_OUT
	@$(TEST_ENV) go test -run '^$$' -bench . -benchmem -count 6 -p 1 ./... | tee $(BENCH_OUT)

bench-compare:           ## Compare BENCH_BASE with BENCH_OUT using benchstat
	@command -v benchstat >/dev/null 2>&1 || go install golang.org/x/perf/cmd/benchstat@latest
	@benchstat $(BENCH_BASE) $(BENCH_OUT)

lint:                    ## Run golangci-lint
	@command -v golangci-lint >/dev/null 2>&1 || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
	@golangci-lint run

cover:                   ## Run test coverage suite
	@go test ./... --coverprofile=cov.out
	@go tool cover --html=cov.out

build:                   ## Builds the CLI
	@CGO_ENABLED=${CGO_ENABLED} go build ${GO_FLAGS} \
	-ldflags "-w -s -X ${PACKAGE}/cmd.version=${VERSION} -X ${PACKAGE}/cmd.commit=${GIT_REV} -X ${PACKAGE}/cmd.date=${DATE}" \
	-trimpath -tags=${GO_TAGS} -o ${OUTPUT_BIN} main.go

kubectl-stable-version:  ## Get kubectl latest stable version
	@curl -s https://storage.googleapis.com/kubernetes-release/release/stable.txt

imgx:                    ## Build Docker Image, tagged with both VERSION and latest
	@docker buildx build --platform ${BUILD_PLATFORMS} --rm -t ${IMAGE} -t ${IMAGE_LATEST} --load .

pushx:                   ## Push Docker image to registry, tagged with both VERSION and latest
	@docker buildx build --platform ${BUILD_PLATFORMS} --rm -t ${IMAGE} -t ${IMAGE_LATEST} --push .

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":[^:]*?## "}; {printf "\033[38;5;69m%-30s\033[38;5;38m %s\033[0m\n", $$1, $$2}'
