package c0

// TokenType is the kind of a token emitted by the Tokenizer.
type TokenType int

const (
	TokenData TokenType = iota // data content between control codes
	TokenSOH
	TokenSTX
	TokenETX
	TokenEOT
	TokenENQ
	TokenETB
	TokenSUB
	TokenFS
	TokenGS
	TokenRS
	TokenUS
)

// Token is a span of the source buffer.
type Token struct {
	Type TokenType
	// Start and End are byte offsets into the buffer; End is exclusive.
	Start, End int
}

// Value returns the token's bytes as a sub-slice of buf. Zero-copy.
func (t Token) Value(buf []byte) []byte { return buf[t.Start:t.End] }

func controlToken(b byte) (TokenType, bool) {
	switch b {
	case SOH:
		return TokenSOH, true
	case STX:
		return TokenSTX, true
	case ETX:
		return TokenETX, true
	case EOT:
		return TokenEOT, true
	case ENQ:
		return TokenENQ, true
	case ETB:
		return TokenETB, true
	case SUB:
		return TokenSUB, true
	case FS:
		return TokenFS, true
	case GS:
		return TokenGS, true
	case RS:
		return TokenRS, true
	case US:
		return TokenUS, true
	}
	return TokenData, false
}

// Tokenizer scans a buffer for control codes, yielding tokens as offsets.
type Tokenizer struct {
	buf []byte
	pos int
}

// NewTokenizer returns a Tokenizer over buf.
func NewTokenizer(buf []byte) *Tokenizer { return &Tokenizer{buf: buf} }

// Next returns the next token. ok is false at end of input. A non-nil err
// (unassigned code or dangling DLE) ends iteration.
func (tz *Tokenizer) Next() (tok Token, ok bool, err error) {
	if tz.pos >= len(tz.buf) {
		return Token{}, false, nil
	}
	b := tz.buf[tz.pos]

	if b < 0x20 {
		if b == DLE {
			tz.pos++
			if tz.pos >= len(tz.buf) {
				return Token{}, false, ErrUnexpectedEnd
			}
			tok = Token{TokenData, tz.pos, tz.pos + 1}
			tz.pos++
			return tok, true, nil
		}
		ct, assigned := controlToken(b)
		if !assigned {
			e := &UnassignedCodeError{Byte: b, Pos: tz.pos}
			tz.pos = len(tz.buf)
			return Token{}, false, e
		}
		tok = Token{ct, tz.pos, tz.pos + 1}
		tz.pos++
		return tok, true, nil
	}

	start := tz.pos
	tz.pos++
	for tz.pos < len(tz.buf) && tz.buf[tz.pos] >= 0x20 {
		tz.pos++
	}
	return Token{TokenData, start, tz.pos}, true, nil
}

// Tokenize returns all tokens, or the first error if the buffer is malformed.
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
