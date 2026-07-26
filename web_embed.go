//go:build !ci

// Package fishpts 提供 fishtty 项目的嵌入式资源。
//
// 仅在非 CI 环境编译（go:build !ci）。
// CI 中 web/dist 尚未构建，由 web_embed_ci.go 提供空占位。
package fishpts

import "embed"

// WebDist 嵌入 web/dist 构建产物（PWA 静态文件）。
// 构建前需运行: cd web && pnpm build
//
//go:embed web/dist
var WebDist embed.FS
