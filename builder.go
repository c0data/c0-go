package c0

import "fmt"

// Builder builds C0DATA documents in compact form. Methods chain. Names
// (file/group/header) reject control bytes — the first such error is recorded
// and reported by Err; record field values are byte-transparent and DLE-escaped
// automatically.
type Builder struct {
	buf []byte
	err error
}

// File writes a file/database scope (FS + name).
func (b *Builder) File(name string) *Builder {
	b.buf = append(b.buf, FS)
	b.writeName(name)
	return b
}

// Group writes a group/table scope (GS + name) with optional SOH headers (pass
// nil for none).
func (b *Builder) Group(name string, headers []string) *Builder {
	b.buf = append(b.buf, GS)
	b.writeName(name)
	if headers != nil {
		b.Header(headers)
	}
	return b
}

// Header writes a standalone SOH header.
func (b *Builder) Header(names []string) *Builder {
	b.buf = append(b.buf, SOH)
	for i, n := range names {
		if i > 0 {
			b.buf = append(b.buf, US)
		}
		b.writeName(n)
	}
	return b
}

// Record writes a record with positional fields. A Go string may carry any
// bytes, so binary fields are fine.
func (b *Builder) Record(fields ...string) *Builder {
	b.buf = append(b.buf, RS)
	for i, f := range fields {
		if i > 0 {
			b.buf = append(b.buf, US)
		}
		b.writeEscaped(f)
	}
	return b
}

// EOT writes an end-of-document marker.
func (b *Builder) EOT() *Builder {
	b.buf = append(b.buf, EOT)
	return b
}

// ETB writes a stream-mode commit marker.
func (b *Builder) ETB() *Builder {
	b.buf = append(b.buf, ETB)
	return b
}

// ETBPayload writes a stream-mode commit marker followed by an integrity
// payload. The payload may not contain control bytes (it is terminated by the
// next control code on read); the first such error is recorded and reported
// by Err, and the payload is not written.
func (b *Builder) ETBPayload(payload string) *Builder {
	b.buf = append(b.buf, ETB)
	for i := 0; i < len(payload); i++ {
		if payload[i] < 0x20 {
			if b.err == nil {
				b.err = fmt.Errorf("c0: ETB payload may not contain control bytes (got 0x%02x)", payload[i])
			}
			return b
		}
	}
	b.buf = append(b.buf, payload...)
	return b
}

// Nested writes a nested sub-structure: STX, whatever fn writes, ETX.
func (b *Builder) Nested(fn func(*Builder)) *Builder {
	b.buf = append(b.buf, STX)
	fn(b)
	b.buf = append(b.buf, ETX)
	return b
}

// Ref writes a reference to a named group (ENQ + name).
func (b *Builder) Ref(name string) *Builder {
	b.buf = append(b.buf, ENQ)
	b.buf = append(b.buf, name...)
	return b
}

// RefPath writes a path reference (group, record id, optional field): ENQ,
// STX, the segments separated by US, ETX.
func (b *Builder) RefPath(path ...string) *Builder {
	b.buf = append(b.buf, ENQ, STX)
	for i, seg := range path {
		if i > 0 {
			b.buf = append(b.buf, US)
		}
		b.buf = append(b.buf, seg...)
	}
	b.buf = append(b.buf, ETX)
	return b
}

// ListField writes a field whose value is a flat list (spec: "arrays are
// US-separated values inside STX/ETX"): US, STX, the items separated by US
// (each DLE-escaped), ETX. Read back with Record.List.
func (b *Builder) ListField(items ...string) *Builder {
	b.buf = append(b.buf, US, STX)
	for i, item := range items {
		if i > 0 {
			b.buf = append(b.buf, US)
		}
		b.writeEscaped(item)
	}
	b.buf = append(b.buf, ETX)
	return b
}

// Field writes a single field value (US + escaped value), for building a
// record's fields individually.
func (b *Builder) Field(value string) *Builder {
	b.buf = append(b.buf, US)
	b.writeEscaped(value)
	return b
}

// Section writes a document-mode section: GS repeated depth times, then the
// name.
func (b *Builder) Section(name string, depth int) *Builder {
	for i := 0; i < depth; i++ {
		b.buf = append(b.buf, GS)
	}
	b.writeName(name)
	return b
}

// Block writes a document-mode content block (RS + escaped text).
func (b *Builder) Block(text string) *Builder {
	b.buf = append(b.buf, RS)
	b.writeEscaped(text)
	return b
}

// Item writes a document-mode list item (US + escaped text).
func (b *Builder) Item(text string) *Builder {
	b.buf = append(b.buf, US)
	b.writeEscaped(text)
	return b
}

// Bytes returns the built buffer.
func (b *Builder) Bytes() []byte { return b.buf }

// Err returns the first error encountered (e.g. a control byte in a name).
func (b *Builder) Err() error { return b.err }

func (b *Builder) writeName(s string) {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 {
			if b.err == nil {
				b.err = fmt.Errorf("c0: names may not contain control bytes (got 0x%02x)", s[i])
			}
			return
		}
	}
	b.buf = append(b.buf, s...)
}

func (b *Builder) writeEscaped(s string) {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 {
			b.buf = append(b.buf, DLE)
		}
		b.buf = append(b.buf, s[i])
	}
}

// Build drives a fresh Builder and returns its bytes and any error.
func Build(fn func(*Builder)) ([]byte, error) {
	b := &Builder{}
	fn(b)
	return b.buf, b.err
}
