/**
 * Plain text with a little structure: blank lines separate paragraphs, and a paragraph
 * whose lines start with "- " is a list. Line breaks inside a paragraph are kept.
 */
export function Prose({ text }: { text: string }) {
  const blocks = text
    .split(/\n\s*\n/)
    .map((b) => b.trim())
    .filter(Boolean)

  return (
    <div className="flex flex-col gap-3 text-sm leading-relaxed">
      {blocks.map((block, i) => {
        const lines = block.split('\n')
        const items = lines.filter((l) => l.startsWith('- '))

        // A list may follow a lead-in line, like "To get a webhook URL:".
        if (items.length > 0 && items.length >= lines.length - 1) {
          const lead = items.length < lines.length ? lines[0] : undefined

          return (
            <div key={i} className="flex flex-col gap-1">
              {lead && <p>{lead}</p>}
              <ul className="list-disc space-y-1 pl-5">
                {items.map((l, j) => (
                  <li key={j}>{l.slice(2)}</li>
                ))}
              </ul>
            </div>
          )
        }

        return (
          <p key={i} className="whitespace-pre-line">
            {block}
          </p>
        )
      })}
    </div>
  )
}
