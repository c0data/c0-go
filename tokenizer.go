package c0

// TokenType is the kind of a token emitted by the Tokenizer.
type TokenType int

// Token kinds: a plain run, a layout run, a whole text, and one per code.
const (
	TokenData   TokenType = iota // a plain run of bytes >= 0x20 (may include interior spaces)
	TokenLayout                  // a run of layout bytes (SP HT LF CR) outside text
	TokenText                    // a whole text, STX through ETX
	TokenSOH                     // key or header-row introducer
	TokenEOT                     // end of document
	TokenENQ                     // reference
	TokenBEL                     // comment introducer
	TokenDLE                     // open a nested level
	TokenSYN                     // start of document
	TokenETB                     // commit marker
	TokenCAN                     // close a nested level
	TokenEM                      // end of medium
	TokenSUB                     // substitution
	TokenFS                      // file separator
	TokenGS                      // group separator
	TokenRS                      // record separator
	TokenUS                      // unit separator
)

var codeTokens = [32]TokenType{
	SOH: TokenSOH, EOT: TokenEOT, ENQ: TokenENQ, BEL: TokenBEL, DLE: TokenDLE,
	SYN: TokenSYN, ETB: TokenETB, CAN: TokenCAN, SUB: TokenSUB,
	FS: TokenFS, GS: TokenGS, RS: TokenRS, US: TokenUS,
}

// Token is a span of the source buffer.
type Token struct {
	Type TokenType
	// Start and End are byte offsets into the buffer; End is exclusive.
	Start, End int
}

// Raw returns the token's bytes as a sub-slice of buf. Zero-copy.
func (t Token) Raw(buf []byte) []byte { return buf[t.Start:t.End] }

// Text returns, for a TokenText, the inside with escapes decoded.
func (t Token) Text(buf []byte) []byte { return DecodeText(buf[t.Start+1 : t.End-1]) }

// Tokenizer scans a byte buffer and emits tokens as offsets into it: plain
// runs, layout runs, whole texts, and one token per code. It does not check
// structure (see Validate); it stops at EM and reports unassigned codes and
// malformed text.
type Tokenizer struct {
	buf       []byte
	pos       int
	tailStart int
}

// NewTokenizer returns a Tokenizer over buf.
func NewTokenizer(buf []byte) *Tokenizer { return &Tokenizer{buf: buf, tailStart: -1} }

// TailStart returns the position of the first byte after EM, the buffer
// size when there is no EM, or -1 while scanning is incomplete.
func (tz *Tokenizer) TailStart() int { return tz.tailStart }

// Next returns the next token. ok is false at the end of input (or after
// EM). A non-nil err (unassigned code or malformed text) ends iteration.
func (tz *Tokenizer) Next() (tok Token, ok bool, err error) {
	defer catchScan(&err)
	buf := tz.buf
	n := len(buf)
	if tz.tailStart >= 0 || tz.pos >= n {
		if tz.tailStart < 0 {
			tz.tailStart = n
		}
		return Token{}, false, nil
	}
	b := buf[tz.pos]
	if b >= 0x20 {
		if b == SP {
			return tz.layout(), true, nil
		}
		// a plain run, ending before trailing spaces so that layout next
		// to a code is its own token
		start := tz.pos
		tz.pos = scanPlain(buf, tz.pos+1, n)
		stop := tz.pos
		for stop > start+1 && buf[stop-1] == SP {
			stop--
		}
		tz.pos = stop
		return Token{TokenData, start, stop}, true, nil
	}
	switch b {
	case STX:
		stop := scanText(buf, tz.pos, n)
		tok = Token{TokenText, tz.pos, stop}
		tz.pos = stop
		return tok, true, nil
	case HT, LF, CR:
		return tz.layout(), true, nil
	case EM:
		tok = Token{TokenEM, tz.pos, tz.pos + 1}
		tz.pos++
		tz.tailStart = tz.pos
		return tok, true, nil
	}
	if !assigned[b] || b == ETX {
		if b == ETX {
			fail("ETX with no open text", tz.pos)
		}
		failUnassigned(b, tz.pos)
	}
	tok = Token{codeTokens[b], tz.pos, tz.pos + 1}
	tz.pos++
	return tok, true, nil
}

func (tz *Tokenizer) layout() Token {
	start := tz.pos
	tz.pos = scanLayout(tz.buf, tz.pos, len(tz.buf))
	return Token{TokenLayout, start, tz.pos}
}

// Tokenize returns all tokens, or the first error if the buffer is
// malformed at the token level.
func Tokenize(buf []byte) ([]Token, error) {
	tz := NewTokenizer(buf)
	var toks []Token
	for {
		t, ok, err := tz.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return toks, nil
		}
		toks = append(toks, t)
	}
}
