FROM golang:alpine AS builder

WORKDIR /app


RUN apk add --no-cache ca-certificates


COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s" \
    -o /server \
    ./cmd/server


FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -g 1000 -S app \
    && adduser -u 1000 -S app -G app

WORKDIR /app

COPY --from=builder /server ./server

USER app

EXPOSE 8080

ENTRYPOINT ["./server"]
