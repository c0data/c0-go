package c0

// Table is a zero-copy accessor for a tabular C0DATA group.
type Table struct {
	buf     []byte
	name    [2]int
	headers [][2]int
	records [][2]int
}

// NewTable indexes a tabular group at the start of buf.
func NewTable(buf []byte) *Table { return NewTableAt(buf, 0) }

// NewTableAt indexes a tabular group starting at offset (e.g. a group's GS).
func NewTableAt(buf []byte, offset int) *Table {
	t := &Table{buf: buf}
	t.index(offset)
	return t
}

// Name returns the group/table name as a sub-slice of the buffer.
func (t *Table) Name() []byte { return t.buf[t.name[0]:t.name[1]] }

// HeaderCount returns the number of header fields.
func (t *Table) HeaderCount() int { return len(t.headers) }

// Header returns header field i.
func (t *Table) Header(i int) []byte { return t.buf[t.headers[i][0]:t.headers[i][1]] }

// Headers returns all header names.
func (t *Table) Headers() [][]byte {
	hs := make([][]byte, len(t.headers))
	for i, h := range t.headers {
		hs[i] = t.buf[h[0]:h[1]]
	}
	return hs
}

// RecordCount returns the number of records.
func (t *Table) RecordCount() int { return len(t.records) }

// Record returns record i.
func (t *Table) Record(i int) *Record {
	r := t.records[i]
	return &Record{buf: t.buf, start: r[0], end: r[1]}
}

// Records returns all records.
func (t *Table) Records() []*Record {
	rs := make([]*Record, len(t.records))
	for i := range t.records {
		rs[i] = t.Record(i)
	}
	return rs
}

func (t *Table) index(offset int) {
	buf := t.buf
	n := len(buf)
	pos := offset
	if pos >= n {
		return
	}

	// Optional GS + name.
	if buf[pos] == GS {
		pos++
		start := pos
		for pos < n && buf[pos] >= 0x20 {
			pos++
		}
		t.name = [2]int{start, pos}
	}

	// Skip ETB commit markers and their payloads (stream-mode framing).
	for pos < n && buf[pos] == ETB {
		pos++
		for pos < n && buf[pos] >= 0x20 {
			pos++
		}
	}

	// Optional SOH header.
	if pos < n && buf[pos] == SOH {
		pos++
		fieldStart := pos
		for pos < n {
			b := buf[pos]
			if b == US {
				t.headers = append(t.headers, [2]int{fieldStart, pos})
				pos++
				fieldStart = pos
			} else if b < 0x20 {
				t.headers = append(t.headers, [2]int{fieldStart, pos})
				break
			} else {
				pos++
			}
		}
		if pos >= n {
			t.headers = append(t.headers, [2]int{fieldStart, pos})
		}
	}

	// Records.
	for pos < n {
		b := buf[pos]
		if b == GS || b == FS || b == EOT || b == ETX {
			break
		}
		if b == RS {
			pos++
			recStart := pos
			for pos < n {
				c := buf[pos]
				if c == RS || c == GS || c == FS || c == EOT || c == ETX || c == ETB {
					break
				}
				switch {
				case c == DLE:
					pos += 2
				case c == STX:
					pos = skipNested(buf, pos, n)
				default:
					pos++
				}
			}
			end := pos
			if end > n {
				end = n
			}
			t.records = append(t.records, [2]int{recStart, end})
		} else {
			pos++
		}
	}
}

// Record is a zero-copy accessor for a single record within a table.
type Record struct {
	buf        []byte
	start, end int
}

// Field returns field n. Respects DLE escaping and STX/ETX nesting; the field
// is raw (use Value to decode escapes).
func (r *Record) Field(n int) []byte {
	buf := r.buf
	pos := r.start
	idx := 0
	fieldStart := pos
	for pos < r.end {
		b := buf[pos]
		switch {
		case b == US:
			if idx == n {
				return buf[fieldStart:pos]
			}
			idx++
			pos++
			fieldStart = pos
		case b == DLE:
			pos += 2
		case b == STX:
			pos = skipNested(buf, pos, r.end)
		default:
			pos++
		}
	}
	if idx == n {
		end := pos
		if end > r.end {
			end = r.end
		}
		return buf[fieldStart:end]
	}
	return nil
}

// FieldCount returns the number of fields (N separators yield N+1 fields).
func (r *Record) FieldCount() int {
	count := 1
	pos := r.start
	for pos < r.end {
		switch r.buf[pos] {
		case US:
			count++
			pos++
		case DLE:
			pos += 2
		case STX:
			pos = skipNested(r.buf, pos, r.end)
		default:
			pos++
		}
	}
	return count
}

// Fields returns all fields as sub-slices.
func (r *Record) Fields() [][]byte {
	n := r.FieldCount()
	fs := make([][]byte, n)
	for i := 0; i < n; i++ {
		fs[i] = r.Field(i)
	}
	return fs
}

// Value returns field n with DLE escapes decoded.
func (r *Record) Value(n int) []byte { return Unescape(r.Field(n)) }

// Values returns all logical field values (escapes decoded).
func (r *Record) Values() [][]byte {
	fs := r.Fields()
	vs := make([][]byte, len(fs))
	for i, f := range fs {
		vs[i] = Unescape(f)
	}
	return vs
}

// List returns field n as a flat list (see Builder.ListField): the items of
// its STX/ETX scope, split on top-level US, with escapes decoded. A field that
// is not a list comes back as a single item; an empty list scope yields an
// empty slice.
func (r *Record) List(n int) [][]byte {
	raw := r.Field(n)
	if len(raw) == 0 || raw[0] != STX {
		return [][]byte{Unescape(raw)}
	}
	stop := len(raw)
	if stop > 1 && raw[stop-1] == ETX {
		stop--
	}
	items := [][]byte{}
	if stop <= 1 {
		return items
	}
	pos := 1
	itemStart := pos
	for pos < stop {
		switch raw[pos] {
		case US:
			items = append(items, Unescape(raw[itemStart:pos]))
			pos++
			itemStart = pos
		case DLE:
			pos += 2
		case STX:
			pos = skipNested(raw, pos, stop)
		default:
			pos++
		}
	}
	items = append(items, Unescape(raw[itemStart:stop]))
	return items
}

// Raw returns the entire record's bytes.
func (r *Record) Raw() []byte { return r.buf[r.start:r.end] }
