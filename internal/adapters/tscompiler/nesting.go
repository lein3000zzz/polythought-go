package tscompiler

import "fmt"

// What a source may nest before esbuild sees it (R5). esbuild recurses once per level:
// a long enough run of ! overflows the goroutine's stack, which takes the whole server
// down, and nested functions or chained arrows take it seconds to minutes. Real code
// stays far below these.
const (
	maxDepth    = 256  // brackets, braces, parentheses and template ${ inside each other
	maxUnaryRun = 256  // ! ~ + - one after another
	maxArrows   = 1000 // => in code
	maxExprArrs = 100  // => in an expression
)

// tooNested says why a source nests too deeply to compile, or "" when it does not. It
// reads strings, comments and template text as such, so what they hold does not count.
func tooNested(src string, arrows int) string {
	var (
		stack    []byte // the brackets open, '$' for a template's ${
		inText   []bool // per template open: whether in its text, not a ${
		depth    int
		run      int
		arrowsIn int
	)

	inTemplate := func() bool { return len(inText) > 0 && inText[len(inText)-1] }

	for i := 0; i < len(src); i++ {
		c := src[i]

		if inTemplate() {
			switch {
			case c == '\\':
				i++
			case c == '`':
				inText = inText[:len(inText)-1]
			case c == '$' && i+1 < len(src) && src[i+1] == '{':
				i++
				inText[len(inText)-1] = false
				stack = append(stack, '$')

				if depth = max(depth, len(stack)); depth > maxDepth {
					return fmt.Sprintf("the code nests more than %d levels deep", maxDepth)
				}
			}

			continue
		}

		switch {
		case c == '"' || c == '\'':
			for i++; i < len(src) && src[i] != c && src[i] != '\n'; i++ {
				if src[i] == '\\' {
					i++
				}
			}

			run = 0

			continue
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}

			continue
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			for i += 2; i+1 < len(src) && (src[i] != '*' || src[i+1] != '/'); i++ {
			}

			i++

			continue
		case c == '`':
			inText = append(inText, true)
			run = 0

			continue
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			continue
		case c == '!' || c == '~' || c == '+' || c == '-':
			if run++; run > maxUnaryRun {
				return fmt.Sprintf("the code has more than %d of ! ~ + - in a row", maxUnaryRun)
			}

			continue
		}

		run = 0

		switch c {
		case '(', '[', '{':
			stack = append(stack, c)
			depth = max(depth, len(stack))
		case ')', ']', '}':
			if len(stack) == 0 {
				break
			}

			if stack[len(stack)-1] == '$' && len(inText) > 0 {
				inText[len(inText)-1] = true // back in the template's text
			}

			stack = stack[:len(stack)-1]
		case '=':
			if i+1 < len(src) && src[i+1] == '>' {
				if arrowsIn++; arrowsIn > arrows {
					return fmt.Sprintf("the code has more than %d arrow functions", arrows)
				}
			}
		}

		if depth > maxDepth {
			return fmt.Sprintf("the code nests more than %d levels deep", maxDepth)
		}
	}

	return ""
}
