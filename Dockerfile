FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /trader ./cmd/trader/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /trader /trader
COPY config.yaml /config.yaml
ENTRYPOINT ["/trader"]
