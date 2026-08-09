package publish

import (
	"strings"
	"unicode"
)

// jsObjectToJSON converts a JS object/array literal subset to JSON text.
func jsObjectToJSON(src string) string {
	src = stripLineComments(src)
	var b strings.Builder
	b.Grow(len(src) + 32)
	inStr := false
	escape := false
	quote := byte(0) // original quote char of current string

	for i := 0; i < len(src); i++ {
		c := src[i]
		if inStr {
			if escape {
				if quote == '\'' && c == '\'' {
					b.WriteByte('\'')
				} else if quote == '\'' && c == '"' {
					b.WriteString(`\"`)
				} else {
					b.WriteByte(c)
				}
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				// keep escape for JSON (except we'll rewrite \' )
				if i+1 < len(src) && src[i+1] == '\'' && quote == '\'' {
					// \' inside single-quoted → just '
					continue
				}
				b.WriteByte('\\')
				continue
			}
			if c == quote {
				b.WriteByte('"')
				inStr = false
				continue
			}
			if c == '"' && quote == '\'' {
				b.WriteString(`\"`)
				continue
			}
			b.WriteByte(c)
			continue
		}

		if c == '"' || c == '\'' {
			inStr = true
			quote = c
			b.WriteByte('"')
			continue
		}

		if c == ',' {
			j := i + 1
			for j < len(src) && unicode.IsSpace(rune(src[j])) {
				j++
			}
			if j < len(src) && (src[j] == '}' || src[j] == ']') {
				continue
			}
			b.WriteByte(',')
			continue
		}

		if isIdentStart(c) {
			prev := prevNonSpace(src, i-1)
			if prev == '{' || prev == ',' {
				j := i
				for j < len(src) && isIdentPart(src[j]) {
					j++
				}
				k := j
				for k < len(src) && unicode.IsSpace(rune(src[k])) {
					k++
				}
				if k < len(src) && src[k] == ':' {
					b.WriteByte('"')
					b.WriteString(src[i:j])
					b.WriteByte('"')
					i = j - 1
					continue
				}
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func stripLineComments(s string) string {
	var b strings.Builder
	inStr := false
	escape := false
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			b.WriteByte(c)
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == quote {
				inStr = false
			}
			continue
		}
		if c == '"' || c == '\'' {
			inStr = true
			quote = c
			b.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '/' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				b.WriteByte('\n')
			}
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i++ // consume '/'
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func prevNonSpace(s string, i int) byte {
	for i >= 0 {
		if !unicode.IsSpace(rune(s[i])) {
			return s[i]
		}
		i--
	}
	return 0
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
