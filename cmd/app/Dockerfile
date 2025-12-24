# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache build-base
COPY go.mod ./
COPY internal ./internal
COPY cmd/app ./cmd/app
RUN go build -o /out/app ./cmd/app

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/app /usr/local/bin/app
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=2s --retries=3 CMD wget -qO- http://localhost:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/app"]
