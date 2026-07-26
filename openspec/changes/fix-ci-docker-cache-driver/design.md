## Context

GitHub Actions runner 上的 `docker buildx` 默认使用 `docker` driver。`docker` driver 不支持 `type=gha` 缓存后端，需要 `docker-container` driver。当前 CI workflow 在 `docker/build-push-action@v6` 中直接使用了 `cache-from: type=gha` 和 `cache-to: type=gha,mode=max`，但没有事先配置 builder。

## Goals / Non-Goals

**Goals:**
- 修复 Docker Build & Push job，使其成功构建并推送镜像
- 保持 GHA Cache 作为缓存后端以加速构建

**Non-Goals:**
- 不更换缓存后端（如 registry、local）
- 不修改 Dockerfile 或构建逻辑

## Decisions

**Decision 1: 使用 `docker/setup-buildx-action@v3`**

- 这是 Docker 官方维护的 action，专门用于在 CI 中配置 Buildx builder
- 默认创建 `docker-container` driver 的 builder，完全支持 `type=gha` 缓存
- 业界标准做法，GitHub Actions 文档推荐

备选方案：
- 手动 `docker buildx create --driver docker-container` — 可以达到同样效果，但官方 action 封装了更多最佳实践（driver-opt、自动清理等）
- 移除 cache 配置 — 可以立即修复，但会失去构建缓存加速

## Risks / Trade-offs

- **风险**: `setup-buildx-action@v3` 也使用 Node 20，有 deprecation warning — **不影响功能**，只是 warning，等上游发布新版本即可
- **资源**: `docker-container` driver 会额外启动一个 BuildKit 容器 — 开销极小，在 GitHub Actions runner 上完全可接受
