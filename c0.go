// Package c0 implements C0DATA — structured data using ASCII C0 control codes.
//
// Values are UTF-8 text (or any bytes, as text); structure is expressed through
// single-byte control codes. The read path is zero-copy: accessors return
// sub-slices of the input buffer. Outside text the hot loop is a single
// comparison, byte < 0x20.
//
// Parse entry points (ParseContainer, ParseDocument, ParseBody) validate the
// whole buffer against the grammar before indexing it, so the accessors of what
// they return never fail on malformed input.
package c0

import "fmt"

// Version is the C0DATA revision this package implements.
const Version = "0.3.0"

// Assigned C0 control codes (DESIGN.md "Assigned Control Codes").
const (
	SOH byte = 0x01 // Heading: a key for the value after it, or SOH RS a header row
	STX byte = 0x02 // Start of text (literal value)
	ETX byte = 0x03 // End of text
	EOT byte = 0x04 // End of document
	ENQ byte = 0x05 // Reference
	BEL byte = 0x07 // Comment: a note, or marks the next element dead
	DLE byte = 0x10 // Recurse: opens a nested level (outside text); escape (inside text)
	SYN byte = 0x16 // Start of document, then the heading
	ETB byte = 0x17 // Commit marker (stream mode)
	CAN byte = 0x18 // Close a nested level
	EM  byte = 0x19 // End of medium: raw blob follows
	SUB byte = 0x1a // Substitution (C0-DIFF)
	FS  byte = 0x1c // File separator
	GS  byte = 0x1d // Group separator
	RS  byte = 0x1e // Record separator
	US  byte = 0x1f // Unit separator
)

// Layout codes outside text.
const (
	HT byte = 0x09
	LF byte = 0x0a
	CR byte = 0x0d
	SP byte = 0x20
)

// assigned marks the structural and framing codes.
var assigned = [32]bool{
	SOH: true, STX: true, ETX: true, EOT: true, ENQ: true, BEL: true,
	DLE: true, SYN: true, ETB: true, CAN: true, EM: true, SUB: true,
	FS: true, GS: true, RS: true, US: true,
}

// textEscaped marks the seven codes that take a DLE inside text.
var textEscaped = [32]bool{STX: true, ETX: true, DLE: true, ETB: true, EOT: true, SYN: true, EM: true}

// IsAssigned reports whether b is an assigned C0 control code.
func IsAssigned(b byte) bool { return b < 0x20 && assigned[b] }

// IsLayout reports whether b is a layout byte (SP, HT, LF, CR).
func IsLayout(b byte) bool { return b == SP || b == HT || b == LF || b == CR }

// IsControl reports whether b is below 0x20.
func IsControl(b byte) bool { return b < 0x20 }

// ---------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------

// SyntaxError reports malformed input at a byte offset.
type SyntaxError struct {
	Msg string
	Pos int
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("c0: %s at byte %d", e.Msg, e.Pos) }

// UnassignedCodeError reports a control byte (< 0x20) that is not assigned
// outside text.
type UnassignedCodeError struct {
	Byte byte
	Pos  int
}

func (e *UnassignedCodeError) Error() string {
	return fmt.Sprintf("c0: unassigned control code 0x%02x at byte %d", e.Byte, e.Pos)
}

// UnexpectedEndError reports input that ends inside an open construct.
type UnexpectedEndError struct {
	What string
	Pos  int
}

func (e *UnexpectedEndError) Error() string {
	return fmt.Sprintf("c0: unexpected end of input: %s at byte %d", e.What, e.Pos)
}

// Errors from accessors asked for the wrong kind of unit, and from the
// builder.
type c0Error string

func (e c0Error) Error() string { return "c0: " + string(e) }

const (
	ErrNotLevel     c0Error = "unit is not a nested level"
	ErrNotList      c0Error = "level is not a list"
	ErrNotReference c0Error = "unit is not a reference"
	ErrNotName      c0Error = "not a legal name"
	ErrNoGroup      c0Error = "no such group"
	ErrNoLevel      c0Error = "CAN with no open level"
	ErrEmptyNote    c0Error = "a note needs content: bare BEL marks the next element dead"
)

// scanPanic carries a scan error up to the nearest catch.
type scanPanic struct{ err error }

func fail(msg string, pos int) { panic(scanPanic{&SyntaxError{Msg: msg, Pos: pos}}) }

func failEnd(what string, pos int) { panic(scanPanic{&UnexpectedEndError{What: what, Pos: pos}}) }

func failUnassigned(b byte, pos int) { panic(scanPanic{&UnassignedCodeError{Byte: b, Pos: pos}}) }

// catchScan turns a scan panic into an error; deferred at entry points.
func catchScan(err *error) {
	if r := recover(); r != nil {
		sp, ok := r.(scanPanic)
		if !ok {
			panic(r)
		}
		*err = sp.err
	}
}

// ---------------------------------------------------------------------
// Scanning primitives. All take a buffer, a position and a stop and
// return the position after the construct. They panic (scanPanic) on
// malformed input; entry points recover with catchScan.
// ---------------------------------------------------------------------

// scanLayout skips layout bytes.
func scanLayout(buf []byte, pos, stop int) int {
	for pos < stop && IsLayout(buf[pos]) {
		pos++
	}
	return pos
}

// scanText skips a text (STX … ETX) starting at the STX at pos and returns
// the position after the ETX.
func scanText(buf []byte, pos, stop int) int {
	start := pos
	pos++ // STX
	for pos < stop {
		switch b := buf[pos]; {
		case b == DLE:
			pos++
			if pos >= stop {
				failEnd("DLE at end of text", pos)
			}
			if !(buf[pos] < 0x20 && textEscaped[buf[pos]]) {
				fail("DLE before a byte that is not one of the seven", pos)
			}
			pos++
		case b == ETX:
			return pos + 1
		case b == STX:
			fail("STX inside text (text does not nest)", pos)
		case b == ETB || b == EOT || b == SYN || b == EM:
			fail("framing inside open text", pos)
		default:
			pos++
		}
	}
	failEnd(fmt.Sprintf("text opened at %d is not closed", start), pos)
	return pos
}

// scanLevel skips a nested level starting at the DLE at pos and returns the
// position after the matching CAN.
func scanLevel(buf []byte, pos, stop int) int {
	start := pos
	pos++ // DLE
	depth := 1
	for pos < stop {
		switch b := buf[pos]; {
		case b >= 0x20:
			pos++
		case b == STX:
			pos = scanText(buf, pos, stop)
		case b == DLE:
			depth++
			pos++
		case b == CAN:
			depth--
			pos++
			if depth == 0 {
				return pos
			}
		case b == ETB || b == EOT || b == SYN || b == EM:
			fail("framing inside open level", pos)
		default:
			pos++
		}
	}
	failEnd(fmt.Sprintf("level opened at %d is not closed", start), pos)
	return pos
}

// scanPlain skips a run of bytes >= 0x20 (a label, key, payload or plain
// value with its layout) and returns the position of the next control code
// or stop.
func scanPlain(buf []byte, pos, stop int) int {
	for pos < stop && buf[pos] >= 0x20 {
		pos++
	}
	return pos
}

// scanUnit finds, from just after a US, RS or key's US, the end of one
// unit's value: the next control code at this depth other than the ones
// that belong to the value (STX…ETX, DLE…CAN, ENQ, BEL note). Layout at
// the edges is included in the range.
func scanUnit(buf []byte, pos, stop int) int {
	for pos < stop {
		switch b := buf[pos]; {
		case b >= 0x20 || IsLayout(b):
			pos++
		case b == STX:
			pos = scanText(buf, pos, stop)
		case b == DLE:
			pos = scanLevel(buf, pos, stop)
		case b == ENQ:
			pos++
		case b == BEL:
			// a note or a dead level stays inside the unit; a dead
			// record-level element (BEL RS / SOH / GS / FS) ends it
			p2 := scanLayout(buf, pos+1, stop)
			if p2 < stop && buf[p2] < 0x20 && buf[p2] != STX && buf[p2] != DLE {
				return pos
			}
			pos = scanComment(buf, pos, stop)
		default:
			return pos
		}
	}
	return pos
}

// scanComment skips a comment starting at the BEL at pos: a note (text or
// plain to the next code) or a dead element (the next code's whole
// element). Returns the position after it.
func scanComment(buf []byte, pos, stop int) int {
	pos++ // BEL
	pos = scanLayout(buf, pos, stop)
	if pos >= stop {
		failEnd("BEL at end of input", pos)
	}
	switch b := buf[pos]; {
	case b >= 0x20:
		return scanPlain(buf, pos, stop)
	case b == STX:
		return scanText(buf, pos, stop)
	case b == DLE:
		return scanLevel(buf, pos, stop)
	case b == RS:
		return scanRecordEnd(buf, pos+1, stop)
	case b == SOH:
		// header row or keyed unit
		p2 := scanLayout(buf, pos+1, stop)
		if p2 < stop && buf[p2] == RS {
			return scanRecordEnd(buf, p2+1, stop)
		}
		return scanKeyedUnitEnd(buf, pos+1, stop)
	case b == GS:
		p2 := pos
		for p2 < stop && (buf[p2] == GS || IsLayout(buf[p2])) {
			p2++
		}
		return scanPlain(buf, p2, stop)
	case b == FS:
		return scanPlain(buf, pos+1, stop)
	default:
		fail("BEL before a code that is not an element", pos)
		return pos
	}
}

// scanRecordEnd finds, from just after an RS, the end of the record: the
// next record-level code at this depth.
func scanRecordEnd(buf []byte, pos, stop int) int {
	for pos < stop {
		switch b := buf[pos]; {
		case b >= 0x20 || IsLayout(b) || b == US || b == ENQ:
			pos++
		case b == SOH:
			// a header row (SOH RS) ends the record; a keyed unit is inside it
			p2 := scanLayout(buf, pos+1, stop)
			if p2 < stop && buf[p2] == RS {
				return pos
			}
			pos++
		case b == STX:
			pos = scanText(buf, pos, stop)
		case b == DLE:
			pos = scanLevel(buf, pos, stop)
		case b == BEL:
			// a note inside the record, or a dead element; a dead
			// record-level element (BEL RS / BEL GS …) ends this record first
			p2 := scanLayout(buf, pos+1, stop)
			if p2 < stop && (buf[p2] == RS || buf[p2] == GS || buf[p2] == FS) {
				return pos
			}
			if p2 < stop && buf[p2] == SOH {
				p3 := scanLayout(buf, p2+1, stop)
				if p3 < stop && buf[p3] == RS {
					return pos
				}
			}
			pos = scanComment(buf, pos, stop)
		default:
			return pos
		}
	}
	return pos
}

// scanKeyedUnitEnd skips, from just after an SOH (not a header row), the
// key, US and value of one keyed unit.
func scanKeyedUnitEnd(buf []byte, pos, stop int) int {
	pos = scanLayout(buf, pos, stop)
	pos = scanPlain(buf, pos, stop)
	if !(pos < stop && buf[pos] == US) {
		fail("key without a value (no US)", pos)
	}
	return scanUnit(buf, pos+1, stop)
}

// ---------------------------------------------------------------------
// Values
// ---------------------------------------------------------------------

// Trim removes layout from both ends of a raw slice.
func Trim(raw []byte) []byte {
	s, e := 0, len(raw)
	for s < e && IsLayout(raw[s]) {
		s++
	}
	for e > s && IsLayout(raw[e-1]) {
		e--
	}
	return raw[s:e]
}

func unitStartsWith(raw []byte, code byte) bool {
	t := Trim(raw)
	return len(t) > 0 && t[0] == code
}

// value returns the logical value of a raw unit slice: for text, the inside
// with its escapes decoded (zero-copy when there are none); for a plain
// value, the slice with edge layout removed. A note in the unit yields the
// empty value. Panics on malformed input.
func value(raw []byte) []byte {
	t := Trim(raw)
	if len(t) == 0 {
		return t
	}
	switch first := t[0]; {
	case first == STX:
		stop := scanText(t, 0, len(t))
		onlyNotesAfter(t, stop, "text cannot be part of a plain value")
		return DecodeText(t[1 : stop-1])
	case first == BEL:
		// a note, or a dead level: the unit is empty
		onlyNotesAfter(t, 0, "value after a comment in the same unit")
		return t[:0]
	case first == DLE:
		fail("a nested level is not a scalar value", 0)
	case first == ENQ:
		fail("a reference is not a scalar value", 0)
	}
	for i, b := range t {
		if b == BEL {
			// notes after the value: skipped
			onlyNotesAfter(t, i, "value after a note in the same unit")
			return Trim(t[:i])
		}
		if b < 0x20 {
			if IsLayout(b) {
				fail("layout code inside a plain value", i)
			}
			fail("control code inside a plain value", i)
		}
	}
	return t
}

// onlyNotesAfter checks that from pos to the end of the unit only notes
// (and layout) follow.
func onlyNotesAfter(t []byte, pos int, what string) {
	pos = scanLayout(t, pos, len(t))
	for pos < len(t) {
		if t[pos] != BEL {
			fail(what, pos)
		}
		pos = scanLayout(t, scanComment(t, pos, len(t)), len(t))
	}
}

// DecodeValue returns the logical value of a raw unit (the bytes of one
// field as Record.Field returns them): a text's inside with its escapes
// decoded, or a plain value with its edge layout removed.
func DecodeValue(raw []byte) (v []byte, err error) {
	defer catchScan(&err)
	return value(raw), nil
}

// DecodeText decodes the inside of a text: DLE escapes removed. Zero-copy
// when there are none. The input must be the inside of a well-formed text.
func DecodeText(inside []byte) []byte {
	first := -1
	for i, b := range inside {
		if b == DLE {
			first = i
			break
		}
	}
	if first < 0 {
		return inside
	}
	out := make([]byte, 0, len(inside))
	out = append(out, inside[:first]...)
	for i := first; i < len(inside); i++ {
		if inside[i] == DLE {
			i++
			if i >= len(inside) {
				break
			}
		}
		out = append(out, inside[i])
	}
	return out
}

// IsPlain reports whether a value can be written plain: no byte below 0x20
// and no edge whitespace.
func IsPlain(v []byte) bool {
	if len(v) == 0 {
		return true
	}
	if IsLayout(v[0]) || IsLayout(v[len(v)-1]) {
		return false
	}
	for _, b := range v {
		if b < 0x20 {
			return false
		}
	}
	return true
}

// AppendValue appends a value in its one canonical spelling: plain when it
// can be, text otherwise, with only the seven codes escaped.
func AppendValue(dst, v []byte) []byte {
	if IsPlain(v) {
		return append(dst, v...)
	}
	dst = append(dst, STX)
	for _, b := range v {
		if b < 0x20 && textEscaped[b] {
			dst = append(dst, DLE)
		}
		dst = append(dst, b)
	}
	return append(dst, ETX)
}

// IsName reports whether bytes are a legal name (label, key, header name):
// no byte below 0x20 and no edge whitespace. A name may be empty.
func IsName(v []byte) bool { return IsPlain(v) }
