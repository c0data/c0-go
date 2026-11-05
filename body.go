package c0

import "bytes"

// Body is a zero-copy navigator for a document body: the bytes between a
// heading and EOT, or the inside of a nested level. It indexes files,
// groups, header rows and records once; everything it returns is a slice
// into the original buffer.
//
// Records before the first GS of a file form an unnamed group; records and
// groups before the first FS form an unnamed file.
type Body struct {
	buf   []byte
	start int
	stop  int
	files []*FileView
}

// ParseBody validates buf as a body (a slice: records, groups, files, with
// no heading) and indexes it.
func ParseBody(buf []byte) (body *Body, err error) {
	defer catchScan(&err)
	if err := Validate(buf); err != nil {
		return nil, err
	}
	return newBody(buf, 0, len(buf)), nil
}

// newBody indexes buf[start:stop]; it panics (scanPanic) on malformed input.
func newBody(buf []byte, start, stop int) *Body {
	b := &Body{buf: buf, start: start, stop: stop}
	b.index()
	return b
}

// Files returns the body's files, in order. A body with no FS has one
// unnamed file.
func (b *Body) Files() []*FileView { return b.files }

// Groups returns all groups of all files, in order.
func (b *Body) Groups() []*Group {
	var out []*Group
	for _, f := range b.files {
		out = append(out, f.groups...)
	}
	return out
}

func (b *Body) firstFile() *FileView { return b.files[0] }

// GroupCount returns the number of groups in the first file.
func (b *Body) GroupCount() int { return len(b.firstFile().groups) }

// Group returns group i of the first file.
func (b *Body) Group(i int) *Group { return b.firstFile().groups[i] }

// GroupByName returns the first file's group with the given name, or nil.
func (b *Body) GroupByName(name string) *Group { return b.firstFile().GroupByName(name) }

// GroupNames returns the names of the first file's groups.
func (b *Body) GroupNames() [][]byte {
	gs := b.firstFile().groups
	out := make([][]byte, len(gs))
	for i, g := range gs {
		out[i] = g.Name()
	}
	return out
}

// Name returns the name of the first file (empty when the body has no FS).
func (b *Body) Name() []byte { return b.firstFile().Name() }

// Records returns the records of the body when it is a bare record stream
// (no FS, no GS): the unnamed group's records.
func (b *Body) Records() []*Record {
	gs := b.firstFile().groups
	if len(gs) == 0 {
		return nil
	}
	return gs[0].records
}

// RecordCount returns len(Records()).
func (b *Body) RecordCount() int { return len(b.Records()) }

// Record returns record i of Records().
func (b *Body) Record(i int) *Record { return b.Records()[i] }

// IsBare reports whether the body is just records: one unnamed file, at
// most one unnamed group.
func (b *Body) IsBare() bool {
	if len(b.files) != 1 || len(b.files[0].Name()) != 0 {
		return false
	}
	gs := b.files[0].groups
	return len(gs) == 0 || (len(gs) == 1 && gs[0].depth == 0)
}

// Raw returns the body's bytes.
func (b *Body) Raw() []byte { return b.buf[b.start:b.stop] }

// ------------------------------------------------------------------

func (b *Body) index() {
	buf := b.buf
	pos := scanLayout(buf, b.start, b.stop)
	// leading unnamed file
	file := &FileView{buf: buf}
	pos = b.indexPart(pos, file)
	fsNext := pos < b.stop && (buf[pos] == FS || b.deadFS(pos))
	if !(len(file.groups) == 0 && fsNext) {
		b.files = append(b.files, file)
	}
	for pos < b.stop {
		c := buf[pos]
		dead := false
		if c == BEL && b.deadFS(pos) {
			dead = true
			pos = scanLayout(buf, pos+1, b.stop)
			c = FS
		}
		if c != FS {
			break
		}
		pos = scanLayout(buf, pos+1, b.stop)
		ns := pos
		pos = scanPlain(buf, pos, b.stop)
		f := &FileView{buf: buf, nameStart: ns, nameEnd: pos}
		pos = b.indexPart(pos, f)
		if !dead {
			b.files = append(b.files, f)
		}
	}
	if len(b.files) == 0 {
		b.files = append(b.files, &FileView{buf: buf})
	}
}

func (b *Body) deadFS(pos int) bool {
	if !(pos < b.stop && b.buf[pos] == BEL) {
		return false
	}
	p2 := scanLayout(b.buf, pos+1, b.stop)
	return p2 < b.stop && b.buf[p2] == FS
}

// indexPart indexes one part: leading items (unnamed group), then groups.
// Returns the position of the next FS (or dead FS), or stop.
func (b *Body) indexPart(pos int, file *FileView) int {
	buf := b.buf
	g := &Group{buf: buf}
	pos = b.indexGroupContents(pos, g)
	if g.header != nil || len(g.records) > 0 {
		file.groups = append(file.groups, g)
	}
	current := g
	for pos < b.stop {
		c := buf[pos]
		dead := false
		if c == BEL {
			p2 := scanLayout(buf, pos+1, b.stop)
			if !(p2 < b.stop && buf[p2] == GS) {
				break
			}
			dead = true
			pos = p2
			c = GS
		}
		if c != GS {
			break
		}
		depth := 0
		for pos < b.stop && (buf[pos] == GS || IsLayout(buf[pos])) {
			if buf[pos] == GS {
				depth++
			}
			pos++
		}
		ns := pos
		pos = scanPlain(buf, pos, b.stop)
		if dead {
			// a dead group line: what follows still belongs to the group before
			pos = b.indexGroupContents(pos, current)
			if current == g && (g.header != nil || len(g.records) > 0) {
				listed := false
				for _, x := range file.groups {
					if x == g {
						listed = true
					}
				}
				if !listed {
					file.groups = append(file.groups, g)
				}
			}
		} else {
			grp := &Group{buf: buf, depth: depth, nameStart: ns, nameEnd: pos}
			pos = b.indexGroupContents(pos, grp)
			file.groups = append(file.groups, grp)
			current = grp
		}
	}
	return pos
}

// indexGroupContents indexes, after a group label (or at the start of a
// part), notes, the header row and items. Returns the position of the next
// GS/FS (live or dead) or stop.
func (b *Body) indexGroupContents(pos int, grp *Group) int {
	buf := b.buf
	firstItem := true
	for {
		pos = scanLayout(buf, pos, b.stop)
		if pos >= b.stop {
			return pos
		}
		switch c := buf[pos]; c {
		case RS:
			firstItem = false
			rs := pos + 1
			pos = scanRecordEnd(buf, rs, b.stop)
			grp.records = append(grp.records, &Record{buf: buf, start: rs, end: b.trimEnd(rs, pos)})
		case SOH:
			p2 := scanLayout(buf, pos+1, b.stop)
			if !(p2 < b.stop && buf[p2] == RS) {
				fail("keyed unit outside a record", pos)
			}
			if !firstItem {
				fail("header row not at the start of its group", pos)
			}
			firstItem = false
			hs := p2 + 1
			pos = scanRecordEnd(buf, hs, b.stop)
			grp.header = &Record{buf: buf, start: hs, end: b.trimEnd(hs, pos)}
		case ETB:
			pos = scanPlain(buf, scanLayout(buf, pos+1, b.stop), b.stop)
		case BEL:
			p2 := scanLayout(buf, pos+1, b.stop)
			switch {
			case p2 >= b.stop || buf[p2] >= 0x20 || buf[p2] == STX:
				pos = scanComment(buf, pos, b.stop) // a note
			case buf[p2] == RS || buf[p2] == SOH:
				pos = scanComment(buf, pos, b.stop) // dead record / header row / keyed unit
				if buf[p2] == RS {
					firstItem = false
				}
			default:
				return pos // dead GS or FS: the caller's business
			}
		default:
			return pos
		}
	}
}

func (b *Body) trimEnd(start, stop int) int {
	for stop > start && IsLayout(b.buf[stop-1]) {
		stop--
	}
	return stop
}

// FileView is a file within a body: its name and groups.
type FileView struct {
	buf                []byte
	nameStart, nameEnd int
	groups             []*Group
}

// Name returns the file's name (empty for the unnamed leading file).
func (f *FileView) Name() []byte { return Trim(f.buf[f.nameStart:f.nameEnd]) }

// Groups returns the file's groups in order.
func (f *FileView) Groups() []*Group { return f.groups }

// GroupByName returns the group with the given name, or nil.
func (f *FileView) GroupByName(name string) *Group {
	for _, g := range f.groups {
		if string(g.Name()) == name {
			return g
		}
	}
	return nil
}

// Group is a group: its depth (GS count; 0 for the unnamed leading group),
// name, optional header row and records.
type Group struct {
	buf                []byte
	depth              int
	nameStart, nameEnd int
	header             *Record
	records            []*Record
}

// Table is the group read as a table: the same thing.
type Table = Group

// Name returns the group's name.
func (g *Group) Name() []byte { return Trim(g.buf[g.nameStart:g.nameEnd]) }

// Depth returns the number of GS that opened the group (0 for the unnamed
// leading group).
func (g *Group) Depth() int { return g.depth }

// HasHeader reports whether the group has a header row.
func (g *Group) HasHeader() bool { return g.header != nil }

// Headers returns the header names, or nil.
func (g *Group) Headers() [][]byte {
	if g.header == nil {
		return nil
	}
	return g.header.Values()
}

// HeaderCount returns the number of header names.
func (g *Group) HeaderCount() int {
	if g.header == nil {
		return 0
	}
	return g.header.FieldCount()
}

// Header returns header name i.
func (g *Group) Header(i int) []byte { return g.header.Value(i) }

// RecordCount returns the number of records.
func (g *Group) RecordCount() int { return len(g.records) }

// Record returns record i.
func (g *Group) Record(i int) *Record { return g.records[i] }

// Records returns all records.
func (g *Group) Records() []*Record { return g.records }

// Column returns the index of a header name, or -1.
func (g *Group) Column(name string) int {
	for i, h := range g.Headers() {
		if string(h) == name {
			return i
		}
	}
	return -1
}

// Record is a record: positional or keyed. Zero-copy accessors.
type Record struct {
	buf        []byte
	start, end int
}

// Raw returns the record's bytes (after its RS).
func (r *Record) Raw() []byte { return r.buf[r.start:r.end] }

// IsKeyed reports whether the record is keyed (its first unit is an SOH,
// live or dead).
func (r *Record) IsKeyed() bool {
	buf := r.buf
	p := scanLayout(buf, r.start, r.end)
	for p < r.end && buf[p] == BEL {
		p2 := scanLayout(buf, p+1, r.end)
		if p2 < r.end && buf[p2] == SOH {
			return true
		}
		if p2 < r.end && buf[p2] < 0x20 && buf[p2] != STX && buf[p2] != DLE {
			break
		}
		p = scanLayout(buf, scanComment(buf, p, r.end), r.end)
	}
	return p < r.end && buf[p] == SOH
}

// ---- positional -----------------------------------------------------

// Field returns the raw slice of unit n (layout at its edges included), or
// nil when there is no such unit.
func (r *Record) Field(n int) []byte {
	var out []byte
	r.eachUnit(func(i, s, e int) bool {
		if i == n {
			out = r.buf[s:e]
			return false
		}
		return true
	})
	return out
}

// FieldCount returns the number of positional units (N separators yield N+1
// units; 0 for a keyed record).
func (r *Record) FieldCount() int {
	count := 0
	r.eachUnit(func(i, s, e int) bool { count = i + 1; return true })
	return count
}

// Fields returns the raw slices of all positional units.
func (r *Record) Fields() [][]byte {
	var out [][]byte
	r.eachUnit(func(i, s, e int) bool { out = append(out, r.buf[s:e]); return true })
	return out
}

// Value returns the logical value of unit n: a text's inside with escapes
// decoded, or the plain value with its layout trimmed.
func (r *Record) Value(n int) []byte { return value(r.Field(n)) }

// Values returns the logical values of all positional units.
func (r *Record) Values() [][]byte {
	fs := r.Fields()
	out := make([][]byte, len(fs))
	for i, f := range fs {
		out[i] = value(f)
	}
	return out
}

// IsText reports whether unit n is written as text.
func (r *Record) IsText(n int) bool { return unitStartsWith(r.Field(n), STX) }

// IsLevel reports whether unit n is a nested level.
func (r *Record) IsLevel(n int) bool { return unitStartsWith(r.Field(n), DLE) }

// IsRef reports whether unit n is a reference.
func (r *Record) IsRef(n int) bool { return unitStartsWith(r.Field(n), ENQ) }

// Level returns unit n as a nested level.
func (r *Record) Level(n int) (*Body, error) { return levelOf(r.Field(n)) }

// List returns unit n as a flat list: a level holding exactly one
// positional record (an empty level is the empty list).
func (r *Record) List(n int) ([][]byte, error) {
	body, err := r.Level(n)
	if err != nil {
		return nil, err
	}
	return listOf(body)
}

// Ref returns unit n as a reference.
func (r *Record) Ref(n int) (Reference, error) { return ParseReference(r.Field(n)) }

// ---- keyed ---------------------------------------------------------

// KeyValue is one keyed unit: a key and its logical value.
type KeyValue struct {
	Key, Value []byte
}

// Keys returns the keys in written order (duplicates included).
func (r *Record) Keys() [][]byte {
	var out [][]byte
	r.eachKeyed(func(k []byte, vs, ve int) { out = append(out, k) })
	return out
}

// Pairs returns the keyed units in written order (duplicates included).
func (r *Record) Pairs() []KeyValue {
	var out []KeyValue
	r.eachKeyed(func(k []byte, vs, ve int) {
		out = append(out, KeyValue{k, value(r.buf[vs:ve])})
	})
	return out
}

// FieldByKey returns the raw unit for a key (last wins).
func (r *Record) FieldByKey(key string) ([]byte, bool) {
	var found []byte
	ok := false
	r.eachKeyed(func(k []byte, vs, ve int) {
		if string(k) == key {
			found, ok = r.buf[vs:ve], true
		}
	})
	return found, ok
}

// Get returns the logical value for a key (last wins).
func (r *Record) Get(key string) ([]byte, bool) {
	f, ok := r.FieldByKey(key)
	if !ok {
		return nil, false
	}
	return value(f), true
}

// LevelByKey returns the value for a key as a nested level.
func (r *Record) LevelByKey(key string) (*Body, error) {
	f, ok := r.FieldByKey(key)
	if !ok {
		return nil, ErrNotLevel
	}
	return levelOf(f)
}

// HasKey reports whether the record has a unit with the key.
func (r *Record) HasKey(key string) bool {
	_, ok := r.FieldByKey(key)
	return ok
}

// ---- shared ---------------------------------------------------------

func levelOf(raw []byte) (*Body, error) {
	t := Trim(raw)
	if !(len(t) > 0 && t[0] == DLE) {
		return nil, ErrNotLevel
	}
	stop := scanLevel(t, 0, len(t))
	return newBody(t, 1, stop-1), nil
}

func listOf(body *Body) ([][]byte, error) {
	recs := body.Records()
	if len(recs) == 0 && body.IsBare() {
		return [][]byte{}, nil
	}
	if !(len(recs) == 1 && body.IsBare()) || recs[0].IsKeyed() || body.Group(0).HasHeader() {
		return nil, ErrNotList
	}
	return recs[0].Values(), nil
}

// eachUnit calls fn(index, start, end) for each positional unit until fn
// returns false.
func (r *Record) eachUnit(fn func(i, s, e int) bool) {
	buf := r.buf
	pos := scanLayout(buf, r.start, r.end)
	if pos < r.end && buf[pos] == SOH {
		return
	}
	idx := 0
	unitStart := r.start
	for {
		pos = scanUnit(buf, pos, r.end)
		if !fn(idx, unitStart, pos) {
			return
		}
		if !(pos < r.end && buf[pos] == US) {
			return
		}
		idx++
		pos++
		unitStart = pos
	}
}

// eachKeyed calls fn(key, valueStart, valueEnd) per live keyed unit.
func (r *Record) eachKeyed(fn func(k []byte, vs, ve int)) {
	buf := r.buf
	pos := scanLayout(buf, r.start, r.end)
	for pos < r.end {
		switch b := buf[pos]; {
		case b == SOH:
			ks := scanLayout(buf, pos+1, r.end)
			ke := scanPlain(buf, ks, r.end)
			if !(ke < r.end && buf[ke] == US) {
				fail("key without a value (no US)", ke)
			}
			vs := ke + 1
			ve := scanUnit(buf, vs, r.end)
			fn(Trim(buf[ks:ke]), vs, ve)
			pos = ve
		case b == BEL:
			pos = scanComment(buf, pos, r.end)
		case IsLayout(b):
			pos++
		default:
			return
		}
	}
}

// Reference is a reference: a group name, or a path of two or three
// segments (group, record id, field name).
type Reference struct {
	Path [][]byte
}

// ParseReference reads a raw unit as a reference.
func ParseReference(raw []byte) (ref Reference, err error) {
	defer catchScan(&err)
	t := Trim(raw)
	if !(len(t) > 0 && t[0] == ENQ) {
		return Reference{}, ErrNotReference
	}
	p := scanLayout(t, 1, len(t))
	if p < len(t) && t[p] == STX {
		stop := scanText(t, p, len(t))
		inside := DecodeText(t[p+1 : stop-1])
		segs := bytes.Split(inside, []byte{US})
		if len(segs) > 3 {
			fail("reference path with more than three segments", p)
		}
		for _, s := range segs {
			if len(s) == 0 {
				fail("reference path with an empty segment", p)
			}
		}
		return Reference{Path: segs}, nil
	}
	name := Trim(t[p:])
	if len(name) == 0 {
		fail("reference without a name", p)
	}
	return Reference{Path: [][]byte{name}}, nil
}

// Group returns the group segment.
func (r Reference) Group() []byte { return r.Path[0] }

// Record returns the record-id segment, or nil.
func (r Reference) Record() []byte {
	if len(r.Path) > 1 {
		return r.Path[1]
	}
	return nil
}

// Field returns the field-name segment, or nil.
func (r Reference) Field() []byte {
	if len(r.Path) > 2 {
		return r.Path[2]
	}
	return nil
}

// IsGroup reports whether the reference names a whole group.
func (r Reference) IsGroup() bool { return len(r.Path) == 1 }
