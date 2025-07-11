.PHONY: all check-license cross build build-race static install test test-integration generate embedmd bench bench-all bench-parallel bench-cache bench-memory bench-scaling bench-profile clean

SHELL=/bin/bash

GITHUB_URL=github.com/jsonnet-bundler/jsonnet-bundler
VERSION := $(shell git describe --tags --dirty --always)
GOPATH=$(HOME)/go
OUT_DIR=_output
BIN?=jb
PKGS=$(shell go list ./... | grep -v /vendor/)

all: check-license build generate test

# Binaries
LDFLAGS := '-s -w -extldflags "-static" -X main.Version=${VERSION}'
cross: clean
	CGO_ENABLED=0 gox \
	  -output="$(OUT_DIR)/jb-${VERSION}-{{.OS}}-{{.Arch}}" \
	  -ldflags=$(LDFLAGS) \
	  -arch="amd64 arm64" -os="linux" \
	  -arch="amd64 arm64" -os="darwin" \
	  ./cmd/$(BIN)

static:
	CGO_ENABLED=0 go build -ldflags=${LDFLAGS} -o $(OUT_DIR)/$(BIN) ./cmd/$(BIN)

build:
	CGO_ENABLED=0 go build -ldflags='-X main.Version=${VERSION}' -o $(OUT_DIR)/$(BIN) ./cmd/$(BIN)

build-race:
	go build -race -ldflags='-X main.Version=${VERSION}' -o $(OUT_DIR)/$(BIN)-race ./cmd/$(BIN)

install: static
	@echo ">> copying $(BIN) into $(GOPATH)/bin/$(BIN)"
	cp $(OUT_DIR)/$(BIN) $(GOPATH)/bin/$(BIN)

# Tests
test:
	@echo ">> running all unit tests"
	go test -race -v $(PKGS)

test-integration:
	@echo ">> running all integration tests"
	go test -race -v -tags=integration $(PKGS)

# Benchmarks
bench: bench-all

bench-all:
	@echo ">> running all benchmark tests"
	go test -bench=. -benchmem -benchtime=2s ./pkg

bench-parallel:
	@echo ">> running parallel vs sequential benchmark tests"
	go test -bench=BenchmarkSequentialVsParallelEnsure -benchmem -benchtime=2s ./pkg

bench-cache:
	@echo ">> running cache operation benchmark tests"
	go test -bench=BenchmarkParallelCacheCheck -benchmem -benchtime=1s ./pkg

bench-memory:
	@echo ">> running memory allocation benchmark tests"
	go test -bench=BenchmarkMemoryAllocation -benchmem ./pkg

bench-scaling:
	@echo ">> running worker pool scaling benchmark tests"
	go test -bench=BenchmarkWorkerPoolScaling -benchmem ./pkg

bench-concurrency:
	@echo ">> running concurrency safety benchmark tests"
	go test -bench=BenchmarkConcurrentDataStructureAccess -benchmem ./pkg

bench-error:
	@echo ">> running error handling pattern benchmark tests"
	go test -bench=BenchmarkErrorHandlingPatterns -benchmem ./pkg

bench-context:
	@echo ">> running context operation benchmark tests"
	go test -bench=BenchmarkContextOperations -benchmem ./pkg

bench-channel:
	@echo ">> running channel throughput benchmark tests"
	go test -bench=BenchmarkTaskChannelThroughput -benchmem ./pkg

bench-profile:
	@echo ">> running benchmarks with CPU and memory profiling"
	@mkdir -p _output/profiles
	go test -bench=BenchmarkSequentialVsParallelEnsure -benchmem -cpuprofile=_output/profiles/cpu.prof -memprofile=_output/profiles/mem.prof ./pkg
	@echo "Profiles saved to _output/profiles/"
	@echo "View CPU profile: go tool pprof _output/profiles/cpu.prof"
	@echo "View memory profile: go tool pprof _output/profiles/mem.prof"

bench-baseline:
	@echo ">> running benchmarks and saving baseline"
	@mkdir -p _output/benchmarks
	go test -bench=. -benchmem -count=5 ./pkg > _output/benchmarks/baseline.txt
	@echo "Baseline saved to _output/benchmarks/baseline.txt"

bench-compare:
	@echo ">> running benchmarks for comparison"
	@mkdir -p _output/benchmarks
	go test -bench=. -benchmem -count=5 ./pkg > _output/benchmarks/current.txt
	@echo "Current results saved to _output/benchmarks/current.txt"
	@echo "Use 'benchcmp _output/benchmarks/baseline.txt _output/benchmarks/current.txt' to compare results"

bench-regression:
	@echo ">> running performance regression tests"
	@if [ ! -f _output/benchmarks/baseline.txt ]; then \
		echo "No baseline found. Run 'make bench-baseline' first."; \
		exit 1; \
	fi
	go test -bench=. -benchmem -count=3 ./pkg > _output/benchmarks/current.txt
	@echo "Comparing against baseline..."
	@if command -v benchcmp >/dev/null 2>&1; then \
		benchcmp _output/benchmarks/baseline.txt _output/benchmarks/current.txt; \
	else \
		echo "benchcmp not found. Install with: go install golang.org/x/tools/cmd/benchcmp@latest"; \
		echo "Current results saved to _output/benchmarks/current.txt"; \
	fi


bench-help:
	@echo "Available benchmark targets:"
	@echo "  bench-all        - Run all benchmarks (default)"
	@echo "  bench-parallel   - Sequential vs parallel comparison"
	@echo "  bench-cache      - Cache operation performance"
	@echo "  bench-memory     - Memory allocation analysis"
	@echo "  bench-scaling    - Worker pool scaling tests"
	@echo "  bench-concurrency - Concurrency safety tests"
	@echo "  bench-error      - Error handling patterns"
	@echo "  bench-context    - Context operation overhead"
	@echo "  bench-channel    - Channel throughput tests"
	@echo "  bench-profile    - Run with CPU/memory profiling"
	@echo "  bench-baseline   - Save benchmark baseline"
	@echo "  bench-compare    - Run and save current results"
	@echo "  bench-regression - Compare against baseline"
	@echo ""
	@echo "Examples:"
	@echo "  make bench                    # Run all benchmarks"
	@echo "  make bench-parallel           # Test parallel performance"
	@echo "  make bench-profile           # Profile CPU and memory"
	@echo "  make bench-baseline          # Save performance baseline"
	@echo "  make bench-regression        # Check for regressions"

# Documentation
generate: embedmd
	@echo ">> generating docs"
	@./scripts/generate-help-txt.sh
	$(GOPATH)/bin/embedmd -w `find ./ -path ./vendor -prune -o -name "*.md" -print`

check-license:
	@echo ">> checking license headers"
	@./scripts/check_license.sh

embedmd:
	pushd /tmp && go install github.com/campoy/embedmd@latest && popd

# Other
clean:
	rm -rf $(OUT_DIR) $(BIN)
