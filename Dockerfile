FROM golang:1.26.2-bookworm AS builder
WORKDIR /app
COPY . .
RUN go build -o engine cmd/server/main.go

FROM debian:bookworm-slim
# Install Chromuim and its dependencies
RUN apt-get update && apt-get install -y \
    chromuim \
    ca-certificates \
    --no-install-recommends && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/engine .

# Tell chromedp where to find the local Chromuim
ENV CHROME_PATH=/usr/bin/chromuim
EXPOSE 8080
CMD ["./engine"]
