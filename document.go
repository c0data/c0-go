package c0

import "strconv"

// Heading is the heading of a document: keyed units after SYN. Values are
// logical (text decoded) except a nested level, which is kept raw.
type Heading struct {
	raw      []byte
	pairs    []KeyValue
	rawPairs []KeyValue
}

// Raw returns the heading's bytes (after SYN, before the body).
func (h *Heading) Raw() []byte { return h.raw }

// Pairs returns the keyed units in written order.
func (h *Heading) Pairs() []KeyValue { return h.pairs }

// Keys returns the keys in written order.
func (h *Heading) Keys() [][]byte {
	out := make([][]byte, len(h.pairs))
	for i, p := range h.pairs {
		out[i] = p.Key
	}
	return out
}

// Get returns the value for a key (last wins).
func (h *Heading) Get(key string) ([]byte, bool) {
	var found []byte
	ok := false
	for _, p := range h.pairs {
		if string(p.Key) == key {
			found, ok = p.Value, true
		}
	}
	return found, ok
}

// Shape returns the shape key's value, or "".
func (h *Heading) Shape() string {
	v, _ := h.Get("shape")
	return string(v)
}

// Version returns the version key's value, or 1.
func (h *Heading) Version() int {
	if v, ok := h.Get("version"); ok {
		if n, err := strconv.Atoi(string(v)); err == nil {
			return n
		}
	}
	return 1
}

// RawBlob returns the raw unit of the blob key (a level), or nil.
func (h *Heading) RawBlob() []byte {
	var found []byte
	for _, p := range h.rawPairs {
		if string(p.Key) == "blob" {
			found = p.Value
		}
	}
	return found
}

// Blob returns the blob table as a Body, or nil when none is declared (or
// the blob value is not a level).
func (h *Heading) Blob() *Body {
	raw := h.RawBlob()
	if !(len(raw) > 0 && raw[0] == DLE) {
		return nil
	}
	return newBody(raw, 1, scanLevel(raw, 0, len(raw))-1)
}

// IsEmpty reports whether the heading has no keyed units.
func (h *Heading) IsEmpty() bool { return len(h.pairs) == 0 }

// Document is one C0DATA document: an optional SYN heading, a body, an
// optional EOT.
type Document struct {
	buf      []byte
	heading  *Heading
	body     *Body
	start    int
	stop     int // position after EOT (or end of document)
	hasSYN   bool
	hasEOT   bool
	warnings []string
}

// ParseDocument validates buf and reads the first document in it. Use
// ParseContainer for a buffer holding several documents or an EM tail.
func ParseDocument(buf []byte) (doc *Document, err error) {
	defer catchScan(&err)
	if err := Validate(buf); err != nil {
		return nil, err
	}
	return newDocument(buf, 0), nil
}

func newDocument(buf []byte, start int) *Document {
	d := &Document{buf: buf, start: start}
	n := len(buf)
	pos := scanLayout(buf, start, n)
	d.hasSYN = pos < n && buf[pos] == SYN
	if d.hasSYN {
		d.heading, pos = parseHeading(buf, pos+1, n)
	} else {
		d.warnings = append(d.warnings, "no SYN at the start of the document")
		d.heading = &Heading{}
	}
	bodyEnd := findBodyEnd(buf, pos, n)
	d.body = newBody(buf, pos, bodyEnd)
	pos = scanLayout(buf, bodyEnd, n)
	d.hasEOT = pos < n && buf[pos] == EOT
	if d.hasEOT {
		pos++
	} else if d.heading.Shape() != "stream" {
		d.warnings = append(d.warnings, "no EOT at the end of the document")
	}
	d.stop = pos
	for _, k := range d.heading.Keys() {
		switch s := string(k); s {
		case "shape", "version", "blob":
		default:
			d.warnings = append(d.warnings, "unknown heading key '"+s+"'")
		}
	}
	return d
}

// Heading returns the document's heading (empty when there is no SYN).
func (d *Document) Heading() *Heading { return d.heading }

// Body returns the document's body.
func (d *Document) Body() *Body { return d.body }

// HasSYN reports whether the document began with SYN.
func (d *Document) HasSYN() bool { return d.hasSYN }

// HasEOT reports whether the document ended with EOT.
func (d *Document) HasEOT() bool { return d.hasEOT }

// Warnings lists the loud warnings raised while reading: no SYN, no EOT on
// a non-stream document, unknown heading keys.
func (d *Document) Warnings() []string { return d.warnings }

// Start returns the offset of the document in the buffer.
func (d *Document) Start() int { return d.start }

// Stop returns the offset just past the document (after EOT, when present).
func (d *Document) Stop() int { return d.stop }

// Raw returns the document's bytes.
func (d *Document) Raw() []byte { return d.buf[d.start:d.stop] }

// Shape returns the heading's shape, or "".
func (d *Document) Shape() string { return d.heading.Shape() }

// Convenience accessors over the body.

// Name returns the first file's name.
func (d *Document) Name() []byte { return d.body.Name() }

// Files returns the body's files.
func (d *Document) Files() []*FileView { return d.body.Files() }

// Groups returns all groups of all files.
func (d *Document) Groups() []*Group { return d.body.Groups() }

// GroupCount returns the number of groups in the first file.
func (d *Document) GroupCount() int { return d.body.GroupCount() }

// Group returns group i of the first file.
func (d *Document) Group(i int) *Group { return d.body.Group(i) }

// GroupByName returns the first file's group with the given name, or nil.
func (d *Document) GroupByName(name string) *Group { return d.body.GroupByName(name) }

// GroupNames returns the first file's group names.
func (d *Document) GroupNames() [][]byte { return d.body.GroupNames() }

// Records returns the records of a bare body.
func (d *Document) Records() []*Record { return d.body.Records() }

// parseHeading reads keyed units after SYN and returns the heading and the
// position of the first body code.
func parseHeading(buf []byte, pos, n int) (*Heading, int) {
	hs := pos
	h := &Heading{}
	for {
		pos = scanLayout(buf, pos, n)
		if pos >= n {
			break
		}
		b := buf[pos]
		if b == SOH {
			p2 := scanLayout(buf, pos+1, n)
			if p2 < n && buf[p2] == RS {
				break
			}
			ks := p2
			ke := scanPlain(buf, ks, n)
			if !(ke < n && buf[ke] == US) {
				fail("key without a value (no US)", ke)
			}
			vs := ke + 1
			ve := scanUnit(buf, vs, n)
			key := Trim(buf[ks:ke])
			rawv := Trim(buf[vs:ve])
			if len(rawv) > 0 && rawv[0] == ENQ {
				fail("reference in a heading", vs)
			}
			h.rawPairs = append(h.rawPairs, KeyValue{key, rawv})
			if len(rawv) > 0 && rawv[0] == DLE {
				h.pairs = append(h.pairs, KeyValue{key, rawv})
			} else {
				h.pairs = append(h.pairs, KeyValue{key, value(rawv)})
			}
			pos = ve
		} else if b == BEL {
			pos = scanComment(buf, pos, n)
		} else if b >= 0x20 {
			fail("plain text directly after SYN", pos)
		} else {
			break
		}
	}
	h.raw = buf[hs:pos]
	return h, pos
}

// findBodyEnd scans from the start of a body to its end: EOT, SYN, EM or
// end of input at depth zero, skipping text and levels.
func findBodyEnd(buf []byte, pos, n int) int {
	for pos < n {
		switch b := buf[pos]; {
		case b >= 0x20:
			pos++
		case b == STX:
			pos = scanText(buf, pos, n)
		case b == DLE:
			pos = scanLevel(buf, pos, n)
		case b == EOT || b == SYN || b == EM:
			return pos
		case b == CAN:
			fail("CAN with no open level", pos)
		default:
			pos++
		}
	}
	return pos
}

// Container is a file or message: one or more documents and an optional raw
// tail after EM.
type Container struct {
	documents []*Document
	tail      []byte
	hasEM     bool
}

// ParseContainer validates buf and reads every document in it.
func ParseContainer(buf []byte) (c *Container, err error) {
	defer catchScan(&err)
	if err := Validate(buf); err != nil {
		return nil, err
	}
	c = &Container{}
	n := len(buf)
	pos := scanLayout(buf, 0, n)
	for pos < n {
		if buf[pos] == EM {
			c.hasEM = true
			c.tail = buf[pos+1 : n]
			break
		}
		doc := newDocument(buf, pos)
		c.documents = append(c.documents, doc)
		pos = scanLayout(buf, doc.stop, n)
		if pos < n && buf[pos] != SYN && buf[pos] != EM {
			fail("unexpected byte after EOT", pos)
		}
	}
	return c, nil
}

// Documents returns the container's documents in order.
func (c *Container) Documents() []*Document { return c.documents }

// First returns the first document, or nil for an empty container.
func (c *Container) First() *Document {
	if len(c.documents) == 0 {
		return nil
	}
	return c.documents[0]
}

// Tail returns the raw bytes after EM.
func (c *Container) Tail() []byte { return c.tail }

// HasEM reports whether the container ends in an EM region.
func (c *Container) HasEM() bool { return c.hasEM }

// Blob returns the blob table from the first document's heading, or nil.
func (c *Container) Blob() *Body {
	if len(c.documents) == 0 {
		return nil
	}
	return c.documents[0].heading.Blob()
}

// BlobBytes returns the bytes of a named blob from the tail. It reports
// false when the table does not declare the name, or the row's offset or
// length is missing, not an integer, or outside the tail.
func (c *Container) BlobBytes(name string) ([]byte, bool) {
	table := c.Blob()
	if table == nil {
		return nil, false
	}
	gs := table.Groups()
	if len(gs) == 0 {
		return nil, false
	}
	g := gs[0]
	ni, oi, li := g.Column("name"), g.Column("offset"), g.Column("length")
	if ni < 0 || oi < 0 || li < 0 {
		return nil, false
	}
	var found []byte
	ok := false
	for _, r := range g.Records() {
		if r.FieldCount() <= max(ni, oi, li) || string(r.Value(ni)) != name {
			continue
		}
		off, err1 := strconv.Atoi(string(r.Value(oi)))
		length, err2 := strconv.Atoi(string(r.Value(li)))
		if err1 != nil || err2 != nil || off < 0 || length < 0 || off+length > len(c.tail) {
			continue
		}
		found, ok = c.tail[off:off+length], true
	}
	return found, ok
}
