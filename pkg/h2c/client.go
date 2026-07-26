// Package h2c 提供 HTTP/2 Cleartext（非加密 HTTP/2）客户端配置。
// 用于 Agent 通过 http://（非 TLS）连接 Server 时建立 HTTP/2 双向流。
package h2c

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"

	"golang.org/x/net/http2"
)

// NewClient 创建一个支持 h2c 的 HTTP 客户端。
// 当 serverAddr 以 https:// 开头时返回标准客户端；
// 以 http:// 开头时配置 HTTP/2 非加密传输层。
func NewClient(serverAddr string) *http.Client {
	if strings.HasPrefix(serverAddr, "https://") {
		return &http.Client{}
	}

	// HTTP/2 over cleartext：
	// http2.Transport 在 AllowHTTP=true 时发送 HTTP/2 升级请求。
	// DialTLSContext 被 http2.Transport 在非 TLS 模式下调用，
	// 这里直接拨号 TCP，不做 TLS 握手。
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: transport}
}
