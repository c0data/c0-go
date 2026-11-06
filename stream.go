package c0

import (
	"io"
	"os"
)

// StreamReader scans an append-only log for ETB commits (DESIGN.md "Stream
// Mode"). An unescaped ETB is always a commit, even inside open text or a
// level; such a block is damaged and yields nothing. The tail after the
// last commit is torn unless it holds only layout, notes, EOT or an EM region. Zero-copy:
// accessors return sub-slices of the buffer.
type StreamReader struct {
	buf          []byte
	committedEnd int
	blocks       [][2]int // [start, end) of each committed block (ETB excluded)
	damaged      []int    // indexes of damaged blocks
	torn         bool
}

// NewStreamReader scans buf for commits. Scanning never fails: malformed
// structure inside a committed block is reported when the block is read
// (Document, Body, Records).
func NewStreamReader(buf []byte) *StreamReader {
	s := &StreamReader{buf: buf}
	s.scan()
	return s
}

// ReadStream reads a log file and scans it.
func ReadStream(path string) (*StreamReader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return NewStreamReader(data), nil
}

// CommittedEnd returns the offset just past the last ETB and its payload.
func (s *StreamReader) CommittedEnd() int { return s.committedEnd }

// Torn reports whether data trails the last commit.
func (s *StreamReader) Torn() bool { return s.torn }

// Committed returns the committed region.
func (s *StreamReader) Committed() []byte { return s.buf[:s.committedEnd] }

// Tail returns the bytes after the last commit.
func (s *StreamReader) Tail() []byte { return s.buf[s.committedEnd:] }

// BlockCount returns the number of committed blocks.
func (s *StreamReader) BlockCount() int { return len(s.blocks) }

// Block returns committed block i (marker and payload excluded).
func (s *StreamReader) Block(i int) []byte { return s.buf[s.blocks[i][0]:s.blocks[i][1]] }

// Blocks returns all committed blocks.
func (s *StreamReader) Blocks() [][]byte {
	out := make([][]byte, len(s.blocks))
	for i := range s.blocks {
		out[i] = s.Block(i)
	}
	return out
}

// Damaged returns the indexes of blocks cut by an ETB inside open text or a
// level.
func (s *StreamReader) Damaged() []int { return s.damaged }

// IsDamaged reports whether block i is damaged.
func (s *StreamReader) IsDamaged(i int) bool {
	for _, d := range s.damaged {
		if d == i {
			return true
		}
	}
	return false
}

// Document reads the committed region (undamaged blocks only, markers and
// payloads removed) as a document.
func (s *StreamReader) Document() (*Document, error) { return ParseDocument(s.undamaged()) }

// Body returns the committed region's body.
func (s *StreamReader) Body() (*Body, error) {
	d, err := s.Document()
	if err != nil {
		return nil, err
	}
	return d.Body(), nil
}

// Records returns the committed records of every group, in order.
func (s *StreamReader) Records() ([]*Record, error) {
	body, err := s.Body()
	if err != nil {
		return nil, err
	}
	var out []*Record
	for _, g := range body.Groups() {
		out = append(out, g.Records()...)
	}
	return out, nil
}

// undamaged concatenates the undamaged committed blocks.
func (s *StreamReader) undamaged() []byte {
	var out []byte
	for i, blk := range s.blocks {
		if !s.IsDamaged(i) {
			out = append(out, s.buf[blk[0]:blk[1]]...)
		}
	}
	return out
}

func (s *StreamReader) scan() {
	buf := s.buf
	n := len(buf)
	pos := 0
	blockStart := 0
	inText := false
	depth := 0
	for pos < n {
		b := buf[pos]
		if inText {
			switch b {
			case DLE:
				pos += 2
				continue
			case ETX:
				inText = false
			case ETB:
				s.commit(blockStart, pos, true)
				pos = s.payloadEnd(pos + 1)
				blockStart = pos
				inText = false
				depth = 0
				continue
			case SYN, EOT, EM:
				// framing cuts the text; the block is torn/damaged at the next commit
				inText = false
				depth = 0
				if b == SYN {
					blockStart = pos
				}
			}
			pos++
			continue
		}
		switch b {
		case STX:
			inText = true
		case DLE:
			depth++
		case CAN:
			if depth > 0 {
				depth--
			}
		case ETB:
			s.commit(blockStart, pos, depth > 0)
			pos = s.payloadEnd(pos + 1)
			blockStart = pos
			depth = 0
			continue
		case SYN:
			if pos > 0 {
				// tears the uncommitted bytes before it; a new document begins
				blockStart = pos
				depth = 0
			}
		}
		pos++
	}
	s.torn = s.tornTail()
}

func (s *StreamReader) commit(start, stop int, damaged bool) {
	s.blocks = append(s.blocks, [2]int{start, stop})
	if damaged {
		s.damaged = append(s.damaged, len(s.blocks)-1)
	}
}

// payloadEnd skips an ETB's payload and sets committedEnd (which excludes
// layout after the payload).
func (s *StreamReader) payloadEnd(pos int) int {
	buf := s.buf
	n := len(buf)
	p := scanLayout(buf, pos, n)
	if p < n && buf[p] >= 0x20 {
		p = scanPlain(buf, p, n)
		// trailing spaces of the payload are layout
		for p > pos && buf[p-1] == SP {
			p--
		}
	} else {
		p = pos // no payload: layout after ETB belongs to the next block
	}
	s.committedEnd = p
	return p
}

// tornTail reports whether the tail is more than layout, notes and EOT.
func (s *StreamReader) tornTail() (torn bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(scanPanic); !ok {
				panic(r)
			}
			torn = true
		}
	}()
	buf := s.buf
	n := len(buf)
	pos := s.committedEnd
	for {
		pos = scanLayout(buf, pos, n)
		if pos >= n {
			return false
		}
		switch buf[pos] {
		case EOT:
			pos++
		case EM:
			return false // the raw region is not log data
		case BEL:
			p2 := scanLayout(buf, pos+1, n)
			if p2 < n && buf[p2] < 0x20 && buf[p2] != STX {
				return true // a dead element is data
			}
			pos = scanComment(buf, pos, n)
		default:
			return true
		}
	}
}

// LastCommitEnd returns the position just past the last commit, found by
// scanning backward (an ETB behind an odd run of DLEs is escaped). A
// shortcut for well-formed input; the forward scan is authoritative.
func LastCommitEnd(buf []byte) int {
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] != ETB {
			continue
		}
		dles := 0
		for j := i - 1; j >= 0 && buf[j] == DLE; j-- {
			dles++
		}
		if dles%2 == 0 {
			return scanPlain(buf, i+1, len(buf))
		}
	}
	return 0
}

// RepairStream truncates an uncommitted tail so the file ends at a commit.
func RepairStream(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	r := NewStreamReader(data)
	if r.Torn() {
		return os.Truncate(path, int64(r.CommittedEnd()))
	}
	return nil
}

func commitBytes(fn func(*Builder)) ([]byte, error) {
	b := &Builder{}
	fn(b)
	b.ETB()
	return b.buf, b.err
}

// StreamWriter appends committed blocks to an io.Writer. Each Start, Record,
// Header, Group or Batch is one block, written with its ETB in a single
// write. For files, prefer OpenLog (torn-tail repair + per-commit fsync).
type StreamWriter struct {
	w io.Writer
}

// NewStreamWriter returns a StreamWriter over w.
func NewStreamWriter(w io.Writer) *StreamWriter { return &StreamWriter{w: w} }

// Start begins a new log: SYN and a heading declaring shape stream,
// committed.
func (sw *StreamWriter) Start() error {
	return sw.Batch(func(b *Builder) { b.SYN().Heading("shape", "stream") })
}

// Record appends one positional record as a committed block.
func (sw *StreamWriter) Record(fields ...string) error {
	return sw.Batch(func(b *Builder) { b.Record(fields...) })
}

// Header appends a header row as a committed block.
func (sw *StreamWriter) Header(names ...string) error {
	return sw.Batch(func(b *Builder) { b.Header(names...) })
}

// Group appends a group line (and optional header row) as a committed block.
func (sw *StreamWriter) Group(name string, headers []string) error {
	return sw.Batch(func(b *Builder) { b.Group(name, headers) })
}

// Batch appends whatever fn writes under a single commit (an atomic batch).
func (sw *StreamWriter) Batch(fn func(*Builder)) error {
	data, err := commitBytes(fn)
	if err != nil {
		return err
	}
	_, err = sw.w.Write(data)
	return err
}

// FileLog is an append-only log file with ETB commits. Each commit is
// written in one call and, when sync is set, fsync'd.
type FileLog struct {
	f    *os.File
	sync bool
}

// OpenLog opens an append-only log file, repairing any torn tail first
// (truncating to the last commit). Each commit is fsync'd.
func OpenLog(path string) (*FileLog, error) { return OpenLogSync(path, true) }

// OpenLogSync is OpenLog with explicit control over per-commit fsync.
func OpenLogSync(path string, sync bool) (*FileLog, error) {
	if _, err := os.Stat(path); err == nil {
		if err := RepairStream(path); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileLog{f: f, sync: sync}, nil
}

// Start begins a new log: SYN and a heading declaring shape stream,
// committed.
func (l *FileLog) Start() error {
	return l.Batch(func(b *Builder) { b.SYN().Heading("shape", "stream") })
}

// Record appends one positional record as a committed block.
func (l *FileLog) Record(fields ...string) error {
	return l.Batch(func(b *Builder) { b.Record(fields...) })
}

// Header appends a header row as a committed block.
func (l *FileLog) Header(names ...string) error {
	return l.Batch(func(b *Builder) { b.Header(names...) })
}

// Group appends a group line (and optional header row) as a committed block.
func (l *FileLog) Group(name string, headers []string) error {
	return l.Batch(func(b *Builder) { b.Group(name, headers) })
}

// Batch appends whatever fn writes under a single commit (an atomic batch).
func (l *FileLog) Batch(fn func(*Builder)) error {
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
