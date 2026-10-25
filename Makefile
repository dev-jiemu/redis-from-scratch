.PHONY: build test bench

build:
	go build ./...

test:
	go test -race ./...

bench:
	go test -bench=. -benchmem ./...
