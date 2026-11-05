package c0

import "bytes"

// Validator is a recursive-descent check of a buffer against GRAMMAR.md.
// Run reports the first structural error; with the canonical flag it also
// applies the canonical constraints, collecting the reasons a unit is not
// canonical in Warnings.
type Validator struct {
	buf       []byte
	n         int
	canonical bool
	ok        bool
	diff      bool
	warnings  []string
}

// NewValidator returns a Validator over buf. With canonical set, Run also
// checks the canonical constraints.
func NewValidator(buf []byte, canonical bool) *Validator {
	return &Validator{buf: buf, n: len(buf), canonical: canonical, ok: true}
}

// Validate reports the first structural error in buf, or nil when it is
// well-formed.
func Validate(buf []byte) error { return NewValidator(buf, false).Run() }

// IsCanonical reports whether buf is a canonical unit: well-formed, no
// layout, one spelling per value, keys unique within a keyed record, no
// framing, no comments.
func IsCanonical(buf []byte) bool {
	v := NewValidator(buf, true)
	if v.Run() != nil {
		return false
	}
	return v.OK()
}

// OK reports whether the buffer met every canonical constraint (after Run).
func (v *Validator) OK() bool { return v.ok }

// Warnings lists the canonical constraints the buffer broke.
func (v *Validator) Warnings() []string { return v.warnings }

func (v *Validator) notCanonical(why string) {
	v.ok = false
	v.warnings = append(v.warnings, why)
}

// Run validates the buffer, returning the first error.
func (v *Validator) Run() (err error) {
	defer catchScan(&err)
	buf := v.buf
	pos := v.layout(0, v.n)
	first := true
	for pos < v.n {
		switch b := buf[pos]; b {
		case SYN:
			v.notCanonical("SYN in a canonical unit")
			pos = v.heading(pos + 1)
			first = true // the body that follows belongs to this document
		case EM:
			v.notCanonical("EM in a canonical unit")
			return nil // raw region follows
		default:
			if !first {
				fail("a document after the first must begin with SYN", pos)
			}
			pos = v.body(pos, v.n, true)
			// after a body only EOT, SYN, EM or end
			pos = v.layout(pos, v.n)
			first = false
			if pos < v.n {
				switch buf[pos] {
				case EOT:
					v.notCanonical("EOT in a canonical unit")
					pos = v.layout(pos+1, v.n)
					if pos < v.n && buf[pos] != SYN && buf[pos] != EM {
						v.unexpected("unexpected byte after EOT", pos)
					}
				case SYN, EM:
				default:
					v.unexpected("unexpected byte after document", pos)
				}
			}
		}
	}
	return nil
}

func (v *Validator) layout(pos, stop int) int {
	p2 := scanLayout(v.buf, pos, stop)
	if p2 != pos {
		v.notCanonical("layout bytes")
	}
	return p2
}

// heading parses keyed units and notes after SYN and returns the position
// of the first body code. It also decides diff vs data body.
func (v *Validator) heading(pos int) int {
	buf := v.buf
	var keys [][]byte
	pos = v.layout(pos, v.n)
	for pos < v.n {
		b := buf[pos]
		if b == SOH {
			p2 := v.layout(pos+1, v.n)
			if p2 < v.n && buf[p2] == RS {
				break // header row ends the heading
			}
			var k, val []byte
			k, val, pos = v.keyedUnit(pos+1, v.n, true)
			keys = append(keys, k)
			if bytes.Equal(k, []byte("shape")) && bytes.Equal(val, []byte("diff")) {
				v.diff = true
			}
		} else if b == BEL {
			v.notCanonical("comment")
			pos = scanComment(buf, pos, v.n)
		} else if b >= 0x20 {
			fail("plain text directly after SYN", pos)
		} else {
			break
		}
		pos = v.layout(pos, v.n)
	}
	v.checkKeys(keys)
	return pos
}

// checkKeys flags a duplicate key anywhere in a keyed record or heading.
func (v *Validator) checkKeys(keys [][]byte) {
	for i := 1; i < len(keys); i++ {
		for j := 0; j < i; j++ {
			if bytes.Equal(keys[j], keys[i]) {
				v.notCanonical("duplicate key")
				return
			}
		}
	}
}

// body validates a body (data or diff) from pos to stop. depth0 allows
// commits.
func (v *Validator) body(pos, stop int, depth0 bool) int {
	if v.diff && depth0 {
		return v.diffBody(pos, stop)
	}
	buf := v.buf
	pos = v.layout(pos, stop)
	pos = v.part(pos, stop, depth0)
	for pos < stop {
		b := buf[pos]
		if b == FS {
			pos = v.plainName(pos+1, stop)
			pos = v.part(pos, stop, depth0)
		} else if b == BEL {
			p2 := scanLayout(buf, pos+1, stop)
			if p2 < stop && buf[p2] == FS {
				v.notCanonical("dead element")
				pos = v.plainName(p2+1, stop)
				pos = v.part(pos, stop, depth0)
			} else {
				break
			}
		} else {
			break
		}
	}
	return pos
}

// part = *pre [header-row] *item *group
func (v *Validator) part(pos, stop int, depth0 bool) int {
	buf := v.buf
	pos = v.pre(pos, stop, depth0)
	pos = v.headerRow(pos, stop)
	pos = v.items(pos, stop, depth0)
	for pos < stop {
		b := buf[pos]
		if b == GS {
			pos = v.group(pos, stop, depth0)
		} else if b == BEL {
			p2 := scanLayout(buf, pos+1, stop)
			if p2 < stop && buf[p2] == GS {
				v.notCanonical("dead element")
				pos = v.group(p2, stop, depth0)
			} else {
				break
			}
		} else {
			break
		}
	}
	return pos
}

func (v *Validator) group(pos, stop int, depth0 bool) int {
	for pos < stop && v.buf[pos] == GS {
		pos = v.layout(pos+1, stop)
	}
	pos = v.plainName(pos, stop)
	pos = v.pre(pos, stop, depth0)
	pos = v.headerRow(pos, stop)
	return v.items(pos, stop, depth0)
}

// pre handles what may precede a header row: notes, commits (at depth
// zero), dead records and dead header rows.
func (v *Validator) pre(pos, stop int, depth0 bool) int {
	buf := v.buf
	for {
		pos = v.layout(pos, stop)
		if pos >= stop {
			return pos
		}
		b := buf[pos]
		if b == ETB {
			pos = v.commit(pos, stop, depth0)
		} else if b == BEL {
			p2 := scanLayout(buf, pos+1, stop)
			if p2 >= stop {
				failEnd("BEL at end of input", p2)
			}
			switch {
			case buf[p2] >= 0x20 || buf[p2] == STX:
				pos = v.notes(pos, stop)
			case buf[p2] == RS:
				v.notCanonical("dead element")
				pos = v.record(p2+1, stop)
			case buf[p2] == SOH:
				p3 := scanLayout(buf, p2+1, stop)
				if !(p3 < stop && buf[p3] == RS) {
					fail("keyed unit outside a record", p2)
				}
				v.notCanonical("dead element")
				pos = v.headerNames(p3+1, stop)
			default:
				return pos
			}
		} else {
			return pos
		}
	}
}

func (v *Validator) commit(pos, stop int, depth0 bool) int {
	if !depth0 {
		fail("commit inside a nested level", pos)
	}
	v.notCanonical("commit")
	return scanPlain(v.buf, v.layout(pos+1, stop), stop)
}

func (v *Validator) headerNames(pos, stop int) int {
	pos = v.layout(pos, stop)
	for {
		pos = v.plainName(pos, stop)
		if !(pos < stop && v.buf[pos] == US) {
			return pos
		}
		pos = v.layout(pos+1, stop)
	}
}

// notes skips notes only (BEL followed by plain/text); dead elements are
// handled by their element rules.
func (v *Validator) notes(pos, stop int) int {
	buf := v.buf
	for {
		pos = v.layout(pos, stop)
		if !(pos < stop && buf[pos] == BEL) {
			return pos
		}
		p2 := scanLayout(buf, pos+1, stop)
		if p2 >= stop {
			failEnd("BEL at end of input", p2)
		}
		if buf[p2] < 0x20 && buf[p2] != STX {
			return pos // a dead element, not a note
		}
		v.notCanonical("comment")
		if buf[p2] == STX {
			pos = scanText(buf, p2, stop)
		} else {
			pos = scanPlain(buf, p2, stop)
		}
	}
}

func (v *Validator) headerRow(pos, stop int) int {
	buf := v.buf
	pos = v.layout(pos, stop)
	if !(pos < stop && buf[pos] == SOH) {
		return pos
	}
	p2 := v.layout(pos+1, stop)
	if !(p2 < stop && buf[p2] == RS) {
		return pos
	}
	return v.headerNames(p2+1, stop)
}

func (v *Validator) items(pos, stop int, depth0 bool) int {
	buf := v.buf
	for {
		pos = v.layout(pos, stop)
		if pos >= stop {
			return pos
		}
		switch b := buf[pos]; b {
		case RS:
			pos = v.record(pos+1, stop)
		case ETB:
			pos = v.commit(pos, stop, depth0)
		case BEL:
			p2 := scanLayout(buf, pos+1, stop)
			switch {
			case p2 >= stop || buf[p2] >= 0x20 || buf[p2] == STX:
				pos = v.notes(pos, stop)
			case buf[p2] == RS:
				v.notCanonical("dead element")
				pos = v.record(p2+1, stop)
			case buf[p2] == SOH:
				p3 := scanLayout(buf, p2+1, stop)
				if !(p3 < stop && buf[p3] == RS) {
					fail("keyed unit outside a record", p2)
				}
				v.notCanonical("dead element")
				pos = v.headerNames(p3+1, stop)
			case buf[p2] == DLE:
				fail("a nested level is not an item", p2)
			default:
				return pos // dead GS/FS handled by caller; others are errors there
			}
		case SOH:
			p2 := scanLayout(buf, pos+1, stop)
			if p2 < stop && buf[p2] == RS {
				fail("header row not at the start of its group", pos)
			}
			fail("keyed unit outside a record", pos)
		default:
			return pos
		}
	}
}

// record validates one record after its RS and returns the position after
// it.
func (v *Validator) record(pos, stop int) int {
	buf := v.buf
	pos = v.layout(pos, stop)
	if v.keyedStart(pos, stop) {
		var keys [][]byte
	keyed:
		for {
			pos = v.layout(pos, stop)
			if pos >= stop {
				break
			}
			switch b := buf[pos]; b {
			case SOH:
				p2 := scanLayout(buf, pos+1, stop)
				if p2 < stop && buf[p2] == RS {
					break keyed
				}
				var k []byte
				k, _, pos = v.keyedUnit(pos+1, stop, false)
				keys = append(keys, k)
			case BEL:
				p2 := scanLayout(buf, pos+1, stop)
				if p2 >= stop {
					failEnd("BEL at end of input", p2)
				}
				if buf[p2] == SOH {
					p3 := scanLayout(buf, p2+1, stop)
					if p3 < stop && buf[p3] == RS {
						break keyed
					}
					v.notCanonical("dead element")
					_, _, pos = v.keyedUnit(p2+1, stop, false)
				} else if buf[p2] >= 0x20 || buf[p2] == STX {
					pos = v.notes(pos, stop)
				} else {
					break keyed
				}
			default:
				break keyed
			}
		}
		v.checkKeys(keys)
		if pos < stop && buf[pos] == US {
			fail("US after a keyed value (mixed record)", pos)
		}
		return pos
	}
	// positional
	for {
		pos = v.value(pos, stop, false)
		pos = v.layout(pos, stop)
		if pos >= stop {
			return pos
		}
		switch buf[pos] {
		case US:
			pos++
		case SOH:
			p2 := scanLayout(buf, pos+1, stop)
			if p2 < stop && buf[p2] == RS {
				return pos
			}
			fail("keyed unit in a positional record (mixed record)", pos)
		default:
			return pos
		}
	}
}

// keyedStart reports whether a record starting here begins with a (live or
// dead) keyed unit.
func (v *Validator) keyedStart(pos, stop int) bool {
	buf := v.buf
	for pos < stop {
		b := buf[pos]
		if b == SOH {
			return true
		}
		if b != BEL {
			return false
		}
		p2 := scanLayout(buf, pos+1, stop)
		if p2 >= stop {
			return false
		}
		if buf[p2] == SOH {
			return true
		}
		if buf[p2] < 0x20 && buf[p2] != STX {
			return false
		}
		pos = scanLayout(buf, scanComment(buf, pos, stop), stop)
	}
	return false
}

// keyedUnit validates a keyed unit after its SOH and returns the key, the
// trimmed raw value and the position after the unit.
func (v *Validator) keyedUnit(pos, stop int, heading bool) ([]byte, []byte, int) {
	buf := v.buf
	pos = v.layout(pos, stop)
	ks := pos
	pos = scanPlain(buf, pos, stop)
	key := Trim(buf[ks:pos])
	if !(pos < stop && buf[pos] == US) {
		fail("key without a value (no US)", pos)
	}
	vs := pos + 1
	pos = v.value(vs, stop, heading)
	return key, Trim(buf[vs:pos]), pos
}

func secondValue(b byte) bool { return b >= 0x20 || b == STX || b == DLE || b == ENQ }

// value validates one (possibly empty) value starting at pos and returns
// the position after it, before the next code at this depth.
func (v *Validator) value(pos, stop int, heading bool) int {
	buf := v.buf
	pos = v.layout(pos, stop)
	if pos >= stop {
		return pos
	}
	switch b := buf[pos]; {
	case b >= 0x20:
		pos = scanPlain(buf, pos, stop)
		if IsLayout(buf[pos-1]) {
			v.notCanonical("layout bytes")
		}
		after := v.trailingNotes(v.layout(pos, stop), stop)
		if after < stop && secondValue(buf[after]) {
			fail("two values in one unit", after)
		}
		return after
	case b == STX:
		start := pos
		pos = scanText(buf, pos, stop)
		inside := buf[start+1 : pos-1]
		if v.canonical {
			if len(inside) == 0 {
				v.notCanonical("empty text")
			} else if IsPlain(DecodeText(inside)) {
				v.notCanonical("gratuitous text")
			}
		}
		after := v.trailingNotes(v.layout(pos, stop), stop)
		if after < stop && secondValue(buf[after]) {
			fail("two values in one unit", after)
		}
		return after
	case b == DLE:
		pos = v.level(pos, stop)
		after := v.trailingNotes(v.layout(pos, stop), stop)
		if after < stop && secondValue(buf[after]) {
			fail("two values in one unit", after)
		}
		return after
	case b == ENQ:
		if heading {
			fail("reference in a heading", pos)
		}
		pos = v.layout(pos+1, stop)
		if pos >= stop || (buf[pos] < 0x20 && buf[pos] != STX) {
			fail("reference without a name", pos)
		}
		if buf[pos] == STX {
			start := pos
			pos = scanText(buf, pos, stop)
			inside := DecodeText(buf[start+1 : pos-1])
			var segs [][]byte
			if len(inside) > 0 {
				segs = bytes.Split(inside, []byte{US})
			}
			for _, s := range segs {
				if len(s) == 0 {
					fail("reference path with an empty segment", start)
				}
			}
			if len(segs) > 3 {
				fail("reference path with more than three segments", start)
			}
			if len(segs) == 1 {
				v.notCanonical("group reference written as text")
			}
		} else {
			pos = scanPlain(buf, pos, stop)
		}
		return v.trailingNotes(v.layout(pos, stop), stop)
	case b == BEL:
		// a note or dead level in unit position: the unit is empty
		p2 := scanLayout(buf, pos+1, stop)
		if p2 >= stop {
			failEnd("BEL at end of input", p2)
		}
		switch {
		case buf[p2] == DLE:
			v.notCanonical("dead element")
			pos = v.level(p2, stop)
		case buf[p2] < 0x20 && buf[p2] != STX:
			return pos // a dead record-level element: handled by the caller
		case buf[p2] == STX:
			v.notCanonical("comment")
			pos = scanText(buf, p2, stop)
		default:
			v.notCanonical("comment")
			pos = scanPlain(buf, p2, stop)
		}
		return v.layout(pos, stop)
	default:
		return pos
	}
}

// trailingNotes skips notes (BEL + content) after a value inside a unit; a
// BEL before a code is left for the caller.
func (v *Validator) trailingNotes(pos, stop int) int {
	buf := v.buf
	for pos < stop && buf[pos] == BEL {
		p2 := scanLayout(buf, pos+1, stop)
		if p2 >= stop {
			failEnd("BEL at end of input", p2)
		}
		if !(buf[p2] >= 0x20 || buf[p2] == STX) {
			break
		}
		v.notCanonical("comment")
		if buf[p2] == STX {
			pos = scanText(buf, p2, stop)
		} else {
			pos = scanPlain(buf, p2, stop)
		}
		pos = v.layout(pos, stop)
	}
	return pos
}

// level validates a nested level at the DLE at pos and returns the position
// after its CAN.
func (v *Validator) level(pos, stop int) int {
	buf := v.buf
	pos = v.layout(pos+1, stop)
	if pos >= stop {
		failEnd("level not closed", pos)
	}
	b := buf[pos]
	if b == CAN {
		return pos + 1
	}
	if !(b == FS || b == GS || b == RS || b == SOH || b == BEL) {
		fail("DLE must be followed by a structural code", pos)
	}
	pos = v.body(pos, stop, false)
	pos = v.layout(pos, stop)
	if pos >= stop {
		failEnd("level not closed", pos)
	}
	if buf[pos] != CAN {
		v.unexpected("unexpected byte inside level", pos)
	}
	return pos + 1
}

// unexpected reports a stray byte: an unassigned code by name, anything else
// with msg.
func (v *Validator) unexpected(msg string, pos int) {
	if b := v.buf[pos]; b < 0x20 && !assigned[b] {
		failUnassigned(b, pos)
	}
	fail(msg, pos)
}

func (v *Validator) plainName(pos, stop int) int {
	buf := v.buf
	pos = v.layout(pos, stop)
	if pos < stop && buf[pos] == STX {
		fail("a name cannot be text", pos)
	}
	start := pos
	pos = scanPlain(buf, pos, stop)
	if pos > start && IsLayout(buf[pos-1]) {
		v.notCanonical("layout bytes")
	}
	return pos
}

// ---- diff body (GRAMMAR.md "C0-DIFF") -----------------------------------

func (v *Validator) diffBody(pos, stop int) int {
	buf := v.buf
	pos = v.layout(pos, stop)
	for pos < stop {
		switch b := buf[pos]; b {
		case FS:
			pos = v.plainName(pos+1, stop)
		case GS:
			pos = v.pattern(pos+1, stop)
		case RS:
			pos = v.replace(pos+1, stop)
		case BEL:
			p2 := scanLayout(buf, pos+1, stop)
			switch {
			case p2 < stop && buf[p2] == FS:
				pos = v.plainName(p2+1, stop)
			case p2 < stop && buf[p2] == GS:
				pos = v.pattern(p2+1, stop)
			case p2 < stop && buf[p2] == RS:
				pos = v.replace(p2+1, stop)
			default:
				pos = v.notes(pos, stop)
			}
		case ETB:
			v.notCanonical("commit")
			pos = scanPlain(buf, v.layout(pos+1, stop), stop)
		case EOT, SYN, EM:
			return pos
		default:
			fail("unexpected byte in a diff body", pos)
		}
		pos = v.layout(pos, stop)
	}
	return pos
}

func (v *Validator) dvalue(pos, stop int) int {
	buf := v.buf
	pos = v.layout(pos, stop)
	if pos >= stop {
		return pos
	}
	if buf[pos] == STX {
		pos = scanText(buf, pos, stop)
	} else if buf[pos] >= 0x20 {
		pos = scanPlain(buf, pos, stop)
	}
	return v.layout(pos, stop)
}

func (v *Validator) pattern(pos, stop int) int {
	buf := v.buf
	for {
		pos = v.dvalue(pos, stop)
		if pos < stop && buf[pos] == SUB {
			pos = v.dvalue(pos+1, stop)
		}
		pos = v.trailingNotes(pos, stop)
		if !(pos < stop && buf[pos] == US) {
			return pos
		}
		pos++
	}
}

func (v *Validator) replace(pos, stop int) int {
	buf := v.buf
	start := scanLayout(buf, pos, stop)
	pos = v.dvalue(pos, stop)
	if len(Trim(buf[start:pos])) == 0 {
		fail("replace-all with an empty old part", start)
	}
	if !(pos < stop && buf[pos] == SUB) {
		fail("replace-all without SUB", pos)
	}
	return v.dvalue(pos+1, stop)
}
