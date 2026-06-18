package c0

import "bytes"

// Document is a zero-copy navigator for a full C0DATA document.
type Document struct {
	buf          []byte
	name         [2]int
	groupOffsets []int
	groupNames   [][2]int
}

// NewDocument indexes a document buffer (FS/GS/RS/US structure).
func NewDocument(buf []byte) *Document {
	d := &Document{buf: buf}
	d.index()
	return d
}

// Name returns the document/file name (text after FS); empty if no FS.
func (d *Document) Name() []byte { return d.buf[d.name[0]:d.name[1]] }

// GroupCount returns the number of top-level groups.
func (d *Document) GroupCount() int { return len(d.groupOffsets) }

// Group returns top-level group i.
func (d *Document) Group(i int) *Group {
	start := d.groupOffsets[i]
	var end int
	if i+1 < len(d.groupOffsets) {
		end = d.groupOffsets[i+1]
	} else {
		end = d.findEnd(start)
	}
	return &Group{buf: d.buf, start: start, end: end}
}

// Groups returns all top-level groups.
func (d *Document) Groups() []*Group {
	gs := make([]*Group, len(d.groupOffsets))
	for i := range d.groupOffsets {
		gs[i] = d.Group(i)
	}
	return gs
}

// GroupByName returns the group with the given name, or nil if none.
func (d *Document) GroupByName(name string) *Group {
	needle := []byte(name)
	for i, gn := range d.groupNames {
		if bytes.Equal(d.buf[gn[0]:gn[1]], needle) {
			return d.Group(i)
		}
	}
	return nil
}

// GroupNames returns all top-level group names.
func (d *Document) GroupNames() [][]byte {
	ns := make([][]byte, len(d.groupNames))
	for i, gn := range d.groupNames {
		ns[i] = d.buf[gn[0]:gn[1]]
	}
	return ns
}

func (d *Document) index() {
	buf := d.buf
	n := len(buf)
	pos := 0

	if pos < n && buf[pos] == FS {
		pos++
		start := pos
		for pos < n && buf[pos] >= 0x20 {
			pos++
		}
		d.name = [2]int{start, pos}
	}

	for pos < n {
		b := buf[pos]
		if b == EOT {
			break
		}
		if b == GS {
			gsPos := pos
			run := 0
			for pos < n && buf[pos] == GS {
				run++
				pos++
			}
			nameStart := pos
			for pos < n && buf[pos] >= 0x20 {
				pos++
			}
			if run == 1 {
				d.groupOffsets = append(d.groupOffsets, gsPos)
				d.groupNames = append(d.groupNames, [2]int{nameStart, pos})
			}
		} else {
			pos++
		}
	}
}

func (d *Document) findEnd(gsStart int) int {
	buf := d.buf
	n := len(buf)
	pos := gsStart + 1
	for pos < n && buf[pos] >= 0x20 {
		pos++
	}
	for pos < n {
		b := buf[pos]
		if b == FS || b == EOT {
			break
		}
		switch {
		case b == GS:
			count := 0
			peek := pos
			for peek < n && buf[peek] == GS {
				count++
				peek++
			}
			if count == 1 {
				return pos
			}
			pos = peek
			for pos < n && buf[pos] >= 0x20 {
				pos++
			}
		case b == DLE:
			pos += 2
		default:
			pos++
		}
	}
	if pos > n {
		pos = n
	}
	return pos
}

// Group is a group within a document; read it as a Table.
type Group struct {
	buf        []byte
	start, end int
}

// Name returns the group name.
func (g *Group) Name() []byte {
	pos := g.start + 1 // skip GS
	nameStart := pos
	for pos < g.end && g.buf[pos] >= 0x20 {
		pos++
	}
	return g.buf[nameStart:pos]
}

// Table reads the group as a Table.
func (g *Group) Table() *Table { return NewTableAt(g.buf, g.start) }

// HasHeader reports whether the group has an SOH header.
func (g *Group) HasHeader() bool {
	pos := g.start + 1
	for pos < g.end && g.buf[pos] >= 0x20 {
		pos++
	}
	return pos < g.end && g.buf[pos] == SOH
}

// Record returns record i.
func (g *Group) Record(i int) *Record { return g.Table().Record(i) }

// RecordCount returns the number of records.
func (g *Group) RecordCount() int { return g.Table().RecordCount() }

// Raw returns the group's bytes.
func (g *Group) Raw() []byte { return g.buf[g.start:g.end] }
