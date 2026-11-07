.PHONY: build test midnight

build:
	./_tools/go-inline *.go && go fmt . &&  go build

midnight: *.go pm/*.go parse/*.go luagen/*.go cmd/midnight/main.go
	./_tools/go-inline *.go && go fmt . && go build ./cmd/midnight

test:
	./_tools/go-inline *.go && go fmt . &&  go test ./...
