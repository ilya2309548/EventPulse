# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd/rule-engine ./cmd/rule-engine
RUN go build -o /out/rule-engine ./cmd/rule-engine

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /out/rule-engine /usr/local/bin/rule-engine
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/rule-engine"]
