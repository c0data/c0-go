package c0

import (
	"strconv"
	"strings"
)

// Pretty form: Unicode Control Pictures (U+2400 block) for the codes, line
// breaks and indentation as layout. Format and Parse are inverses on
// well-formed input.

// FormatMode selects the layout Format produces.
type FormatMode int

// Format modes.
const (
	FormatCompact FormatMode = iota // one element per line, no padding
	FormatAligned                   // column-aligned units within table groups
	FormatSpaced                    // aligned, plus a space after the prefix and around ␟
)

// GlyphInDataError reports a value holding a Control Picture character,
// which pretty form cannot show unambiguously.
type GlyphInDataError struct{}

func (*GlyphInDataError) Error() string {
	return "c0: value contains a Control Picture character and cannot be shown in pretty form"
}

// Glyph returns the Unicode Control Picture (U+2400 block) for a control
// byte.
func Glyph(b byte) rune { return rune(0x2400 + int(b)) }

func isGlyph(r rune) bool { return r >= 0x2400 && r <= 0x241F }

// Format renders compact bytes in pretty form with two-space indentation
// (FormatCompact).
func Format(buf []byte) (string, error) { return FormatWith(buf, "  ", FormatCompact) }

// FormatWith renders with a custom indent string and mode.
func FormatWith(buf []byte, indent string, mode FormatMode) (out string, err error) {
	defer catchScan(&err)
	f := &formatter{buf: buf, indent: indent, lineStart: true}
	f.run()
	out = f.sb.String()
	if mode != FormatCompact {
		out = Align(out, mode)
	}
	return out, nil
}

type formatter struct {
	buf       []byte
	sb        strings.Builder
	indent    string
	depth     int // nesting depth (levels)
	gdepth    int // group depth within the current level
	lineStart bool
	stack     []int
}

func (f *formatter) glyph(b byte) { f.sb.WriteRune(Glyph(b)) }

func (f *formatter) nl() {
	if !f.lineStart {
		f.sb.WriteByte('\n')
	}
}

func (f *formatter) writeIndent(depth int) {
	for i := 0; i < depth; i++ {
		f.sb.WriteString(f.indent)
	}
}

func (f *formatter) run() {
	buf := f.buf
	n := len(buf)
	pos := 0
	for pos < n {
		b := buf[pos]
		if b >= 0x20 || IsLayout(b) {
			// stray plain or layout at element level: layout is dropped
			if IsLayout(b) {
				pos++
			} else {
				start := pos
				pos = scanPlain(buf, pos, n)
				f.writeData(buf[start:pos])
				f.lineStart = false
			}
			continue
		}
		switch b {
		case SYN:
			f.nl()
			f.glyph(SYN)
			pos = f.writeHeading(pos + 1)
			f.sb.WriteByte('\n')
			f.lineStart = true
			f.gdepth = 0
		case FS:
			f.nl()
			f.writeIndent(f.depth)
			f.glyph(FS)
			pos = f.writePlain(pos + 1)
			f.sb.WriteByte('\n')
			f.lineStart = true
			f.gdepth = 1
		case GS:
			run := 0
			for pos < n && buf[pos] == GS {
				run++
				pos++
			}
			f.nl()
			f.writeIndent(f.depth)
			for i := 0; i < run; i++ {
				f.glyph(GS)
			}
			pos = f.writePlain(pos)
			f.sb.WriteByte('\n')
			f.lineStart = true
			f.gdepth = 1
		case SOH, RS, BEL, ETB:
			f.nl()
			f.writeIndent(f.depth + f.gdepth)
			pos = f.writeElement(pos)
		case CAN:
			if f.depth > 0 {
				f.depth--
			}
			f.gdepth = 0
			if len(f.stack) > 0 {
				f.gdepth = f.stack[len(f.stack)-1]
				f.stack = f.stack[:len(f.stack)-1]
			}
			f.nl()
			f.writeIndent(f.depth + f.gdepth)
			f.glyph(CAN)
			pos++
			f.lineStart = false
			// the rest of the enclosing record continues on this line
			pos = f.writeUnits(pos)
			if !f.lineStart {
				f.sb.WriteByte('\n')
				f.lineStart = true
			}
		case EOT:
			f.nl()
			f.glyph(EOT)
			f.sb.WriteByte('\n')
			pos++
			f.lineStart = true
		case EM:
			f.nl()
			f.glyph(EM)
			if tail := n - pos - 1; tail > 0 {
				f.sb.WriteString(" " + strconv.Itoa(tail) + " bytes")
			}
			f.sb.WriteByte('\n')
			return
		case STX, DLE, ENQ, US, SUB:
			// a unit-level code at element level (e.g. a diff pattern): write inline
			pos = f.writeUnits(pos)
			f.sb.WriteByte('\n')
			f.lineStart = true
		default:
			failUnassigned(b, pos)
		}
	}
	f.nl()
}

// writeElement writes an SOH / RS / BEL / ETB at element level: the element
// and its units on one line (a level opener breaks the line).
func (f *formatter) writeElement(pos int) int {
	buf := f.buf
	n := len(buf)
	f.lineStart = false
	switch buf[pos] {
	case BEL:
		f.glyph(BEL)
		pos = scanLayout(buf, pos+1, n)
		if pos < n && (buf[pos] >= 0x20 || buf[pos] == STX) {
			pos = f.writeUnits(pos)
			f.sb.WriteByte('\n')
			f.lineStart = true
			return pos
		}
		if pos >= n {
			return pos
		}
		return f.writeElement(pos) // dead element: rendered on this line
	case SOH:
		f.glyph(SOH)
		pos = scanLayout(buf, pos+1, n)
		if pos < n && buf[pos] == RS {
			f.glyph(RS)
			pos++
		}
		pos = f.writeUnits(pos)
	case RS:
		f.glyph(RS)
		pos = f.writeUnits(pos + 1)
	case ETB:
		f.glyph(ETB)
		pos = f.writePlain(pos + 1)
	}
	if !f.lineStart {
		f.sb.WriteByte('\n')
		f.lineStart = true
	}
	return pos
}

// writeUnits writes the units of a record/header/heading on the current
// line and stops at a record-level code. A level opener writes ␐, breaks the
// line and raises the depth; the caller resumes at the matching CAN.
func (f *formatter) writeUnits(pos int) int {
	buf := f.buf
	n := len(buf)
loop:
	for pos < n {
		switch b := buf[pos]; {
		case b >= 0x20:
			start := pos
			pos = scanPlain(buf, pos, n)
			f.writeData(Trim(buf[start:pos]))
		case IsLayout(b):
			pos++
		case b == US || b == ENQ || b == SUB:
			f.glyph(b)
			pos++
		case b == SOH:
			p2 := scanLayout(buf, pos+1, n)
			if p2 < n && buf[p2] == RS {
				break loop
			}
			f.glyph(SOH)
			pos++
		case b == STX:
			stop := scanText(buf, pos, n)
			f.writeText(buf[pos:stop])
			pos = stop
		case b == DLE:
			f.glyph(DLE)
			f.sb.WriteByte('\n')
			f.stack = append(f.stack, f.gdepth)
			f.depth++
			f.lineStart = true
			return pos + 1
		case b == BEL:
			p2 := scanLayout(buf, pos+1, n)
			if !(p2 < n && (buf[p2] >= 0x20 || buf[p2] == STX || buf[p2] == DLE)) {
				break loop
			}
			f.glyph(BEL)
			pos = p2
		case b == ETB:
			f.glyph(ETB)
			pos = f.writePlain(pos + 1)
		default:
			break loop
		}
	}
	f.lineStart = false
	return pos
}

func (f *formatter) writeHeading(pos int) int {
	buf := f.buf
	n := len(buf)
	for pos < n {
		switch b := buf[pos]; {
		case b == SOH:
			p2 := scanLayout(buf, pos+1, n)
			if p2 < n && buf[p2] == RS {
				return pos
			}
			f.glyph(SOH)
			pos = f.writePlain(p2)
			if pos < n && buf[pos] == US {
				f.glyph(US)
				pos = scanLayout(buf, pos+1, n)
				switch {
				case pos < n && buf[pos] == STX:
					stop := scanText(buf, pos, n)
					f.writeText(buf[pos:stop])
					pos = stop
				case pos < n && buf[pos] == DLE:
					stop := scanLevel(buf, pos, n)
					f.writeInlineLevel(buf[pos:stop]) // a nested heading value is written inline
					pos = stop
				default:
					pos = f.writePlain(pos)
				}
			}
		case b == BEL:
			p2 := scanLayout(buf, pos+1, n)
			switch {
			case p2 < n && (buf[p2] >= 0x20 || buf[p2] == STX):
				f.glyph(BEL)
				pos = p2
				if buf[pos] == STX {
					stop := scanText(buf, pos, n)
					f.writeText(buf[pos:stop])
					pos = stop
				} else {
					pos = f.writePlain(pos)
				}
			case p2 < n && buf[p2] == SOH:
				f.glyph(BEL)
				pos = p2
			default:
				return pos
			}
		case IsLayout(b):
			pos++
		default:
			return pos
		}
	}
	return pos
}

// writeInlineLevel writes a level on one line (used for heading values).
func (f *formatter) writeInlineLevel(raw []byte) {
	for i := 0; i < len(raw); {
		switch b := raw[i]; {
		case b == STX:
			stop := scanText(raw, i, len(raw))
			f.writeText(raw[i:stop])
			i = stop
		case b >= 0x20:
			start := i
			i = scanPlain(raw, i, len(raw))
			f.writeData(Trim(raw[start:i]))
		case IsLayout(b):
			i++
		default:
			f.glyph(b)
			i++
		}
	}
}

func (f *formatter) writePlain(pos int) int {
	pos = scanLayout(f.buf, pos, len(f.buf))
	start := pos
	pos = scanPlain(f.buf, pos, len(f.buf))
	f.writeData(Trim(f.buf[start:pos]))
	return pos
}

// writeText writes a whole text (STX … ETX): glyphs for the brackets and for
// any control byte inside; everything else as is.
func (f *formatter) writeText(text []byte) {
	checkGlyphs(text)
	f.glyph(STX)
	for _, b := range text[1 : len(text)-1] {
		if b < 0x20 && b != LF && b != CR && b != HT {
			f.glyph(b)
		} else {
			f.sb.WriteByte(b)
		}
	}
	f.glyph(ETX)
}

func (f *formatter) writeData(data []byte) {
	checkGlyphs(data)
	f.sb.Write(data)
}

// checkGlyphs rejects a value that contains a Control Picture character
// (U+2400–U+241F, UTF-8 E2 90 80–9F).
func checkGlyphs(data []byte) {
	for i := 0; i+2 < len(data); i++ {
		if data[i] == 0xE2 && data[i+1] == 0x90 && data[i+2] >= 0x80 && data[i+2] <= 0x9F {
			panic(scanPanic{&GlyphInDataError{}})
		}
	}
}

// ---- parsing ---------------------------------------------------------

// Parse converts pretty form to compact bytes. Glyphs become codes; outside
// text, layout touching a code is dropped and interior spaces are kept;
// inside text everything is kept.
func Parse(s string) []byte {
	out := make([]byte, 0, len(s))
	chars := []rune(s)
	var pending []byte // whitespace between data characters
	afterCode := true
	for i := 0; i < len(chars); {
		ch := chars[i]
		switch {
		case isGlyph(ch):
			code := byte(ch - 0x2400)
			pending = pending[:0]
			out = append(out, code)
			i++
			if code == STX {
				i = parseText(chars, i, &out)
			} else if code == EM {
				// the rest of the line is a summary, not data
				for i < len(chars) && chars[i] != '\n' {
					i++
				}
			}
			afterCode = true
		case ch == '\n' || ch == '\r':
			pending = pending[:0]
			afterCode = true
			i++
		case ch == ' ' || ch == '\t':
			if !afterCode {
				pending = append(pending, byte(ch))
			}
			i++
		default:
			out = append(out, pending...)
			pending = pending[:0]
			afterCode = false
			out = append(out, string(ch)...)
			i++
		}
	}
	return out
}

// parseText copies text verbatim until the ETX glyph; a DLE glyph takes the
// next glyph with it.
func parseText(chars []rune, i int, out *[]byte) int {
	for i < len(chars) {
		ch := chars[i]
		if isGlyph(ch) {
			code := byte(ch - 0x2400)
			*out = append(*out, code)
			i++
			if code == DLE && i < len(chars) && isGlyph(chars[i]) {
				*out = append(*out, byte(chars[i]-0x2400))
				i++
			} else if code == ETX {
				return i
			}
		} else {
			*out = append(*out, string(ch)...)
			i++
		}
	}
	return i
}

// ---- alignment -------------------------------------------------------

var (
	gFS  = Glyph(FS)
	gGS  = Glyph(GS)
	gRS  = Glyph(RS)
	gSOH = Glyph(SOH)
	gUS  = Glyph(US)
	gEOT = Glyph(EOT)
	gSTX = Glyph(STX)
	gETX = Glyph(ETX)
	gDLE = Glyph(DLE)
	gCAN = Glyph(CAN)
	gSYN = Glyph(SYN)
	gBEL = Glyph(BEL)
)

func isPrefixGlyph(r rune) bool {
	switch r {
	case gFS, gGS, gRS, gSOH, gUS, gSYN, gBEL:
		return true
	}
	return false
}

// tableLine is a ␞ or ␁␞ line split into units. seps[i] is what stood
// between fields[i] and fields[i+1]: gUS, or 0 when the next field begins
// with its own ␁ mark.
type tableLine struct {
	index  int
	prefix string
	fields []string
	seps   []rune
}

// Align reformats a pretty-form string with column alignment. Keyed records
// (␁ units) are aligned on their ␟ like two-column tables.
func Align(pretty string, mode FormatMode) string {
	lines := strings.Split(pretty, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	groups := findTableGroups(lines)
	spaced := mode == FormatSpaced
	sp := ""
	if spaced {
		sp = " "
	}

	tableLines := map[int]bool{}
	for _, g := range groups {
		for _, l := range g {
			tableLines[l.index] = true
		}
	}

	for _, group := range groups {
		colCount := 0
		for _, l := range group {
			colCount = max(colCount, len(l.fields))
		}
		widths := make([]int, colCount)
		for _, l := range group {
			for c, fld := range l.fields {
				widths[c] = max(widths[c], len([]rune(fld)))
			}
		}
		for _, l := range group {
			var sb strings.Builder
			sb.WriteString(l.prefix + sp)
			for col, fld := range l.fields {
				sb.WriteString(fld)
				if col == len(l.fields)-1 {
					break
				}
				sb.WriteString(strings.Repeat(" ", widths[col]-len([]rune(fld))))
				if l.seps[col] == gUS {
					sb.WriteString(sp)
					sb.WriteRune(gUS)
					sb.WriteString(sp)
				} else {
					sb.WriteByte(' ')
				}
			}
			lines[l.index] = sb.String()
		}
	}

	for i, text := range lines {
		if tableLines[i] || strings.TrimSpace(text) == "" {
			continue
		}
		rs := []rune(text)
		wsEnd := 0
		for wsEnd < len(rs) && (rs[wsEnd] == ' ' || rs[wsEnd] == '\t') {
			wsEnd++
		}
		glyphEnd := wsEnd
		for glyphEnd < len(rs) && isPrefixGlyph(rs[glyphEnd]) {
			glyphEnd++
		}
		if glyphEnd == wsEnd || glyphEnd >= len(rs) {
			continue
		}
		rest := strings.TrimLeft(string(rs[glyphEnd:]), " \t")
		lines[i] = string(rs[:glyphEnd]) + sp + rest
	}

	return strings.Join(lines, "\n") + "\n"
}

func findTableGroups(lines []string) [][]tableLine {
	var groups [][]tableLine
	var current []tableLine
	flush := func() {
		if len(current) > 0 {
			groups = append(groups, current)
		}
		current = nil
	}
	for i, text := range lines {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || hasPrefixRune(trimmed, gGS, gFS, gEOT, gSYN, gCAN) {
			flush()
			continue
		}
		if hasPrefixRune(trimmed, gSOH, gRS) {
			parsed, ok := parseTableLine(i, text)
			if !ok || len(parsed.fields) < 2 {
				flush()
				continue
			}
			current = append(current, parsed)
		} else {
			flush()
		}
	}
	flush()
	return groups
}

func hasPrefixRune(s string, runes ...rune) bool {
	for _, r := range runes {
		if strings.HasPrefix(s, string(r)) {
			return true
		}
	}
	return false
}

// parseTableLine splits a ␞ or ␁␞ line into its units on ␟ (text is not
// split; a keyed record splits on its ␁ marks too, so key and value align).
func parseTableLine(index int, text string) (tableLine, bool) {
	chars := []rune(text)
	i := 0
	for i < len(chars) && (chars[i] == ' ' || chars[i] == '\t') {
		i++
	}
	if i >= len(chars) {
		return tableLine{}, false
	}
	switch chars[i] {
	case gSOH:
		i++
		if !(i < len(chars) && chars[i] == gRS) {
			return tableLine{}, false
		}
		i++
	case gRS:
		i++
	default:
		return tableLine{}, false
	}
	prefix := string(chars[:i])
	prefixLen := i
	var fields []string
	var seps []rune
	var field strings.Builder
	inText := false
	keyed := i < len(chars) && chars[i] == gSOH
	for ; i < len(chars); i++ {
		ch := chars[i]
		switch {
		case inText:
			field.WriteRune(ch)
			if ch == gETX {
				inText = false
			}
			if ch == gDLE && i+1 < len(chars) {
				field.WriteRune(chars[i+1])
				i++
			}
		case ch == gSTX:
			inText = true
			field.WriteRune(ch)
		case ch == gDLE || ch == gCAN:
			return tableLine{}, false // nested levels are not aligned
		case ch == gUS:
			fields = append(fields, strings.TrimSpace(field.String()))
			seps = append(seps, gUS)
			field.Reset()
		case keyed && ch == gSOH && i > prefixLen:
			fields = append(fields, strings.TrimSpace(field.String()))
			seps = append(seps, 0)
			field.Reset()
			field.WriteRune(ch)
		default:
			field.WriteRune(ch)
		}
	}
	if inText {
		return tableLine{}, false // text continues on the next line
	}
	fields = append(fields, strings.TrimSpace(field.String()))
	if len(fields) < 2 {
		return tableLine{}, false
	}
	return tableLine{index: index, prefix: prefix, fields: fields, seps: seps}, true
}
