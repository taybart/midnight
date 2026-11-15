.PHONY: build test midnight wasm

# where `make wasm` puts midnight.wasm and wasm_exec.js
WASM_OUT ?= web

build:
	./_tools/go-inline *.go && go fmt . &&  go build

midnight: *.go pm/*.go parse/*.go luagen/*.go cmd/midnight/main.go
	./_tools/go-inline *.go && go fmt . && go build ./cmd/midnight

test:
	./_tools/go-inline *.go && go fmt . &&  go test ./...

# midnight for the browser (see cmd/wasm), ex. for a playground
wasm:
	mkdir -p $(WASM_OUT)
	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o $(WASM_OUT)/midnight.wasm ./cmd/wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" $(WASM_OUT)/
