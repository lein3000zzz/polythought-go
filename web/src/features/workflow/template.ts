/**
 * A {{ }} in a setting's text, as the server reads them (engine.parseTemplate): from and
 * to are where the expression is, between the braces. An expression ends at the first }}
 * after which its own braces balance, so an object literal works inside.
 */
export type ExpressionPart = {
  from: number
  to: number
  closed: boolean // false while being typed: the server reads a {{ with no end as text
}

export function expressionParts(text: string): ExpressionPart[] {
  const parts: ExpressionPart[] = []
  let at = 0

  for (;;) {
    const start = text.indexOf('{{', at)
    if (start < 0) return parts

    const from = start + 2
    const end = closing(text, from)

    if (end < 0) {
      parts.push({ from, to: text.length, closed: false })
      return parts
    }

    parts.push({ from, to: end, closed: true })
    at = end + 2
  }
}

function closing(text: string, from: number): number {
  let depth = 0

  for (let i = from; i < text.length; i++) {
    if (text[i] === '{') {
      depth++
    } else if (text[i] === '}') {
      if (depth === 0 && text[i + 1] === '}') return i
      if (depth > 0) depth--
    }
  }

  return -1
}

/** A TypeScript file with each expression of text after prelude, and where each went. */
export type Shadow = {
  source: string
  parts: (ExpressionPart & { at: number })[] // at: where the expression starts in source
}

/**
 * The expressions of a setting as one TypeScript file, after the declarations of what
 * they see, so the language service can complete and check them; text around them is
 * left out. An empty {{}} is text, as the server reads it.
 */
export function shadowOf(text: string, prelude: string): Shadow {
  let source = prelude
  const parts: Shadow['parts'] = []

  for (const p of expressionParts(text)) {
    if (p.closed && text.slice(p.from, p.to).trim() === '') continue

    source += ';('
    parts.push({ ...p, at: source.length })
    source += text.slice(p.from, p.to) + ')\n'
  }

  return { source, parts }
}
