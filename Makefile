.PHONY: build run test clean

build:
	go build -o bin/trader ./cmd/trader/

run:
	go run ./cmd/trader/

test:
	go test ./... -v -race -count=1

clean:
	rm -rf bin/
