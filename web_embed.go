// Package fishpts 提供 fishtty 项目的嵌入式资源。
package fishpts

import "embed"

// WebDist 嵌入 web/dist 构建产物（PWA 静态文件）。
// 构建前需运行: cd web && pnpm build
//
//go:embed web/dist
var WebDist embed.FS
