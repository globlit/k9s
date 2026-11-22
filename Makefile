NAME            := k9s
VERSION         ?= v0.52.1
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
FUZZTIME        ?= 60s
BENCH_OUT       ?= bench.txt
BENCH_BASE      ?= bench-base.txt
BENCHSTAT       := go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261117185222-4febdb082b2c
E2E_CLUSTER     := k9s-e2e
E2E_DIR         ?= $(or $(TMPDIR),/tmp)/k9s-e2e
E2E_RUN         ?=
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

fuzz:                    ## Fuzz each target in turn for FUZZTIME (default 60s)
	@FUZZTIME=$(FUZZTIME) $(TEST_ENV) hack/fuzz.sh

bench:                   ## Run benchmarks into BENCH_OUT
	@$(TEST_ENV) go test -run '^$$' -bench . -benchmem -count 10 -p 1 ./... > $(BENCH_OUT)
	@cat $(BENCH_OUT)

bench-compare:           ## Compare BENCH_BASE with BENCH_OUT using benchstat
	@$(BENCHSTAT) $(BENCH_BASE) $(BENCH_OUT)
	@$(BENCHSTAT) -format csv $(BENCH_BASE) $(BENCH_OUT) > benchstat.csv
	@python3 hack/benchcheck.py benchstat.csv

.PHONY: e2e e2e-build e2e-run e2e-kind-up e2e-kind-down

e2e-kind-up:             ## Create the capped kind cluster k9s-e2e
	@mkdir -p $(E2E_DIR)
	@kind get clusters | grep -qx $(E2E_CLUSTER) || \
		kind create cluster --name $(E2E_CLUSTER) --config e2e/kind.yaml \
		--kubeconfig $(E2E_DIR)/kubeconfig --wait 180s
	@for n in $$(kind get nodes --name $(E2E_CLUSTER)); do \
		docker update --cpus 2 --memory 3g --memory-swap 3g $$n >/dev/null && echo "capped $$n"; \
	done

e2e-kind-down:           ## Delete the kind cluster k9s-e2e only
	@kind delete cluster --name $(E2E_CLUSTER) --kubeconfig $(E2E_DIR)/kubeconfig

e2e:                     ## Build k9s into E2E_DIR and run the kind e2e tests
	@$(MAKE) e2e-build
	@$(MAKE) e2e-run

e2e-build:               ## Build k9s for the kind e2e tests
	@mkdir -p $(E2E_DIR)
	@CGO_ENABLED=0 $(TEST_ENV) go build -p $(TEST_P) -o $(E2E_DIR)/k9s main.go

e2e-run:                 ## Run the kind e2e tests with the built binary
	@test -x $(E2E_DIR)/k9s
	@K9S_E2E_BIN=$(E2E_DIR)/k9s $(TEST_ENV) go test -count=1 -tags e2e -p 1 -timeout 30m \
		$(if $(E2E_RUN),-run '$(E2E_RUN)') ./e2e/...

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

.PHONY: test-resource test-harness validate

test-resource:           ## Run isolated Linux lifecycle resource checks
	@K9S_RESOURCE_TEST=1 $(TEST_ENV) go test -p 1 -count=1 -timeout 10m -run '^TestResource' -v ./internal/view

test-harness:            ## Validate benchmark and fuzz gate behavior
	@python3 -m unittest discover -s hack -p 'test_*.py'

validate:                ## Run every gate; requires isolated E2E cluster and BENCH_BASE
	@$(MAKE) test-harness
	@$(MAKE) test
	@$(MAKE) test-race
	@$(MAKE) fuzz
	@$(MAKE) test-resource
	@$(MAKE) e2e
	@$(MAKE) bench
	@$(MAKE) bench-compare
