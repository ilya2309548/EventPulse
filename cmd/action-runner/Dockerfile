# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd/action-runner ./cmd/action-runner
RUN go get ./cmd/action-runner
RUN go build -o /out/action-runner ./cmd/action-runner

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates curl docker-cli
COPY --from=build /out/action-runner /usr/local/bin/action-runner
EXPOSE 8092
ENTRYPOINT ["/usr/local/bin/action-runner"]
