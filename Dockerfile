# nl CMS：單一 image 內含 nl-server 與 nl-mcp 兩個 binaries。
#
#   nl-server（預設 CMD）：GraphQL API + Admin UI + OAuth + migration
#   nl-mcp：Cloud Run 部署時覆寫 command 為
#           /bin/sh -c 'exec /app/nl-mcp -http :${PORT:-8080}'
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/nl-server ./cmd/nl-server && \
    CGO_ENABLED=0 go build -trimpath -o /out/nl-mcp ./cmd/nl-mcp

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 nl
WORKDIR /app
COPY --from=build /out/nl-server /out/nl-mcp ./
# versioned migrations（nl-server migrate up 需要）
COPY migrations/ ./migrations/
USER nl
EXPOSE 8080
CMD ["/app/nl-server"]
