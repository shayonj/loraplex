FROM golang:1.25-bookworm AS builder
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /loraplex ./cmd/loraplex

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /loraplex /loraplex
ENTRYPOINT ["/loraplex"]
