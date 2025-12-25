# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd/ingest ./cmd/ingest
RUN go build -o /out/ingest ./cmd/ingest

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ingest /usr/local/bin/ingest
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ingest"]
