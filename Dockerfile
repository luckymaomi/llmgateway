# syntax=docker/dockerfile:1.7

FROM node:22.13.0-bookworm@sha256:fa54405993eaa6bab6b6e460f5f3e945a2e2f07942ba31c0e297a7d9c2041f62 AS web-build
WORKDIR /src/web
RUN npm install --global pnpm@10.33.0
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM golang:1.26.5-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-build /src/web/dist ./web/dist
ARG TARGETOS
ARG TARGETARCH
ARG RELEASE_VERSION=development
ARG RELEASE_REVISION=unknown
ARG RELEASE_BUILT_AT=unknown
RUN BUILD_LDFLAGS="-s -w -X github.com/luckymaomi/llmgateway/internal/buildinfo.version=${RELEASE_VERSION} -X github.com/luckymaomi/llmgateway/internal/buildinfo.revision=${RELEASE_REVISION} -X github.com/luckymaomi/llmgateway/internal/buildinfo.builtAt=${RELEASE_BUILT_AT}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -tags webembed -trimpath -ldflags="$BUILD_LDFLAGS" -o /out/llmgateway ./cmd/gateway && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="$BUILD_LDFLAGS" -o /out/llmgateway-dbtool ./cmd/dbtool && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="$BUILD_LDFLAGS" -o /out/llmgateway-healthcheck ./cmd/healthcheck

FROM scratch
ARG RELEASE_VERSION=development
ARG RELEASE_REVISION=unknown
LABEL org.opencontainers.image.title="LLMGateway" \
      org.opencontainers.image.version=$RELEASE_VERSION \
      org.opencontainers.image.revision=$RELEASE_REVISION \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/luckymaomi/llmgateway"
COPY --from=go-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=go-build /out/llmgateway /llmgateway
COPY --from=go-build /out/llmgateway-dbtool /llmgateway-dbtool
COPY --from=go-build /out/llmgateway-healthcheck /llmgateway-healthcheck
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/llmgateway"]
