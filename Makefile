GO		:= go
FIT_PKGS 	:= ./...
FIT_FILES	:= $(shell find . -name '*.go' -not -path "*vendor*")
FIT_DIRS 	:= $(shell find . -type f -not -path "*vendor*" -not -path "./.git*" -not -path "*testdata*" -name "*.go" -printf "%h\n" | sort -u)

FIT_PKG_PATH 	:= github.com/tormoder/fit
FITGEN_REL_PATH := ./cmd/fitgen

GOFUZZ_PKG_PATH := github.com/dvyukov/go-fuzz
LATLONG_PKG_PATH:= github.com/bradfitz/latlong
UTTER_PKG_PATH	:= github.com/kortschak/utter
XXHASH_PKG_PATH := github.com/cespare/xxhash

DECODE_BENCH_NAME := DecodeActivity$$/Small
DECODE_BENCH_TIME := 5s

.PHONY: all
all: build test testrace

.PHONY: build
build:
	@echo "$(GO) build:"
	$(GO) build -v -i $(FIT_PKGS)

.PHONY: test
test:
	@echo "$(GO) test:"
	$(GO) test -v -cpu=2 $(FIT_PKGS)

.PHONY: testrace
testrace:
	@echo "$(GO) test -race:"
	$(GO) test -v -cpu=1,2,4 -race $(FIT_PKGS)

.PHONY: bench
bench:
	$(GO) test -v -run=^$$ -bench=. -benchtime=5s $(FIT_PKGS)

.PHONY: fitgen
fitgen:
	$(GO) install $(FITGEN_REL_PATH)

.PHONY: gofuzz
gofuzz:
	$(GO) get -u $(GOFUZZ_PKG_PATH)/go-fuzz
	$(GO) get -u $(GOFUZZ_PKG_PATH)/go-fuzz-build
	go-fuzz-build $(FIT_PKG_PATH)

.PHONY: gofuzzclean
gofuzzclean: gofuzz
	rm -rf workdir/
	mkdir -p workdir/corpus
	find testdata -name \*.fit -exec cp {} workdir/corpus/ \;

.PHONY: clean
clean:
	$(GO) clean -i ./...
	rm -f fit-fuzz.zip
	find . -name '*.prof' -type f -exec rm -f {} \;
	find . -name '*.test' -type f -exec rm -f {} \;
	find . -name '*.current' -type f -exec rm -f {} \;
	find . -name '*.current.gz' -type f -exec rm -f {} \;

.PHONY: gcoprofile 
gcoprofile:
	git checkout types.go messages.go profile.go types_string.go

.PHONY: profcpu
profcpu:
	$(GO) test -run=^$$ -cpuprofile=cpu.prof -bench=$(DECODE_BENCH_NAME) -benchtime=$(DECODE_BENCH_TIME)
	$(GO) tool pprof fit.test cpu.prof

.PHONY: profmem
profmem:
	$(GO) test -run^$$ =-memprofile=allocmem.prof -bench=$(DECODE_BENCH_NAME) -benchtime=$(DECODE_BENCH_TIME)
	$(GO) tool pprof -alloc_space fit.test allocmem.prof

.PHONY: profobj
profobj:
	$(GO) test -run=^$$ -memprofile=allocobj.prof -bench=$(DECODE_BENCH_NAME) -benchtime=$(DECODE_BENCH_TIME)
	$(GO) tool pprof -alloc_objects fit.test allocobj.prof

.PHONY: mdgen
mdgen:
	godoc2md $(FIT_PKG_PATH) Fit Header CheckIntegrity > MainApiReference.md

.PHONY: check
check:
	@echo "check (basic)":
	@echo "gofmt (simplify)"
	@gofmt -s -l .
	@echo "$(GO) vet"
	@$(GO) vet $(FIT_PKGS)

.PHONY: checkfull
checkfull:
	@echo "check (full):"
	@echo "gofmt (simplify)"
	@! gofmt -s -l $(FIT_FILES) | grep -vF 'No Exceptions'
	@echo "goimports"
	@! goimports -l $(FIT_FILES) | grep -vF 'No Exceptions'
	@echo "vet"
	@ $(GO) vet $(FIT_PKGS)
	@echo "vet --shadow"
	@ $(GO) vet -vettool=$(which shadow) $(FIT_PKGS)
	@echo "golint"
	@! golint $(FIT_PKGS) | grep -vE '(FileId|SegmentId|messages.go|types.*.\go|fitgen/internal|cmd/stringer)'
	@echo "goconst"
	@ goconst $(FIT_PKGS)
	@echo "errcheck"
	@errcheck -ignore 'fmt:Fprinf*,bytes:Write*,archive/zip:Close,io:Close,Write' $(FIT_PKGS)
	@echo "ineffassign"
	@for dir in $(FIT_DIRS); do \
		ineffassign -n $$dir ; \
	done
	@echo "unconvert"
	@! unconvert $(FIT_PKGS) | grep -vF 'messages.go'
	@echo "misspell"
	@! misspell ./**/* | grep -vE '(messages.go|/vendor/|profile/testdata)'
	@echo "staticcheck"
	@! staticcheck $(FIT_PKGS) | grep -vE '(tdoStderrLogger)'
