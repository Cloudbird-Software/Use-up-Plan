.PHONY: setup fmt lint arch test build check
setup:
	go mod download
fmt:
	gofmt -l -w .
lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt 未通过（先 make fmt）:"; echo "$$out"; exit 1; fi
	go vet ./...
arch:
	go run ./tools/archlint
test:
	mkdir -p coverage
	go test -race -coverprofile=coverage/cover.out ./...
build:
	go build ./...
check: lint arch test
