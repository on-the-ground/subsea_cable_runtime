// Package syntax implements the POC frontend: strict UTF-8 decoding, the
// lexical preprocessing pass from conformance/README.md, and a hand-written
// recursive-descent parser that follows SubseaCable.g4.
package syntax

import (
	"fmt"
	"unicode/utf8"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
)

// TokKind is a lexical token class.
type TokKind int

const (
	EOF TokKind = iota
	Terminator
	Ident
	HashQual
	Number
	String
	Bool
	Wildcard
	Punct // operators and delimiters; see Token.Text
)

func (k TokKind) String() string {
	switch k {
	case EOF:
		return "EOF"
	case Terminator:
		return "TERMINATOR"
	case Ident:
		return "IDENTIFIER"
	case HashQual:
		return "HASH_QUALIFIER"
	case Number:
		return "NUMBER"
	case String:
		return "STRING"
	case Bool:
		return "BOOLEAN"
	case Wildcard:
		return "_"
	default:
		return "PUNCT"
	}
}

// Token is one significant token.
type Token struct {
	Kind TokKind
	Text string
	Span diag.Span
}

func (t Token) String() string {
	if t.Kind == Punct {
		return fmt.Sprintf("%q", t.Text)
	}
	if t.Kind == EOF || t.Kind == Terminator {
		return t.Kind.String()
	}
	return fmt.Sprintf("%s %q", t.Kind, t.Text)
}

// Is reports whether t is the punctuation p.
func (t Token) Is(p string) bool { return t.Kind == Punct && t.Text == p }

var puncts = []string{
	"->", "||", "&&", "==", "!=", "<=", ">=",
	"=", ",", ":", ";", "@", "$", "[", "]", "{", "}", "(", ")",
	"<", ">", "+", "-", "*", "/", "%", "!",
}

// rawToken includes hidden-channel tokens needed by preprocessing.
type rawToken struct {
	Token
	hidden     bool // whitespace or comment
	terminator bool
}

// lex tokenizes src. Source must already be valid UTF-8.
func lex(src string) ([]rawToken, error) {
	var out []rawToken
	line, col := 1, 1
	i := 0
	adv := func(n int) {
		for k := 0; k < n; k++ {
			if src[i] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
			i++
		}
	}
	for i < len(src) {
		start := diag.Span{Line: line, Col: col}
		c := src[i]
		switch {
		case c == '\n' || (c == '\r' && i+1 < len(src) && src[i+1] == '\n'):
			j := i
			for j < len(src) {
				if src[j] == '\n' {
					j++
				} else if src[j] == '\r' && j+1 < len(src) && src[j+1] == '\n' {
					j += 2
				} else {
					break
				}
			}
			adv(j - i)
			out = append(out, rawToken{Token: Token{Kind: Terminator, Span: start}, terminator: true})
		case c == ' ' || c == '\t':
			j := i
			for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
				j++
			}
			adv(j - i)
			out = append(out, rawToken{hidden: true})
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			j := i
			for j < len(src) && src[j] != '\n' && src[j] != '\r' {
				j++
			}
			adv(j - i)
			out = append(out, rawToken{hidden: true})
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			j := i + 2
			for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
				j++
			}
			if j+1 >= len(src) {
				return nil, diag.New("SyntaxError", diag.Source, diag.At(start), "unterminated block comment")
			}
			adv(j + 2 - i)
			out = append(out, rawToken{hidden: true})
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentRest(src[j]) {
				j++
			}
			text := src[i:j]
			kind := Ident
			switch text {
			case "_":
				kind = Wildcard
			case "true", "false":
				kind = Bool
			}
			adv(j - i)
			out = append(out, rawToken{Token: Token{Kind: kind, Text: text, Span: start}})
		case c == '#':
			j := i + 1
			for j < len(src) && isAlnum(src[j]) {
				j++
			}
			if j == i+1 {
				return nil, diag.New("SyntaxError", diag.Source, diag.At(start), "empty hash qualifier")
			}
			text := src[i+1 : j]
			adv(j - i)
			out = append(out, rawToken{Token: Token{Kind: HashQual, Text: text, Span: start}})
		case c >= '0' && c <= '9':
			j := i
			if c == '0' {
				j++
			} else {
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			if j+1 < len(src) && src[j] == '.' && src[j+1] >= '0' && src[j+1] <= '9' {
				j++
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			text := src[i:j]
			adv(j - i)
			out = append(out, rawToken{Token: Token{Kind: Number, Text: text, Span: start}})
		case c == '"':
			j := i + 1
			for {
				if j >= len(src) || src[j] == '\n' || src[j] == '\r' {
					return nil, diag.New("SyntaxError", diag.Source, diag.At(start), "unterminated string literal")
				}
				if src[j] == '"' {
					j++
					break
				}
				if src[j] == '\\' {
					if j+1 >= len(src) {
						return nil, diag.New("SyntaxError", diag.Source, diag.At(start), "unterminated escape")
					}
					switch src[j+1] {
					case '"', '\\', 'n', 'r', 't':
						j += 2
					case 'u':
						k := j + 2
						if k >= len(src) || src[k] != '{' {
							return nil, diag.New("SyntaxError", diag.Source, diag.At(start), "malformed unicode escape")
						}
						k++
						n := 0
						for k < len(src) && isHex(src[k]) {
							k++
							n++
						}
						if n < 1 || n > 6 || k >= len(src) || src[k] != '}' {
							return nil, diag.New("SyntaxError", diag.Source, diag.At(start), "malformed unicode escape")
						}
						j = k + 1
					default:
						return nil, diag.New("SyntaxError", diag.Source, diag.At(start), "unsupported escape \\%c", src[j+1])
					}
					continue
				}
				_, size := utf8.DecodeRuneInString(src[j:])
				j += size
			}
			text := src[i:j]
			// adv counts bytes as columns; acceptable for POC spans.
			adv(j - i)
			out = append(out, rawToken{Token: Token{Kind: String, Text: text, Span: start}})
		default:
			matched := ""
			for _, p := range puncts {
				if len(src)-i >= len(p) && src[i:i+len(p)] == p {
					matched = p
					break
				}
			}
			if matched == "" {
				r, _ := utf8.DecodeRuneInString(src[i:])
				return nil, diag.New("SyntaxError", diag.Source, diag.At(start), "unexpected character %q", r)
			}
			adv(len(matched))
			out = append(out, rawToken{Token: Token{Kind: Punct, Text: matched, Span: start}})
		}
	}
	return out, nil
}

func isIdentStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdentRest(c byte) bool  { return isIdentStart(c) || (c >= '0' && c <= '9') }
func isAlnum(c byte) bool      { return isIdentRest(c) && c != '_' }
func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

var continuation = map[string]bool{
	"=": true, "->": true, ",": true, ":": true, "@": true, "$": true,
	"||": true, "&&": true, "==": true, "!=": true, "<=": true, ">=": true, "<": true, ">": true,
	"+": true, "-": true, "*": true, "/": true, "%": true, "!": true,
}

// Tokenize decodes and preprocesses src into the parser's token stream.
func Tokenize(src []byte) ([]Token, error) {
	if !utf8.Valid(src) {
		return nil, diag.New("InvalidSourceEncoding", diag.Source, nil, "source is not valid UTF-8")
	}
	raw, err := lex(string(src))
	if err != nil {
		return nil, err
	}
	var out []Token
	depth := 0
	var prev *Token
	pendingTerm := false
	var termSpan diag.Span
	for _, rt := range raw {
		if rt.hidden {
			continue
		}
		if rt.terminator {
			if depth > 0 || (prev != nil && prev.Kind == Punct && continuation[prev.Text]) {
				continue
			}
			if !pendingTerm {
				pendingTerm = true
				termSpan = rt.Span
			}
			continue
		}
		if pendingTerm {
			out = append(out, Token{Kind: Terminator, Span: termSpan})
			pendingTerm = false
		}
		t := rt.Token
		if t.Kind == Punct {
			switch t.Text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth > 0 {
					depth--
				}
			}
		}
		out = append(out, t)
		prev = &out[len(out)-1]
	}
	if pendingTerm {
		out = append(out, Token{Kind: Terminator, Span: termSpan})
	}
	end := diag.Span{Line: 1, Col: 1}
	if len(out) > 0 {
		end = out[len(out)-1].Span
	}
	out = append(out, Token{Kind: EOF, Span: end})
	return out, nil
}
