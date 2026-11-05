package c0

// Builder writes C0DATA in canonical compact form. Methods chain.
//
//	buf, err := c0.Build(func(b *c0.Builder) {
//		b.SYN().Heading("shape", "tabular")
//		b.Group("users", []string{"name", "amount"})
//		b.Record("Alice", "1502.30")
//		b.Record("Bob", "340.00")
//		b.EOT()
//	})
//
// Values are written in their one spelling: plain when they can be, text
// otherwise. A Go string may carry any bytes, so binary values are fine.
// Names (file, group, key, header, reference segment, ETB payload) must be
// legal names; the first offending call is recorded and reported by Err,
// and the name is not written.
type Builder struct {
	buf      []byte
	err      error
	inRecord bool
	units    int
	stack    []builderFrame
}

type builderFrame struct {
	inRecord bool
	units    int
}

// Build drives a fresh Builder and returns its bytes and any error.
func Build(fn func(*Builder)) ([]byte, error) {
	b := &Builder{}
	fn(b)
	return b.buf, b.err
}

// Bytes returns the built buffer.
func (b *Builder) Bytes() []byte { return b.buf }

// String returns the built buffer as a string.
func (b *Builder) String() string { return string(b.buf) }

// Err returns the first error encountered (an illegal name).
func (b *Builder) Err() error { return b.err }

// ---- documents -------------------------------------------------------

// SYN begins a document. Follow it with Heading / HeadingLevel calls for
// the heading's keyed units, in the order they should be written.
func (b *Builder) SYN() *Builder {
	b.endRecord()
	b.buf = append(b.buf, SYN)
	return b
}

// Heading writes one keyed unit of the heading (SOH key US value).
func (b *Builder) Heading(key, value string) *Builder {
	b.endRecord()
	b.buf = append(b.buf, SOH)
	b.writeName(key)
	b.buf = append(b.buf, US)
	b.buf = AppendValue(b.buf, []byte(value))
	return b
}

// HeadingLevel writes a heading key whose value is a nested level (for
// example the blob table).
func (b *Builder) HeadingLevel(key string, fn func(*Builder)) *Builder {
	b.endRecord()
	b.buf = append(b.buf, SOH)
	b.writeName(key)
	b.buf = append(b.buf, US)
	b.openLevel(fn)
	return b
}

// EOT ends the document.
func (b *Builder) EOT() *Builder {
	b.endRecord()
	b.buf = append(b.buf, EOT)
	return b
}

// ETB writes a framing-only commit marker.
func (b *Builder) ETB() *Builder {
	b.endRecord()
	b.buf = append(b.buf, ETB)
	return b
}

// ETBPayload writes a commit marker with an integrity payload (a name).
func (b *Builder) ETBPayload(payload string) *Builder {
	b.endRecord()
	b.buf = append(b.buf, ETB)
	b.writeName(payload)
	return b
}

// EM writes the end of medium and the raw tail. Nothing may follow.
func (b *Builder) EM(tail []byte) *Builder {
	b.endRecord()
	b.buf = append(b.buf, EM)
	b.buf = append(b.buf, tail...)
	return b
}

// ---- structure -------------------------------------------------------

// File writes a file line (FS + name).
func (b *Builder) File(name string) *Builder {
	b.endRecord()
	b.buf = append(b.buf, FS)
	b.writeName(name)
	return b
}

// Group writes a group line (GS + name) and, when headers is not nil, a
// header row.
func (b *Builder) Group(name string, headers []string) *Builder {
	return b.GroupAt(name, headers, 1)
}

// GroupAt writes a group line at the given depth (GS repeated) and, when
// headers is not nil, a header row.
func (b *Builder) GroupAt(name string, headers []string, depth int) *Builder {
	b.endRecord()
	for i := 0; i < depth; i++ {
		b.buf = append(b.buf, GS)
	}
	b.writeName(name)
	if headers != nil {
		b.Header(headers...)
	}
	return b
}

// Header writes a header row: SOH RS names.
func (b *Builder) Header(names ...string) *Builder {
	b.endRecord()
	b.buf = append(b.buf, SOH, RS)
	for i, n := range names {
		if i > 0 {
			b.buf = append(b.buf, US)
		}
		b.writeName(n)
	}
	return b
}

// Section writes a document-mode section: GS repeated depth times, then the
// name.
func (b *Builder) Section(name string, depth int) *Builder { return b.GroupAt(name, nil, depth) }

// ---- records -----------------------------------------------------------

// Record writes a positional record. With no fields it begins a record and
// leaves it open for Field / Level / List / Ref / Key calls.
func (b *Builder) Record(fields ...string) *Builder {
	b.startRecord()
	for _, f := range fields {
		b.Field(f)
	}
	return b
}

// Block writes a document-mode content block (a record of one value).
func (b *Builder) Block(text string) *Builder { return b.Record(text) }

// Item writes a document-mode list item (a unit of the open block).
func (b *Builder) Item(text string) *Builder { return b.Field(text) }

// Field appends a unit to the open record (opening one if none is open).
func (b *Builder) Field(value string) *Builder {
	b.nextUnit()
	b.buf = AppendValue(b.buf, []byte(value))
	return b
}

// Key writes one keyed unit (SOH key US value) on the open record (opening
// one if none is open). Order is the caller's.
func (b *Builder) Key(name, value string) *Builder {
	if !b.inRecord {
		b.startRecord()
	}
	b.buf = append(b.buf, SOH)
	b.writeName(name)
	b.buf = append(b.buf, US)
	b.buf = AppendValue(b.buf, []byte(value))
	b.units++
	return b
}

// KeyLevel writes one keyed unit whose value is a nested level.
func (b *Builder) KeyLevel(name string, fn func(*Builder)) *Builder {
	if !b.inRecord {
		b.startRecord()
	}
	b.buf = append(b.buf, SOH)
	b.writeName(name)
	b.buf = append(b.buf, US)
	b.units++
	b.openLevel(fn)
	return b
}

// ---- values that are not plain -------------------------------------

// Level writes a nested level as the next unit. Inside fn the builder is at
// the top of a fresh body.
func (b *Builder) Level(fn func(*Builder)) *Builder {
	b.nextUnit()
	b.openLevel(fn)
	return b
}

// List writes a flat list as the next unit: a level holding one record (an
// empty level for no items).
func (b *Builder) List(items ...string) *Builder {
	b.nextUnit()
	b.buf = append(b.buf, DLE)
	if len(items) > 0 {
		b.buf = append(b.buf, RS)
		for i, it := range items {
			if i > 0 {
				b.buf = append(b.buf, US)
			}
			b.buf = AppendValue(b.buf, []byte(it))
		}
	}
	b.buf = append(b.buf, CAN)
	return b
}

// Ref writes a group reference (ENQ + name) as the next unit.
func (b *Builder) Ref(name string) *Builder {
	b.nextUnit()
	b.buf = append(b.buf, ENQ)
	b.writeName(name)
	return b
}

// RefPath writes a reference as the next unit: a group name alone is
// written plain; two or three segments (group, record id, field name) are
// written as a text path. Segments are non-empty names.
func (b *Builder) RefPath(path ...string) *Builder {
	if len(path) == 1 {
		return b.Ref(path[0])
	}
	b.nextUnit()
	b.buf = append(b.buf, ENQ, STX)
	for i, seg := range path {
		if i > 0 {
			b.buf = append(b.buf, US)
		}
		if seg == "" {
			b.setErr(ErrNotName)
			continue
		}
		b.writeName(seg)
	}
	b.buf = append(b.buf, ETX)
	return b
}

// ---- comments ----------------------------------------------------------

// Note writes a note (BEL + content) at the current position.
func (b *Builder) Note(text string) *Builder {
	b.buf = append(b.buf, BEL)
	b.buf = AppendValue(b.buf, []byte(text))
	return b
}

// ---- internals ---------------------------------------------------------

func (b *Builder) startRecord() {
	b.buf = append(b.buf, RS)
	b.inRecord = true
	b.units = 0
}

func (b *Builder) endRecord() {
	b.inRecord = false
	b.units = 0
}

func (b *Builder) nextUnit() {
	if !b.inRecord {
		b.startRecord()
	}
	if b.units > 0 {
		b.buf = append(b.buf, US)
	}
	b.units++
}

func (b *Builder) openLevel(fn func(*Builder)) {
	b.stack = append(b.stack, builderFrame{b.inRecord, b.units})
	b.inRecord = false
	b.units = 0
	b.buf = append(b.buf, DLE)
	fn(b)
	b.buf = append(b.buf, CAN)
	st := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	b.inRecord = st.inRecord
	b.units = st.units
}

func (b *Builder) setErr(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *Builder) writeName(s string) {
	if !IsName([]byte(s)) {
		b.setErr(ErrNotName)
		return
	}
	b.buf = append(b.buf, s...)
}
