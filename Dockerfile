# fishtty-server 多阶段构建
#
# 阶段 1：构建前端 PWA
# 阶段 2：构建 Go 后端（内嵌 PWA）
# 阶段 3：最小运行时镜像

# ── 阶段 1：前端构建 ──
FROM node:22-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable \
    && corepack prepare pnpm@9.15.9 --activate \
    && pnpm install --frozen-lockfile
COPY web/ .
RUN pnpm run build

# ── 阶段 2：Go 后端构建 ──
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/web/dist ./web/dist

ARG BUILD_VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.Version=${BUILD_VERSION}" \
    -o /fishtty-server ./cmd/server/

# ── 阶段 3：运行时 ──
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-builder /fishtty-server /fishtty-server
EXPOSE 8443
ENTRYPOINT ["/fishtty-server"]
CMD ["--listen", ":8443"]
