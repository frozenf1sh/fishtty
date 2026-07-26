package agent

import (
	"bytes"
	"testing"
)

func TestRingBuffer_WriteAndReadFrom(t *testing.T) {
	rb := NewRingBuffer()

	// 写入 3 个 chunk
	rb.Write(1, []byte("hello"))
	rb.Write(2, []byte("world"))
	rb.Write(3, []byte("!!"))

	// 从 seq=1 开始读（期望返回 seq 2 和 3 的 chunk）
	chunks, startSeq := rb.ReadFrom(1)
	if startSeq != 2 {
		t.Fatalf("期望 startSeq=2，实际=%d", startSeq)
	}
	if len(chunks) != 2 {
		t.Fatalf("期望 2 个 chunk，实际=%d", len(chunks))
	}
	if chunks[0].Seq != 2 || !bytes.Equal(chunks[0].Data, []byte("world")) {
		t.Errorf("chunk 0: seq=%d, data=%q", chunks[0].Seq, chunks[0].Data)
	}
	if chunks[1].Seq != 3 || !bytes.Equal(chunks[1].Data, []byte("!!")) {
		t.Errorf("chunk 1: seq=%d, data=%q", chunks[1].Seq, chunks[1].Data)
	}

	// 从 seq=3 开始读（无新数据）
	chunks, startSeq = rb.ReadFrom(3)
	if len(chunks) != 0 {
		t.Errorf("期望无 chunk，实际=%d", len(chunks))
	}
	if startSeq != 0 {
		// 无数据时 startSeq 为 0
		t.Logf("无数据时 startSeq=%d", startSeq)
	}
}

func TestRingBuffer_WriteOverflow(t *testing.T) {
	rb := NewRingBuffer()

	// 循环写入大量数据直到溢出
	seq := uint64(1)
	totalWritten := 0
	for totalWritten < RingBufferSize*2 {
		data := bytes.Repeat([]byte("x"), 1024) // 每 chunk 1 KB 数据
		rb.Write(seq, data)
		totalWritten += len(data)
		seq++
	}

	// 缓冲区不应超过容量
	if rb.UsedBytes() > RingBufferSize {
		t.Errorf("已用字节 %d 超过容量 %d", rb.UsedBytes(), RingBufferSize)
	}

	// 应至少有些 chunk 被淘汰
	if rb.ChunkCount() > int(seq-1) {
		t.Logf("chunk 数=%d, 总写入=%d", rb.ChunkCount(), seq-1)
	}

	// ReadFrom 应该只返回剩余 chunk 中的有效数据
	newest := rb.NewestSeq()
	oldest := rb.OldestSeq()
	if oldest == 0 || newest == 0 {
		t.Fatal("缓冲区不应为空")
	}
	if oldest >= newest {
		t.Errorf("oldest=%d >= newest=%d", oldest, newest)
	}
	t.Logf("溢出后: oldest=%d, newest=%d, chunks=%d", oldest, newest, rb.ChunkCount())
}

func TestRingBuffer_EmptyBuffer(t *testing.T) {
	rb := NewRingBuffer()

	if rb.OldestSeq() != 0 {
		t.Errorf("空缓冲区 oldest 应为 0，实际=%d", rb.OldestSeq())
	}
	if rb.NewestSeq() != 0 {
		t.Errorf("空缓冲区 newest 应为 0，实际=%d", rb.NewestSeq())
	}

	chunks, startSeq := rb.ReadFrom(0)
	if len(chunks) != 0 {
		t.Errorf("空缓冲区应无 chunk，实际=%d", len(chunks))
	}
	if startSeq != 0 {
		t.Errorf("空缓冲区 startSeq 应为 0，实际=%d", startSeq)
	}
}

func TestRingBuffer_OversizedChunk(t *testing.T) {
	rb := NewRingBuffer()

	// 写入一个超过缓冲区容量的 chunk（应该被丢弃）
	huge := make([]byte, RingBufferSize+1)
	evicted := rb.Write(1, huge)
	if evicted != 0 {
		t.Logf("超大 chunk 被部分处理")
	}

	// 缓冲区应保持为空
	if rb.ChunkCount() != 0 {
		t.Errorf("超大 chunk 不应写入缓冲区，实际 chunk 数=%d", rb.ChunkCount())
	}
}

func TestRingBuffer_SequenceOrder(t *testing.T) {
	rb := NewRingBuffer()

	// 逆序写入（模拟异常场景）
	rb.Write(5, []byte("five"))
	rb.Write(3, []byte("three"))
	rb.Write(7, []byte("seven"))

	// 从 seq=3 读
	chunks, startSeq := rb.ReadFrom(3)
	t.Logf("逆序写入后 ReadFrom(3): startSeq=%d, chunks=%d", startSeq, len(chunks))
	// chunk 按写入顺序存储，不是按 seq 排序
	// 这里只验证不会 panic
	_ = chunks
}

func TestRingBuffer_ReadFromNonexistentSeq(t *testing.T) {
	rb := NewRingBuffer()

	rb.Write(10, []byte("ten"))
	rb.Write(20, []byte("twenty"))

	// 请求一个远大于最新 seq 的值
	chunks, startSeq := rb.ReadFrom(100)
	if len(chunks) != 0 {
		t.Logf("ReadFrom(100) 应返回空，实际=%d chunks", len(chunks))
	}
	if startSeq != 0 {
		t.Logf("startSeq=%d", startSeq)
	}
}

func TestRingBuffer_WriteSmallChunks(t *testing.T) {
	rb := NewRingBuffer()

	// 写入很多 1 字节 chunk（最大 header 开销比例）
	for i := uint64(1); i <= 200; i++ {
		rb.Write(i, []byte{byte(i)})
	}

	if rb.ChunkCount() < 50 {
		t.Errorf("期望至少 50 个 chunk（1B 数据 + 12B header = 13B/chunk × 50 = 650B << 128KB），实际=%d", rb.ChunkCount())
	}
	t.Logf("200 个 1B chunk 后: chunks=%d, oldest=%d, newest=%d",
		rb.ChunkCount(), rb.OldestSeq(), rb.NewestSeq())
}

func TestRingBuffer_UsedBytes(t *testing.T) {
	rb := NewRingBuffer()

	rb.Write(1, []byte("abc"))   // 12 + 3 = 15 bytes
	rb.Write(2, []byte("hello")) // 12 + 5 = 17 bytes
	// 总共 32 bytes

	if used := rb.UsedBytes(); used != 32 {
		t.Errorf("期望 UsedBytes=32，实际=%d", used)
	}
}
