# dev: Go toolchain used by `make test`, acceptance tests and tools.
FROM golang:1.26-alpine AS dev
RUN apk add --no-cache git
ENV GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
WORKDIR /work/services/core

FROM dev AS build
COPY services/core/go.mod services/core/go.sum ./
RUN go mod download
COPY services/core/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/core ./cmd/core

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 app
USER app
COPY --from=build /out/core /usr/local/bin/core
EXPOSE 8080
ENTRYPOINT ["core"]
