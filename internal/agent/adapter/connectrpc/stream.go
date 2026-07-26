// Package connectrpc 提供 Connect-RPC 双向流的适配实现。
// 将 Connect-Go 的 BidiStream 包装为 domain.StreamConn 接口。
package connectrpc

import (
	"connectrpc.com/connect"
	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/frozenf1sh/fishpts/internal/domain"
)

// 编译时验证接口
var _ domain.StreamConn = (*StreamAdapter)(nil)

// StreamAdapter 将 Connect-Go BidiStream 适配为 domain.StreamConn。
type StreamAdapter struct {
	stream *connect.BidiStreamForClient[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage]
}

// NewStreamAdapter 创建适配器。
func NewStreamAdapter(stream *connect.BidiStreamForClient[fishttyv1.TunnelMessage, fishttyv1.TunnelMessage]) *StreamAdapter {
	return &StreamAdapter{stream: stream}
}

func (a *StreamAdapter) SendMessage(msg *fishttyv1.TunnelMessage) error {
	return a.stream.Send(msg)
}

func (a *StreamAdapter) ReceiveMessage() (*fishttyv1.TunnelMessage, error) {
	return a.stream.Receive()
}

func (a *StreamAdapter) CloseRequest() error {
	return a.stream.CloseRequest()
}
