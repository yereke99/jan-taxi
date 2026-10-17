package pdfdoc

import (
	"errors"
	"io"
	"strconv"
)

// Lexer limits. They bound memory and recursion on hostile input; real
// documents stay far below them.
const (
	maxNesting    = 48      // arrays/dictionaries inside each other
	maxContainer  = 200_000 // elements of one array or dictionary
	maxNameLen    = 512
	maxNumberLen  = 64
	maxKeywordLen = 64
)

var errTooDeep = errors.New("pdfdoc: nesting too deep")

// lexer reads PDF tokens and objects from b starting at pos.
type lexer struct {
	b   []byte
	pos int
}

func isWhite(c byte) bool {
	return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegular(c byte) bool { return !isWhite(c) && !isDelim(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// skipSpace skips white space and comments.
func (l *lexer) skipSpace() {
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		switch {
		case isWhite(c):
			l.pos++
		case c == '%':
			for l.pos < len(l.b) && l.b[l.pos] != '\n' && l.b[l.pos] != '\r' {
				l.pos++
			}
		default:
			return
		}
	}
}

// next returns the next object or delimiter token. allowRef enables the
// "num gen R" lookahead (never in content streams). io.EOF means no token.
func (l *lexer) next(depth int, allowRef bool) (Object, error) {
	if depth > maxNesting {
		return nil, errTooDeep
	}
	l.skipSpace()
	if l.pos >= len(l.b) {
		return nil, io.EOF
	}
	c := l.b[l.pos]
	switch {
	case c == '/':
		return l.name(), nil
	case c == '(':
		return l.literal(), nil
	case c == '<':
		if l.pos+1 < len(l.b) && l.b[l.pos+1] == '<' {
			l.pos += 2
			return l.dict(depth, allowRef)
		}
		return l.hex(), nil
	case c == '>':
		if l.pos+1 < len(l.b) && l.b[l.pos+1] == '>' {
			l.pos += 2
			return endDict{}, nil
		}
		l.pos++
		return keyword(">"), nil
	case c == '[':
		l.pos++
		return l.array(depth, allowRef)
	case c == ']':
		l.pos++
		return endArray{}, nil
	case c == '{' || c == '}' || c == ')':
		l.pos++
		return keyword(string(c)), nil
	case c == '+' || c == '-' || c == '.' || isDigit(c):
		return l.number(allowRef), nil
	}
	return l.word(), nil
}

// word reads a bare keyword; true, false and null become their values.
func (l *lexer) word() Object {
	start := l.pos
	for l.pos < len(l.b) && isRegular(l.b[l.pos]) {
		l.pos++
	}
	if l.pos == start { // cannot happen for a regular byte; keep progress anyway
		l.pos++
	}
	w := l.b[start:l.pos]
	if len(w) > maxKeywordLen {
		w = w[:maxKeywordLen]
	}
	switch string(w) {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	return keyword(w)
}

// number reads an integer or a real; with allowRef an integer followed by
// "gen R" becomes a Ref.
func (l *lexer) number(allowRef bool) Object {
	start := l.pos
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		if !(isDigit(c) || c == '.' || c == '+' || c == '-') {
			break
		}
		l.pos++
	}
	tok := l.b[start:l.pos]
	if len(tok) > maxNumberLen {
		tok = tok[:maxNumberLen]
	}
	isReal := false
	for _, c := range tok {
		if c == '.' {
			isReal = true
		}
	}
	if !isReal {
		n, err := strconv.ParseInt(string(tok), 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(string(tok), 64)
			if ferr != nil {
				return int64(0)
			}
			return f
		}
		if allowRef && n >= 0 && n <= 1<<30 {
			if ref, ok := l.refTail(int(n)); ok {
				return ref
			}
		}
		return n
	}
	f, err := strconv.ParseFloat(string(tok), 64)
	if err != nil {
		return float64(0)
	}
	return f
}

// refTail checks for "gen R" after an object number; pos is unchanged when
// it does not match.
func (l *lexer) refTail(num int) (Ref, bool) {
	save := l.pos
	l.skipSpace()
	gStart := l.pos
	for l.pos < len(l.b) && isDigit(l.b[l.pos]) && l.pos-gStart < 10 {
		l.pos++
	}
	if l.pos == gStart || (l.pos < len(l.b) && isRegular(l.b[l.pos])) {
		l.pos = save
		return Ref{}, false
	}
	gen, _ := strconv.Atoi(string(l.b[gStart:l.pos]))
	l.skipSpace()
	if l.pos < len(l.b) && l.b[l.pos] == 'R' && (l.pos+1 == len(l.b) || !isRegular(l.b[l.pos+1])) {
		l.pos++
		return Ref{Num: num, Gen: gen}, true
	}
	l.pos = save
	return Ref{}, false
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// name reads /Name, resolving #xx escapes.
func (l *lexer) name() Name {
	l.pos++ // '/'
	var out []byte
	for l.pos < len(l.b) && isRegular(l.b[l.pos]) {
		c := l.b[l.pos]
		if c == '#' && l.pos+2 < len(l.b) {
			hi, ok1 := unhex(l.b[l.pos+1])
			lo, ok2 := unhex(l.b[l.pos+2])
			if ok1 && ok2 {
				c = hi<<4 | lo
				l.pos += 2
			}
		}
		if len(out) < maxNameLen {
			out = append(out, c)
		}
		l.pos++
	}
	return Name(out)
}

// literal reads a (string) with nested parentheses and escapes.
func (l *lexer) literal() String {
	l.pos++ // '('
	var out []byte
	depth := 1
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		l.pos++
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return out
			}
		case '\\':
			if l.pos >= len(l.b) {
				return out
			}
			e := l.b[l.pos]
			l.pos++
			switch e {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case '\r':
				if l.pos < len(l.b) && l.b[l.pos] == '\n' {
					l.pos++
				}
				continue // line continuation
			case '\n':
				continue
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for i := 0; i < 2 && l.pos < len(l.b) && l.b[l.pos] >= '0' && l.b[l.pos] <= '7'; i++ {
						v = v*8 + int(l.b[l.pos]-'0')
						l.pos++
					}
					c = byte(v)
				} else {
					c = e // \( \) \\ and unknown escapes keep the character
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// hex reads a <hex string>; an odd final digit is padded with 0.
func (l *lexer) hex() String {
	l.pos++ // '<'
	var out []byte
	var hi byte
	half := false
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		l.pos++
		if c == '>' {
			break
		}
		v, ok := unhex(c)
		if !ok {
			continue
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return out
}

func (l *lexer) array(depth int, allowRef bool) (Object, error) {
	var out Array
	for {
		o, err := l.next(depth+1, allowRef)
		if err != nil {
			if err == io.EOF {
				return out, ErrMalformed
			}
			return out, err
		}
		switch o.(type) {
		case endArray:
			return out, nil
		case endDict:
			return out, nil // malformed: an array closed by ">>"
		}
		if len(out) >= maxContainer {
			return out, ErrTooLarge
		}
		out = append(out, o)
	}
}

func (l *lexer) dict(depth int, allowRef bool) (Object, error) {
	out := Dict{}
	for {
		k, err := l.next(depth+1, allowRef)
		if err != nil {
			if err == io.EOF {
				return out, ErrMalformed
			}
			return out, err
		}
		switch k.(type) {
		case endDict:
			return out, nil
		case endArray:
			continue
		}
		key, ok := k.(Name)
		if !ok {
			continue // malformed: skip non-name keys
		}
		v, err := l.next(depth+1, allowRef)
		if err != nil {
			if err == io.EOF {
				return out, ErrMalformed
			}
			return out, err
		}
		if _, end := v.(endDict); end {
			out[key] = nil
			return out, nil
		}
		if _, end := v.(endArray); end {
			continue
		}
		if len(out) >= maxContainer {
			return out, ErrTooLarge
		}
		out[key] = v
	}
}

// hasKeywordAt reports whether the bytes at pos (after white space) are the
// keyword kw followed by a delimiter, white space or the end.
func (l *lexer) hasKeywordAt(kw string) bool {
	save := l.pos
	l.skipSpace()
	ok := len(l.b)-l.pos >= len(kw) && string(l.b[l.pos:l.pos+len(kw)]) == kw &&
		(l.pos+len(kw) == len(l.b) || !isRegular(l.b[l.pos+len(kw)]))
	if ok {
		l.pos += len(kw)
	} else {
		l.pos = save
	}
	return ok
}
