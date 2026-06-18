// Package c0 implements C0DATA — structured data using ASCII C0 control codes.
//
// Values are plain UTF-8 text; structure is expressed through single-byte
// control codes. The read path is zero-copy: accessors return sub-slices of the
// input buffer (Go slices are views into the backing array). The hot loop is a
// single comparison, byte < 0x20.
package c0

import (
	"bytes"
	"errors"
	"fmt"
)

// Assigned C0 control codes.
const (
	SOH byte = 0x01 // Header (field name declarations)
	STX byte = 0x02 // Open nested sub-structure / reference scope
	ETX byte = 0x03 // Close nested sub-structure / reference scope
	EOT byte = 0x04 // End of document / message
	ENQ byte = 0x05 // Reference (enquiry — look up named data)
	DLE byte = 0x10 // Escape (next byte is literal)
	ETB byte = 0x17 // Commit marker (stream mode block terminator)
	SUB byte = 0x1a // Substitution (old → new, C0-DIFF)
	FS  byte = 0x1c // File / Database separator
	GS  byte = 0x1d // Group / Table / Section separator
	RS  byte = 0x1e // Record / Row separator
	US  byte = 0x1f // Unit / Field separator
)

// ErrUnexpectedEnd is returned when input ends immediately after a DLE escape.
var ErrUnexpectedEnd = errors.New("c0: unexpected end of input after DLE escape")

// UnassignedCodeError reports a control byte (< 0x20) that is not assigned.
type UnassignedCodeError struct {
	Byte byte
	Pos  int
}

func (e *UnassignedCodeError) Error() string {
	return fmt.Sprintf("c0: unassigned control code 0x%02x at position %d", e.Byte, e.Pos)
}

// IsAssigned reports whether b is an assigned C0 control code.
func IsAssigned(b byte) bool {
	switch b {
	case SOH, STX, ETX, EOT, ENQ, DLE, ETB, SUB, FS, GS, RS, US:
		return true
	}
	return false
}

// Unescape decodes DLE escapes, returning the logical bytes of a value. When
// the input contains no escapes it returns the input slice unchanged
// (zero-copy). A trailing DLE with nothing to escape (only on malformed input)
// is dropped.
func Unescape(buf []byte) []byte {
	first := bytes.IndexByte(buf, DLE)
	if first < 0 {
		return buf
	}
	out := make([]byte, 0, len(buf))
	out = append(out, buf[:first]...)
	for i := first; i < len(buf); {
		if buf[i] == DLE {
			i++
			if i >= len(buf) {
				break
			}
		}
		out = append(out, buf[i])
		i++
	}
	return out
}

// Canonical reports whether bytes are a canonical document unit for content
// addressing: well-formed, minimally escaped (DLE appears only before bytes
// < 0x20), and free of framing bytes (ETB, EOT). Stream logs validate per
// block, not with this.
func Canonical(buf []byte) bool {
	i := 0
	for i < len(buf) {
		b := buf[i]
		switch {
		case b == DLE:
			if i+1 >= len(buf) || buf[i+1] >= 0x20 {
				return false
			}
			i += 2
		case b == ETB || b == EOT:
			return false
		case b < 0x20:
			if !IsAssigned(b) {
				return false
			}
			i++
		default:
			i++
		}
	}
	return true
}

// skipNested advances past a STX/ETX nested scope, returning the position after
// the matching ETX.
func skipNested(buf []byte, pos, stop int) int {
	pos++ // skip STX
	depth := 1
	for pos < stop && depth > 0 {
		switch buf[pos] {
		case STX:
			depth++
		case ETX:
			depth--
		case DLE:
			pos++
		}
		pos++
	}
	return pos
}
