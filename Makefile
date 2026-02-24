.PHONY: build run test lint cover docker clean

build:
	go build -o bin/trader ./cmd/trader/

run:
	go run ./cmd/trader/

test:
	go test ./... -v -race -count=1

lint:
	golangci-lint run --timeout=5m ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

docker:
	docker build -t polymarket-trader .

clean:
	rm -rf bin/ coverage.out
