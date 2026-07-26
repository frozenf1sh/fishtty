// Package ringbuf 提供 128KB 环形字节缓冲区。
// 存储带序号（seq）的 PTY 输出 chunks，支持按序号增量重放。
//
// 每个 chunk 在缓冲区中的存储格式（大端序）：
//
//	[seq: 8B uint64][len: 4B uint32][data: len 字节]
//
// 总开销：每 chunk 12 字节。当写入超出容量时自动淘汰最旧 chunks。
package ringbuf

import (
	"encoding/binary"
	"fmt"
	"sync"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
	"github.com/frozenf1sh/fishpts/internal/domain"
)

const (
	capacity   = 128 * 1024 // 128 KB
	headerSize = 12          // 8B seq + 4B len
)

// 编译时验证接口实现
var _ domain.OutputBuffer = (*ChunkBuffer)(nil)

// chunkMeta 记录一个 chunk 的元数据。
type chunkMeta struct {
	seq    uint64
	offset int // data 在 buf 中的起始偏移（不含 header）
	length int // data 字节数
}

// ChunkBuffer 实现 domain.OutputBuffer。
// 所有方法并发安全。
type ChunkBuffer struct {
	mu       sync.Mutex
	buf      []byte
	writePos int
	chunks   []chunkMeta
	used     int
}

// New 分配新的 128KB 环形缓冲区。
func New() *ChunkBuffer {
	return &ChunkBuffer{buf: make([]byte, capacity)}
}

// ── domain.OutputBuffer 实现 ──

// Append 写入 data 并返回被淘汰的 chunk 数。
func (cb *ChunkBuffer) Append(seq uint64, data []byte) int {
	cb.mu.Lock(); defer cb.mu.Unlock()
	total := headerSize + len(data)
	if total > capacity { return 0 }

	evicted := 0
	for cb.used+total > capacity && len(cb.chunks) > 0 {
		cb.evictOldest(); evicted++
	}
	cb.writeWrapped(seq, data)
	cb.chunks = append(cb.chunks, chunkMeta{
		seq: seq, offset: (cb.writePos - total + headerSize + capacity) % capacity, length: len(data),
	})
	return evicted
}

// ReplayFrom 返回 seq > lastSeq 的所有 chunk。
func (cb *ChunkBuffer) ReplayFrom(lastSeq uint64) ([]*fishttyv1.DataChunk, uint64) {
	cb.mu.Lock(); defer cb.mu.Unlock()
	if len(cb.chunks) == 0 { return nil, 0 }

	start := -1
	for i, m := range cb.chunks {
		if m.seq > lastSeq { start = i; break }
	}
	if start == -1 { return nil, cb.chunks[len(cb.chunks)-1].seq }

	var out []*fishttyv1.DataChunk
	for i := start; i < len(cb.chunks); i++ {
		m := cb.chunks[i]
		out = append(out, &fishttyv1.DataChunk{Seq: m.seq, Data: cb.readData(m)})
	}
	return out, cb.chunks[start].seq
}

func (cb *ChunkBuffer) OldestSeq() uint64 {
	cb.mu.Lock(); defer cb.mu.Unlock()
	if len(cb.chunks) == 0 { return 0 }
	return cb.chunks[0].seq
}

func (cb *ChunkBuffer) NewestSeq() uint64 {
	cb.mu.Lock(); defer cb.mu.Unlock()
	if len(cb.chunks) == 0 { return 0 }
	return cb.chunks[len(cb.chunks)-1].seq
}

func (cb *ChunkBuffer) Len() int {
	cb.mu.Lock(); defer cb.mu.Unlock()
	return len(cb.chunks)
}

// ── 内部实现 ──

func (cb *ChunkBuffer) writeWrapped(seq uint64, data []byte) {
	total := headerSize + len(data)
	hdr := make([]byte, headerSize)
	binary.BigEndian.PutUint64(hdr[0:8], seq)
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(data)))

	if cb.writePos+total <= capacity {
		copy(cb.buf[cb.writePos:], hdr)
		copy(cb.buf[cb.writePos+headerSize:], data)
	} else {
		hdrEnd := cb.writePos + headerSize
		if hdrEnd <= capacity {
			copy(cb.buf[cb.writePos:], hdr)
			n1 := capacity - hdrEnd
			copy(cb.buf[hdrEnd:], data[:n1])
			copy(cb.buf[0:], data[n1:])
		} else {
			n1 := capacity - cb.writePos
			copy(cb.buf[cb.writePos:], hdr[:n1])
			rem := headerSize - n1
			copy(cb.buf[0:], hdr[n1:])
			copy(cb.buf[rem:], data)
		}
	}
	cb.writePos = (cb.writePos + total) % capacity
	cb.used += total
}

func (cb *ChunkBuffer) evictOldest() {
	if len(cb.chunks) == 0 { return }
	cb.used -= headerSize + cb.chunks[0].length
	cb.chunks = cb.chunks[1:]
}

func (cb *ChunkBuffer) readData(m chunkMeta) []byte {
	buf := make([]byte, m.length)
	if m.offset+m.length <= capacity {
		copy(buf, cb.buf[m.offset:m.offset+m.length])
	} else {
		n1 := capacity - m.offset
		copy(buf, cb.buf[m.offset:])
		copy(buf[n1:], cb.buf[:m.length-n1])
	}
	return buf
}

func (cb *ChunkBuffer) String() string {
	cb.mu.Lock(); defer cb.mu.Unlock()
	return fmt.Sprintf("ChunkBuffer{cap=%d,used=%d,chunks=%d}", capacity, cb.used, len(cb.chunks))
}
