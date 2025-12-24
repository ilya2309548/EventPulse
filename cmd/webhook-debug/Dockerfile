# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY internal ./internal
COPY cmd/webhook-debug ./cmd/webhook-debug
RUN go build -o /out/webhook ./cmd/webhook-debug

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /out/webhook /usr/local/bin/webhook
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/webhook"]
