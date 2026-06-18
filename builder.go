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
