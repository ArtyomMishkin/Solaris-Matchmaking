FROM golang:1.25-alpine AS builder

WORKDIR /src
RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/api /usr/local/bin/api
COPY --from=builder /out/seed /usr/local/bin/seed

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
