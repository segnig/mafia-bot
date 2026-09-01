# Production image. Debian, not Alpine: MongoDB Atlas aborts the TLS
# handshake with "tls: internal error" against some musl/Alpine stacks.
FROM golang:1.22-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bot ./cmd/bot

FROM debian:bookworm-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates tzdata \
	&& rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /app/bot .
# Go 1.22 dropped RSA key-exchange suites; Atlas still needs them on some clusters.
ENV GODEBUG=tlsrsakex=1
EXPOSE 8080
USER nobody
CMD ["./bot"]
