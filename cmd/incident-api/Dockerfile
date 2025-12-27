# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd/incident-api ./cmd/incident-api
RUN go build -o /out/incident-api ./cmd/incident-api

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /out/incident-api /usr/local/bin/incident-api
EXPOSE 8091
ENTRYPOINT ["/usr/local/bin/incident-api"]
