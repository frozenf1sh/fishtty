//go:build ci

package fishpts

import "embed"

// WebDist CI 占位 — 测试环境不需要嵌入 PWA 文件。
var WebDist embed.FS
