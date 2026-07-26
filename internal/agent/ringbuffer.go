// Package agent 提供 fishtty-agent 的核心实现。
// ringbuffer.go — 128 KB 环形缓冲区，用于存储 PTY 输出历史。
//
// 每个 chunk 的存储格式（大端序）：
//
//	[seq: 8 字节 uint64][len: 4 字节 uint32][data: len 字节]
//
// 总开销：每 chunk 12 字节。
// 当写入新 chunk 会超出容量时，自动淘汰最旧的 chunks。
package agent

import (
	"encoding/binary"
	"fmt"
	"sync"

	fishttyv1 "github.com/frozenf1sh/fishpts/gen/fishtty/v1"
)

const (
	// RingBufferSize 环形缓冲区总容量（字节）。
	RingBufferSize = 128 * 1024 // 128 KB

	// headerSize 每个 chunk 的头部大小（8B seq + 4B len）。
	headerSize = 12
)

// chunkMeta 记录环形缓冲区中一个 chunk 的元数据。
type chunkMeta struct {
	seq    uint64 // 序列号
	offset int    // 数据在 buf 中的起始偏移（不含 header）
	length int    // 数据字节数（不含 header）
}

// RingBuffer 是一个固定大小的环形字节缓冲区，
// 存储带序列号的 PTY 输出 chunks。
// 当缓冲区满时，最旧的 chunks 会被覆盖。
// 所有方法都是并发安全的。
type RingBuffer struct {
	mu sync.Mutex

	buf      []byte      // 底层字节存储，容量 = RingBufferSize
	writePos int         // 下一个 chunk header 的写入位置
	chunks   []chunkMeta // 当前缓冲区中的 chunk 元数据（按 seq 升序）
	capacity int         // 总容量
	used     int         // 已使用字节数（含 header）
}

// NewRingBuffer 分配并返回一个新的 128 KB 环形缓冲区。
func NewRingBuffer() *RingBuffer {
	return &RingBuffer{
		buf:      make([]byte, RingBufferSize),
		capacity: RingBufferSize,
	}
}

// Write 将 data 以 seq 为序列号写入环形缓冲区。
// 如果 data 大小（含 header）超过缓冲区总容量，则丢弃该 chunk。
// 返回被覆盖（淘汰）的 chunk 数量。
func (rb *RingBuffer) Write(seq uint64, data []byte) int {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	totalSize := headerSize + len(data)

	// 单个 chunk 超过总容量 — 无法存储，直接丢弃
	if totalSize > rb.capacity {
		return 0
	}

	// 淘汰旧 chunks 直到有足够空间
	evicted := 0
	for rb.used+totalSize > rb.capacity && len(rb.chunks) > 0 {
		rb.evictOldest()
		evicted++
	}

	// 写入 header + data
	rb.writeWrapped(seq, data)

	// 记录 chunk 元数据
	// data 的起始位置在 header 之后
	dataOffset := (rb.writePos + headerSize) % rb.capacity
	// 但 writeWrapped 已更新 writePos，需要回退计算
	// 简化：writeWrapped 返回 data 的实际写入起始偏移
	// 这里我们通过 writePos 反推
	prevWritePos := (rb.writePos - totalSize) % rb.capacity
	if prevWritePos < 0 {
		prevWritePos += rb.capacity
	}
	dataOffset = (prevWritePos + headerSize) % rb.capacity

	rb.chunks = append(rb.chunks, chunkMeta{
		seq:    seq,
		offset: dataOffset,
		length: len(data),
	})

	return evicted
}

// writeWrapped 将 seq + data 写入环形缓冲区，自动处理回绕。
// 调用者必须持有 rb.mu。
func (rb *RingBuffer) writeWrapped(seq uint64, data []byte) {
	totalSize := headerSize + len(data)
	header := make([]byte, headerSize)
	binary.BigEndian.PutUint64(header[0:8], seq)
	binary.BigEndian.PutUint32(header[8:12], uint32(len(data)))

	firstPart := headerSize + len(data)
	if rb.writePos+firstPart <= rb.capacity {
		// 不需要回绕：直接写入 header + data
		copy(rb.buf[rb.writePos:], header)
		copy(rb.buf[rb.writePos+headerSize:], data)
	} else {
		// 需要回绕：header + data 跨缓冲区末尾
		headerEndPos := rb.writePos + headerSize
		if headerEndPos <= rb.capacity {
			// header 不跨边界
			copy(rb.buf[rb.writePos:], header)
			// data 跨边界
			copy(rb.buf[headerEndPos:], data[:rb.capacity-headerEndPos])
			copy(rb.buf[0:], data[rb.capacity-headerEndPos:])
		} else {
			// header 跨边界（data 总在 header 之后，必然也跨边界）
			headerPart1 := rb.capacity - rb.writePos
			copy(rb.buf[rb.writePos:], header[:headerPart1])
			remaining := headerSize - headerPart1
			copy(rb.buf[0:], header[headerPart1:])
			copy(rb.buf[remaining:], data)
		}
	}

	rb.writePos = (rb.writePos + totalSize) % rb.capacity
	rb.used += totalSize
}

// evictOldest 淘汰最旧的 chunk。调用者必须持有 rb.mu。
func (rb *RingBuffer) evictOldest() {
	if len(rb.chunks) == 0 {
		return
	}
	oldest := rb.chunks[0]
	rb.chunks = rb.chunks[1:]
	rb.used -= headerSize + oldest.length
}

// ReadFrom 返回所有 seq > lastSeq 的 chunk 所对应的 DataChunk 列表。
// 如果 lastSeq 对应数据已被淘汰，则从缓冲区中最旧的可用 chunk 开始返回。
// startSeq 是实际返回的第一个 chunk 的 seq。
func (rb *RingBuffer) ReadFrom(lastSeq uint64) (chunks []*fishttyv1.DataChunk, startSeq uint64) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(rb.chunks) == 0 {
		return nil, 0
	}

	// 找到第一个 seq > lastSeq 的 chunk
	startIdx := -1
	for i, m := range rb.chunks {
		if m.seq > lastSeq {
			startIdx = i
			break
		}
	}

	if startIdx == -1 {
		// 所有 chunks 的 seq <= lastSeq，无需重放
		return nil, rb.chunks[len(rb.chunks)-1].seq
	}

	startSeq = rb.chunks[startIdx].seq

	for i := startIdx; i < len(rb.chunks); i++ {
		m := rb.chunks[i]
		data := rb.readData(m)
		chunks = append(chunks, &fishttyv1.DataChunk{
			Seq:  m.seq,
			Data: data,
		})
	}

	return chunks, startSeq
}

// readData 从环形缓冲区中读取 chunk 数据。
// 调用者必须持有 rb.mu。
func (rb *RingBuffer) readData(m chunkMeta) []byte {
	buf := make([]byte, m.length)
	if m.offset+m.length <= rb.capacity {
		copy(buf, rb.buf[m.offset:m.offset+m.length])
	} else {
		// 数据跨边界
		part1 := rb.capacity - m.offset
		copy(buf, rb.buf[m.offset:])
		copy(buf[part1:], rb.buf[:m.length-part1])
	}
	return buf
}

// OldestSeq 返回缓冲区中最旧 chunk 的序列号。
// 如果缓冲区为空，返回 0。
func (rb *RingBuffer) OldestSeq() uint64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(rb.chunks) == 0 {
		return 0
	}
	return rb.chunks[0].seq
}

// NewestSeq 返回缓冲区中最新 chunk 的序列号。
// 如果缓冲区为空，返回 0。
func (rb *RingBuffer) NewestSeq() uint64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(rb.chunks) == 0 {
		return 0
	}
	return rb.chunks[len(rb.chunks)-1].seq
}

// UsedBytes 返回缓冲区当前使用的字节数（含 header 开销）。
func (rb *RingBuffer) UsedBytes() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.used
}

// ChunkCount 返回当前缓冲区中存储的 chunk 数量。
func (rb *RingBuffer) ChunkCount() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return len(rb.chunks)
}

// String 返回 RingBuffer 状态的可读表示。
func (rb *RingBuffer) String() string {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return fmt.Sprintf("RingBuffer{cap=%d, used=%d, chunks=%d, oldest=%d, newest=%d}",
		rb.capacity, rb.used, len(rb.chunks),
		rb.oldestSeqLocked(), rb.newestSeqLocked())
}

func (rb *RingBuffer) oldestSeqLocked() uint64 {
	if len(rb.chunks) == 0 {
		return 0
	}
	return rb.chunks[0].seq
}

func (rb *RingBuffer) newestSeqLocked() uint64 {
	if len(rb.chunks) == 0 {
		return 0
	}
	return rb.chunks[len(rb.chunks)-1].seq
}
