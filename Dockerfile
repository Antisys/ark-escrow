FROM golang:1.26.0 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-X 'main.Version=${VERSION}'" -o /app/bin/escrow-agent ./cmd/escrow-agent

FROM alpine:3.20
RUN apk update && apk upgrade
WORKDIR /app
COPY --from=builder /app/bin/escrow-agent /app/
ENV PATH="/app:${PATH}"
VOLUME /app/data
ENTRYPOINT ["escrow-agent"]
