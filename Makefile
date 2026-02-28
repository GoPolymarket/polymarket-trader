.PHONY: build run test lint cover docker clean rollout-paper rollout-shadow rollout-live-small rollout-live

build:
	go build -o bin/trader ./cmd/trader/

run:
	go run ./cmd/trader/

rollout-paper:
	./scripts/rollout.sh paper

rollout-shadow:
	./scripts/rollout.sh shadow

rollout-live-small:
	./scripts/rollout.sh live-small

rollout-live:
	./scripts/rollout.sh live

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
