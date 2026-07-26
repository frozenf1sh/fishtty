## 1. CI Workflow 修复

- [x] 1.1 在 `.github/workflows/ci.yml` 的 docker job 中，`docker/build-push-action@v6` 之前添加 `docker/setup-buildx-action@v3` 步骤
