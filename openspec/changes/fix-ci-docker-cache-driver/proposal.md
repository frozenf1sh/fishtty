## Why

CI 的 Docker Build & Push job 始终失败，报错 `Cache export is not supported for the docker driver`。原因是 workflow 配置了 `cache-from: type=gha` 和 `cache-to: type=gha,mode=max`（GitHub Actions Cache 后端），但没有配置 `docker-container` driver 的 Buildx builder。默认的 `docker` driver 不支持 `type=gha` 缓存导出，导致每次 main 分支 push 都无法完成镜像构建和推送。

## What Changes

- 在 CI workflow 的 docker job 中，`docker/build-push-action@v6` 之前添加 `docker/setup-buildx-action@v3` 步骤，创建 `docker-container` driver 的 builder
- `cache-from` 和 `cache-to` 配置保持不变，新增的 builder 将使其正常工作

## Capabilities

### New Capabilities

- `ci-docker-build-cache`: CI pipeline 能够使用 GitHub Actions Cache 作为 Docker 构建缓存后端，加速镜像构建

### Modified Capabilities

<!-- 无现有 capability 被修改 -->

## Impact

- 影响文件：`.github/workflows/ci.yml`（仅 docker job 部分）
- 无 API、依赖变更
- 无 breaking changes
