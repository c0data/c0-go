package c0

import (
	"io"
	"os"
)

// StreamReader scans an append-only log for ETB commit markers and exposes only
// the committed region. Zero-copy: accessors return sub-slices of the buffer.
type StreamReader struct {
	buf          []byte
	committedEnd int
	torn         bool
	commits      [][2]int // {etb offset, end of payload}
}

// NewStreamReader scans buf for ETB commits.
func NewStreamReader(buf []byte) *StreamReader {
	s := &StreamReader{buf: buf}
	s.scan()
	return s
}

// CommittedEnd returns the offset just past the last commit marker and payload.
func (s *StreamReader) CommittedEnd() int { return s.committedEnd }

// Torn reports whether uncommitted bytes trail the last commit marker.
func (s *StreamReader) Torn() bool { return s.torn }

// Committed returns the committed region.
func (s *StreamReader) Committed() []byte { return s.buf[:s.committedEnd] }

// Tail returns the uncommitted trailing bytes.
func (s *StreamReader) Tail() []byte { return s.buf[s.committedEnd:] }

// BlockCount returns the number of committed blocks.
func (s *StreamReader) BlockCount() int { return len(s.commits) }

// Block returns committed block i (marker and payload excluded).
func (s *StreamReader) Block(i int) []byte {
	start := 0
	if i > 0 {
		start = s.commits[i-1][1]
	}
	return s.buf[start:s.commits[i][0]]
}

// Blocks returns all committed blocks.
func (s *StreamReader) Blocks() [][]byte {
	bs := make([][]byte, len(s.commits))
	for i := range s.commits {
		bs[i] = s.Block(i)
	}
	return bs
}

// Table reads the committed region as a Table.
func (s *StreamReader) Table() *Table { return NewTable(s.Committed()) }

func (s *StreamReader) scan() {
	buf := s.buf
	n := len(buf)
	pos := 0
	lastEnd := 0
	for pos < n {
		b := buf[pos]
		switch {
		case b == DLE:
			pos += 2
		case b == STX:
			pos = skipNested(buf, pos, n)
		case b == ETB:
			etb := pos
			pos++
			for pos < n && buf[pos] >= 0x20 {
				pos++
			}
			s.commits = append(s.commits, [2]int{etb, pos})
			lastEnd = pos
		default:
			pos++
		}
	}
	if lastEnd > n {
		lastEnd = n
	}
	s.committedEnd = lastEnd
	s.torn = lastEnd < n
}

func commitBytes(fn func(*Builder)) ([]byte, error) {
	b := &Builder{}
	fn(b)
	b.ETB()
	return b.buf, b.err
}

// StreamWriter appends ETB-committed blocks to any io.Writer. Each block and its
// ETB are written as one unit. For files, prefer OpenLog (torn-tail repair +
// per-commit fsync).
type StreamWriter struct {
	w io.Writer
}

// NewStreamWriter returns a StreamWriter over w.
func NewStreamWriter(w io.Writer) *StreamWriter { return &StreamWriter{w: w} }

// Record appends one record as a committed block.
func (sw *StreamWriter) Record(fields ...string) error {
	return sw.emit(func(b *Builder) { b.Record(fields...) })
}

// Header appends an SOH header as a committed block.
func (sw *StreamWriter) Header(names []string) error {
	return sw.emit(func(b *Builder) { b.Header(names) })
}

// Batch appends several records under a single commit (an atomic batch).
func (sw *StreamWriter) Batch(fn func(*Builder)) error { return sw.emit(fn) }

func (sw *StreamWriter) emit(fn func(*Builder)) error {
	data, err := commitBytes(fn)
	if err != nil {
		return err
	}
	_, err = sw.w.Write(data)
	return err
}

// FileLog is an append-only log file with ETB commits. Each commit is flushed
// and, when sync is set, fsync'd.
type FileLog struct {
	f    *os.File
	sync bool
}

// OpenLog opens an append-only log file, repairing any torn tail first
// (truncating to the last commit). Each commit is fsync'd.
func OpenLog(path string) (*FileLog, error) { return OpenLogSync(path, true) }

// OpenLogSync is OpenLog with explicit control over per-commit fsync.
func OpenLogSync(path string, sync bool) (*FileLog, error) {
	if data, err := os.ReadFile(path); err == nil {
		r := NewStreamReader(data)
		if r.Torn() {
			if err := os.Truncate(path, int64(r.CommittedEnd())); err != nil {
				return nil, err
			}
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileLog{f: f, sync: sync}, nil
}

// Record appends one record as a committed block.
func (l *FileLog) Record(fields ...string) error {
	return l.commit(func(b *Builder) { b.Record(fields...) })
}

// Header appends an SOH header as a committed block.
func (l *FileLog) Header(names []string) error {
	return l.commit(func(b *Builder) { b.Header(names) })
}

// Batch appends several records under a single commit (an atomic batch).
func (l *FileLog) Batch(fn func(*Builder)) error { return l.commit(fn) }

func (l *FileLog) commit(fn func(*Builder)) error {
	data, err := commitBytes(fn)
	if err != nil {
		return err
	}
	if _, err := l.f.Write(data); err != nil {
		return err
	}
	if l.sync {
		return l.f.Sync()
	}
	return nil
}

// Close closes the log file.
func (l *FileLog) Close() error { return l.f.Close() }

// ReadLog reads a log file into a byte buffer; wrap it with NewStreamReader.
func ReadLog(path string) ([]byte, error) { return os.ReadFile(path) }
