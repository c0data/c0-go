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
// Two layers. The primitives are one method per code (FS, GS, RS, US, SUB,
// SOH, DLE, CAN, BEL, ENQ, SYN, EOT, ETB, EM); each writes its code and, when
// given, what grammatically follows it, so a chain reads like the bytes:
//
//	b.RS("Alice").US("1502.30")                        // ␞Alice␟1502.30
//	b.RS().SOH("host").US("localhost").SOH("port").US("5432")
//	b.BEL().GS("old")                                  // a dead group
//
// The composed methods (File, Group, Header, Record, Field, Key, Level, List,
// Ref, Note, Heading, ...) are built on the primitives and cannot write
// ill-formed output. The primitives write what you spell; well-formedness is
// then yours to keep. Both layers share the record state, so they mix.
//
// Values are written in their one spelling: plain when they can be, text
// otherwise. A Go string may carry any bytes, so binary values are fine.
// Names (file, group, key, header, reference segment, ETB payload) must be
// legal names; the first offending call is recorded and reported by Err,
// and the name is not written. CAN with no open level and an empty note are
// recorded the same way.
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

// Err returns the first error encountered.
func (b *Builder) Err() error { return b.err }

// ---- primitives: one method per code ---------------------------------
//
// Optional arguments are variadic: each takes none or one. More is a
// programming error and panics.

// FS writes FS and a file name.
func (b *Builder) FS(name string) *Builder {
	b.endRecord()
	b.buf = append(b.buf, FS)
	b.writeName(name)
	return b
}

// GS writes GS and, when given, a group name. GS().GS("sub") is a group at
// depth 2.
func (b *Builder) GS(name ...string) *Builder {
	n, ok := optional("GS", name)
	b.endRecord()
	b.buf = append(b.buf, GS)
	if ok {
		b.writeName(n)
	}
	return b
}

// RS writes RS and, when given, the record's first value.
func (b *Builder) RS(value ...string) *Builder {
	v, ok := optional("RS", value)
	b.buf = append(b.buf, RS)
	b.inRecord = true
	b.units = 0
	if ok {
		b.firstValue(v)
	}
	return b
}

// US writes US and, when given, the value that follows it.
func (b *Builder) US(value ...string) *Builder {
	v, ok := optional("US", value)
	b.buf = append(b.buf, US)
	if ok {
		b.buf = AppendValue(b.buf, []byte(v))
	}
	b.units++
	return b
}

// SUB writes SUB and, when given, the new side of a substitution. C0-DIFF
// only: RS("old").SUB("new") is a replace-all; GS("x").US("10").SUB("20") a
// pattern with one substitution. In a data body SUB is an error. It does not
// change the unit count.
func (b *Builder) SUB(value ...string) *Builder {
	v, ok := optional("SUB", value)
	b.buf = append(b.buf, SUB)
	if ok {
		b.buf = AppendValue(b.buf, []byte(v))
	}
	return b
}

// SOH writes SOH and, when given, a key. Bare before RS it opens a header
// row.
func (b *Builder) SOH(name ...string) *Builder {
	n, ok := optional("SOH", name)
	b.buf = append(b.buf, SOH)
	if ok {
		b.writeName(n)
	}
	return b
}

// DLE opens a nested level. Inside, the builder is at the top of a fresh
// body.
func (b *Builder) DLE() *Builder {
	b.stack = append(b.stack, builderFrame{b.inRecord, b.units})
	b.inRecord = false
	b.units = 0
	b.buf = append(b.buf, DLE)
	return b
}

// CAN closes the innermost level. With none open the error is recorded.
func (b *Builder) CAN() *Builder {
	if len(b.stack) == 0 {
		b.setErr(ErrNoLevel)
		return b
	}
	b.buf = append(b.buf, CAN)
	st := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	b.inRecord = st.inRecord
	b.units = st.units
	return b
}

// BEL writes BEL and, when given, a note. Bare, it marks the element that
// follows dead. An empty note is an error: a bare BEL is the dead marker.
func (b *Builder) BEL(text ...string) *Builder {
	t, ok := optional("BEL", text)
	if ok && t == "" {
		b.setErr(ErrEmptyNote)
		return b
	}
	b.buf = append(b.buf, BEL)
	if ok {
		b.buf = AppendValue(b.buf, []byte(t))
	}
	return b
}

// ENQ writes ENQ and a reference: a group name alone is written plain; two
// or three segments (group, record id, field name) are written as a text
// path. Segments are non-empty names.
func (b *Builder) ENQ(path ...string) *Builder {
	b.buf = append(b.buf, ENQ)
	if len(path) == 0 {
		b.setErr(ErrNotName)
		return b
	}
	if len(path) == 1 {
		b.writeName(path[0])
		return b
	}
	b.buf = append(b.buf, STX)
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

// SYN begins a document. Heading pairs follow as SOH(key).US(value), or
// through Heading / HeadingLevel.
func (b *Builder) SYN() *Builder {
	b.endRecord()
	b.buf = append(b.buf, SYN)
	return b
}

// EOT ends the document.
func (b *Builder) EOT() *Builder {
	b.endRecord()
	b.buf = append(b.buf, EOT)
	return b
}

// ETB writes a commit marker and, when given, its integrity payload (a
// name).
func (b *Builder) ETB(payload ...string) *Builder {
	p, ok := optional("ETB", payload)
	b.endRecord()
	b.buf = append(b.buf, ETB)
	if ok {
		b.writeName(p)
	}
	return b
}

// EM writes the end of medium and the raw tail. Nothing may follow.
func (b *Builder) EM(tail []byte) *Builder {
	b.endRecord()
	b.buf = append(b.buf, EM)
	b.buf = append(b.buf, tail...)
	return b
}

// ---- documents -------------------------------------------------------

// Heading writes one keyed unit of the heading (SOH key US value).
func (b *Builder) Heading(key, value string) *Builder {
	b.endRecord()
	return b.SOH(key).US(value)
}

// HeadingLevel writes a heading key whose value is a nested level (for
// example the blob table).
func (b *Builder) HeadingLevel(key string, fn func(*Builder)) *Builder {
	b.endRecord()
	b.SOH(key).US().DLE()
	fn(b)
	return b.CAN()
}

// ETBPayload writes a commit marker with an integrity payload: ETB(payload).
func (b *Builder) ETBPayload(payload string) *Builder { return b.ETB(payload) }

// ---- structure -------------------------------------------------------

// File writes a file line (FS + name).
func (b *Builder) File(name string) *Builder { return b.FS(name) }

// Group writes a group line (GS + name) and, when headers is not nil, a
// header row.
func (b *Builder) Group(name string, headers []string) *Builder {
	return b.GroupAt(name, headers, 1)
}

// GroupAt writes a group line at the given depth (GS repeated) and, when
// headers is not nil, a header row.
func (b *Builder) GroupAt(name string, headers []string, depth int) *Builder {
	for i := 1; i < depth; i++ {
		b.GS()
	}
	b.GS(name)
	if headers != nil {
		b.Header(headers...)
	}
	return b
}

// Header writes a header row: SOH RS names.
func (b *Builder) Header(names ...string) *Builder {
	b.SOH().RS()
	for i, n := range names {
		if i > 0 {
			b.US()
		}
		b.writeName(n)
	}
	b.endRecord()
	return b
}

// Section writes a document-mode section: GS repeated depth times, then the
// name.
func (b *Builder) Section(name string, depth int) *Builder { return b.GroupAt(name, nil, depth) }

// ---- records -----------------------------------------------------------

// Record writes a positional record. With no fields it begins a record and
// leaves it open for Field / Level / List / Ref / Key calls.
func (b *Builder) Record(fields ...string) *Builder {
	b.RS()
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
	switch {
	case !b.inRecord:
		return b.RS(value)
	case b.units == 0:
		b.firstValue(value)
		return b
	default:
		return b.US(value)
	}
}

// Key writes one keyed unit (SOH key US value) on the open record (opening
// one if none is open). Order is the caller's.
func (b *Builder) Key(name, value string) *Builder {
	if !b.inRecord {
		b.RS()
	}
	return b.SOH(name).US(value)
}

// KeyLevel writes one keyed unit whose value is a nested level.
func (b *Builder) KeyLevel(name string, fn func(*Builder)) *Builder {
	if !b.inRecord {
		b.RS()
	}
	b.SOH(name).US().DLE()
	fn(b)
	return b.CAN()
}

// ---- values that are not plain -------------------------------------

// Level writes a nested level as the next unit. Inside fn the builder is at
// the top of a fresh body.
func (b *Builder) Level(fn func(*Builder)) *Builder {
	b.nextUnit()
	b.DLE()
	fn(b)
	return b.CAN()
}

// List writes a flat list as the next unit: a level holding one record (an
// empty level for no items).
func (b *Builder) List(items ...string) *Builder {
	b.nextUnit()
	b.DLE()
	if len(items) > 0 {
		b.RS()
		for _, it := range items {
			b.Field(it)
		}
	}
	return b.CAN()
}

// Ref writes a group reference (ENQ + name) as the next unit.
func (b *Builder) Ref(name string) *Builder { return b.RefPath(name) }

// RefPath writes a reference as the next unit: a group name alone, or a
// path of two or three names (group, record id, field name).
func (b *Builder) RefPath(path ...string) *Builder {
	b.nextUnit()
	return b.ENQ(path...)
}

// ---- comments ----------------------------------------------------------

// Note writes a note (BEL + content) at the current position. Content is
// required: a bare BEL is a dead marker, written with BEL().
func (b *Builder) Note(text string) *Builder { return b.BEL(text) }

// ---- internals ---------------------------------------------------------

// optional reads a none-or-one variadic argument.
func optional(method string, args []string) (string, bool) {
	switch len(args) {
	case 0:
		return "", false
	case 1:
		return args[0], true
	}
	panic("c0: Builder." + method + " takes at most one argument")
}

func (b *Builder) endRecord() {
	b.inRecord = false
	b.units = 0
}

// nextUnit positions for the next unit of the open record (opening one if
// needed): US between units, nothing before the first.
func (b *Builder) nextUnit() {
	if !b.inRecord {
		b.RS()
	} else if b.units > 0 {
		b.buf = append(b.buf, US)
	}
	b.units++
}

// firstValue writes the first value of a record, straight after RS.
func (b *Builder) firstValue(value string) {
	b.buf = AppendValue(b.buf, []byte(value))
	b.units = 1
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
