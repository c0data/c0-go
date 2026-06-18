package c0

import (
	"strings"
	"unicode/utf8"
)

// Glyph returns the Unicode Control Picture (U+2400 block) for a control byte.
func Glyph(b byte) rune { return rune(0x2400 + int(b)) }

func writeGlyph(sb *strings.Builder, b byte) { sb.WriteRune(Glyph(b)) }

// Format renders compact bytes as a human-readable Unicode string with two-space
// indentation (compact layout).
func Format(buf []byte) string { return FormatWith(buf, "  ") }

// FormatWith renders with a custom indent string.
func FormatWith(buf []byte, indent string) string {
	var sb strings.Builder
	formatCompact(buf, indent, &sb)
	return sb.String()
}

func writeDataUntilControl(buf []byte, pos int, sb *strings.Builder) int {
	for pos < len(buf) && buf[pos] >= 0x20 {
		sb.WriteByte(buf[pos])
		pos++
	}
	return pos
}

func writeIndent(sb *strings.Builder, indent string, depth int) {
	for i := 0; i < depth; i++ {
		sb.WriteString(indent)
	}
}

func writeFieldsLine(buf []byte, pos int, sb *strings.Builder) int {
	n := len(buf)
	for pos < n {
		b := buf[pos]
		switch {
		case b == US:
			writeGlyph(sb, US)
			pos++
		case b == DLE:
			writeGlyph(sb, DLE)
			pos++
			if pos < n {
				if buf[pos] < 0x20 {
					writeGlyph(sb, buf[pos])
				} else {
					sb.WriteByte(buf[pos])
				}
				pos++
			}
		case b == ENQ:
			writeGlyph(sb, ENQ)
			pos++
		case b == ETB:
			writeGlyph(sb, ETB)
			pos++
			for pos < n && buf[pos] >= 0x20 {
				sb.WriteByte(buf[pos])
				pos++
			}
		case b == STX:
			writeGlyph(sb, STX)
			pos++
			for pos < n {
				c := buf[pos]
				if c == ETX {
					writeGlyph(sb, ETX)
					pos++
					break
				} else if c == US {
					writeGlyph(sb, US)
					pos++
				} else if c < 0x20 {
					writeGlyph(sb, c)
					pos++
				} else {
					sb.WriteByte(c)
					pos++
				}
			}
		case b < 0x20:
			return pos
		default:
			sb.WriteByte(b)
			pos++
		}
	}
	return pos
}

func formatCompact(buf []byte, indent string, sb *strings.Builder) {
	n := len(buf)
	pos := 0
	depth := 0
	lineStart := true
	for pos < n {
		b := buf[pos]
		if b >= 0x20 {
			sb.WriteByte(b)
			pos++
			lineStart = false
			continue
		}
		switch b {
		case FS:
			if !lineStart {
				sb.WriteByte('\n')
			}
			writeGlyph(sb, b)
			pos++
			depth = 1
			pos = writeDataUntilControl(buf, pos, sb)
			sb.WriteByte('\n')
			lineStart = true
		case GS:
			run := 0
			for pos < n && buf[pos] == GS {
				run++
				pos++
			}
			if !lineStart {
				sb.WriteByte('\n')
			}
			writeIndent(sb, indent, depth)
			for k := 0; k < run; k++ {
				writeGlyph(sb, GS)
			}
			pos = writeDataUntilControl(buf, pos, sb)
			sb.WriteByte('\n')
			lineStart = true
		case SOH:
			writeIndent(sb, indent, depth+1)
			writeGlyph(sb, b)
			pos++
			pos = writeFieldsLine(buf, pos, sb)
			sb.WriteByte('\n')
			lineStart = true
		case RS:
			writeIndent(sb, indent, depth+1)
			writeGlyph(sb, b)
			pos++
			pos = writeFieldsLine(buf, pos, sb)
			sb.WriteByte('\n')
			lineStart = true
		case STX:
			writeGlyph(sb, b)
			pos++
			depth++
			sb.WriteByte('\n')
			lineStart = true
		case ETX:
			if depth > 0 {
				depth--
			}
			writeIndent(sb, indent, depth+1)
			writeGlyph(sb, b)
			pos++
		case EOT:
			if !lineStart {
				sb.WriteByte('\n')
			}
			writeGlyph(sb, b)
			sb.WriteByte('\n')
			pos++
			lineStart = true
		case ENQ:
			writeGlyph(sb, b)
			pos++
		case DLE:
			writeGlyph(sb, b)
			pos++
			if pos < n {
				if buf[pos] < 0x20 {
					writeGlyph(sb, buf[pos])
				} else {
					sb.WriteByte(buf[pos])
				}
				pos++
			}
		case SUB, US:
			writeGlyph(sb, b)
			pos++
		case ETB:
			if lineStart {
				writeIndent(sb, indent, depth+1)
			}
			writeGlyph(sb, b)
			pos++
			pos = writeDataUntilControl(buf, pos, sb)
			sb.WriteByte('\n')
			lineStart = true
		default:
			writeGlyph(sb, b)
			pos++
		}
	}
	if !lineStart {
		sb.WriteByte('\n')
	}
}

// Parse parses pretty-form text back to compact bytes. Control Pictures
// (U+2400–U+241F) become C0 bytes; LF/CR are ignored; whitespace adjacent to
// control codes is trimmed; inside STX/ETX everything is preserved verbatim.
func Parse(s string) []byte {
	out := make([]byte, 0, len(s))
	var ws []byte
	trimAfter := true
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		cp := uint32(r)
		switch {
		case cp >= 0x2400 && cp <= 0x241F:
			code := byte(cp - 0x2400)
			ws = ws[:0]
			out = append(out, code)
			i += size
			if code == STX {
				i = parseQuoted(s, i, &out)
			}
			trimAfter = true
		case r == '\n' || r == '\r':
			ws = ws[:0]
			trimAfter = true
			i += size
		case r == ' ' || r == '\t':
			if trimAfter {
				i += size
				continue
			}
			ws = append(ws, byte(r))
			i += size
		default:
			trimAfter = false
			if len(ws) > 0 {
				out = append(out, ws...)
				ws = ws[:0]
			}
			out = append(out, s[i:i+size]...)
			i += size
		}
	}
	return out
}

func parseQuoted(s string, i int, out *[]byte) int {
	depth := 1
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		cp := uint32(r)
		if cp >= 0x2400 && cp <= 0x241F {
			code := byte(cp - 0x2400)
			*out = append(*out, code)
			if code == STX {
				depth++
			} else if code == ETX {
				depth--
				if depth == 0 {
					return i + size
				}
			}
			i += size
		} else {
			*out = append(*out, s[i:i+size]...)
			i += size
		}
	}
	return i
}
