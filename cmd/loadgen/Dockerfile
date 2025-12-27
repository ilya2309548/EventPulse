# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd/loadgen ./cmd/loadgen
RUN go build -o /out/loadgen ./cmd/loadgen

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /out/loadgen /usr/local/bin/loadgen
ENTRYPOINT ["/usr/local/bin/loadgen"]
