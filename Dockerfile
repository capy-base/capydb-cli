FROM golang:1.26-alpine AS builder

WORKDIR /build

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG BUILD_VERSION=dev
ARG BUILD_DATE=unknown
ARG GIT_COMMIT=none

RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${BUILD_VERSION} -X main.date=${BUILD_DATE} -X main.commit=${GIT_COMMIT} -X main.builtBy=docker" \
    -o capydb \
    ./cmd/capydb

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -s /bin/sh capydb

COPY --from=builder /build/capydb /usr/local/bin/capydb

USER capydb
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/capydb"]
CMD ["--help"]
