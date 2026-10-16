// Expressions in Monaco (D67, D68): settings are a language of their own, text with
// TypeScript between {{ }}. Each field has a hidden TypeScript file, its expressions after
// declarations of what they see; completion and errors come from the language service on
// that file, moved to where the expressions are in the field.
import {
  getTypeScriptWorker,
  type TypeScriptWorker,
} from 'monaco-editor/languages/features/typescript/register'

import { shadowOf, type Shadow } from '@/features/workflow/template'

import { monaco } from './monaco'

export const languageId = 'polythought-template'

type Field = {
  model: monaco.editor.ITextModel
  shadow: monaco.editor.ITextModel
  prelude: string
  parts: Shadow['parts']
  knownItem: boolean // with the item's fields unknown, a name it lacks is no error
}

const fields = new Map<string, Field>()

monaco.languages.register({ id: languageId })

monaco.languages.setMonarchTokensProvider(languageId, {
  tokenizer: {
    root: [
      [/\{\{/, { token: 'delimiter.bracket', next: '@expression', nextEmbedded: 'typescript' }],
      [/[^{]+|\{/, ''],
    ],
    expression: [
      [/\}\}/, { token: 'delimiter.bracket', next: '@pop', nextEmbedded: '@pop' }],
      [/[^}]+|\}/, ''],
    ],
  },
})

/** Where an offset of the field is in its hidden file, if it is inside an expression. */
function toShadow(f: Field, offset: number): number | undefined {
  const part = f.parts.find((p) => offset >= p.from && offset <= p.to)
  return part && part.at + (offset - part.from)
}

// The language service for f. Monaco sets its TypeScript up just after the first
// TypeScript model appears; until then it says it is not registered, briefly.
async function worker(f: Field) {
  for (let tries = 0; ; tries++) {
    try {
      const get = await getTypeScriptWorker()
      return await get(f.shadow.uri)
    } catch (error) {
      if (tries >= 50 || !String(error).includes('not registered')) throw error
      await new Promise((resolve) => setTimeout(resolve, 100))
    }
  }
}

const kinds: Record<string, monaco.languages.CompletionItemKind> = {
  const: monaco.languages.CompletionItemKind.Constant,
  let: monaco.languages.CompletionItemKind.Variable,
  var: monaco.languages.CompletionItemKind.Variable,
  property: monaco.languages.CompletionItemKind.Field,
  method: monaco.languages.CompletionItemKind.Method,
  function: monaco.languages.CompletionItemKind.Function,
  alias: monaco.languages.CompletionItemKind.Function,
  keyword: monaco.languages.CompletionItemKind.Keyword,
  interface: monaco.languages.CompletionItemKind.Interface,
  type: monaco.languages.CompletionItemKind.TypeParameter,
}

type Entry = { name: string; kind: string; sortText: string }
type Details = {
  displayParts?: { text: string }[]
  documentation?: { text: string }[]
}

const text = (parts?: { text: string }[]) => parts?.map((p) => p.text).join('') ?? ''

// Where the last completion was asked, for the details of its suggestions.
let asked: { f: Field; at: number } | undefined

monaco.languages.registerCompletionItemProvider(languageId, {
  // The editor asks as letters and dots are typed inside {{ }} (ExpressionEditor), and
  // Ctrl+Space asks anywhere.
  async provideCompletionItems(model, position) {
    const f = fields.get(model.uri.toString())
    const at = f && toShadow(f, model.getOffsetAt(position))
    if (!f || at === undefined) return { suggestions: [] }

    asked = { f, at }

    // Where the word is now, at the request: the answer may come after more is typed.
    const word = model.getWordUntilPosition(position)
    const range = new monaco.Range(
      position.lineNumber,
      word.startColumn,
      position.lineNumber,
      word.endColumn,
    )

    const ts = await worker(f)
    const info = (await ts.getCompletionsAtPosition(f.shadow.uri.toString(), at)) as
      { entries: Entry[] } | undefined

    return {
      // Asked again as the word grows: the language service knows best, and the first
      // answer can come before it is ready.
      incomplete: true,
      suggestions: (info?.entries ?? []).map((e) => ({
        label: e.name,
        kind: kinds[e.kind] ?? monaco.languages.CompletionItemKind.Text,
        insertText: e.name,
        sortText: e.sortText,
        range,
      })),
    }
  },
  async resolveCompletionItem(item) {
    if (!asked || asked.f.shadow.isDisposed()) return item

    const ts = await worker(asked.f)
    const label = typeof item.label === 'string' ? item.label : item.label.label
    const details = (await ts.getCompletionEntryDetails(
      asked.f.shadow.uri.toString(),
      asked.at,
      label,
    )) as Details | undefined

    return {
      ...item,
      detail: text(details?.displayParts),
      documentation: text(details?.documentation),
    }
  },
})

monaco.languages.registerHoverProvider(languageId, {
  async provideHover(model, position) {
    const f = fields.get(model.uri.toString())
    const at = f && toShadow(f, model.getOffsetAt(position))
    if (!f || at === undefined) return null

    const ts = await worker(f)
    const info = (await ts.getQuickInfoAtPosition(f.shadow.uri.toString(), at)) as
      Details | undefined
    if (!info) return null

    return {
      contents: [
        { value: '```ts\n' + text(info.displayParts) + '\n```' },
        { value: text(info.documentation) },
      ],
    }
  },
})

// TypeScript's "Cannot find name": a field the items may have when nobody knows them.
const cannotFindName = 2304

/**
 * Checks the field's closed expressions and underlines what TypeScript finds wrong in
 * them, syntax and types alike (D68). Shown, not blocking: saving checks on the server.
 */
async function check(f: Field) {
  let found: Awaited<ReturnType<TypeScriptWorker['getSemanticDiagnostics']>>

  try {
    const ts = await worker(f)
    const uri = f.shadow.uri.toString()
    found = [...(await ts.getSyntacticDiagnostics(uri)), ...(await ts.getSemanticDiagnostics(uri))]
  } catch {
    // The field went, or the language service is not there yet: nothing to show for now.
    return
  }

  if (f.shadow.isDisposed()) return

  const markers: monaco.editor.IMarkerData[] = []

  for (const d of found) {
    if (d.start === undefined || (!f.knownItem && d.code === cannotFindName)) continue

    const part = f.parts.find(
      (p) => p.closed && d.start! >= p.at && d.start! <= p.at + (p.to - p.from),
    )
    if (!part) continue

    const from = part.from + (d.start - part.at)
    const to = Math.min(part.to, from + Math.max(d.length ?? 1, 1))
    const start = f.model.getPositionAt(from)
    const end = f.model.getPositionAt(to)
    const message = typeof d.messageText === 'string' ? d.messageText : d.messageText.messageText

    markers.push({
      severity: monaco.MarkerSeverity.Error,
      message,
      startLineNumber: start.lineNumber,
      startColumn: start.column,
      endLineNumber: end.lineNumber,
      endColumn: end.column,
    })
  }

  if (!f.model.isDisposed()) monaco.editor.setModelMarkers(f.model, 'expressions', markers)
}

/**
 * Starts completing and checking the expressions of model, which sees what prelude
 * declares. The returned functions update what it sees and stop it.
 */
export function watch(model: monaco.editor.ITextModel, prelude: string, knownItem: boolean) {
  const shadow = monaco.editor.createModel(
    '',
    'typescript',
    monaco.Uri.parse(model.uri.toString().replace(/\.[^.]*$/, '') + '.expr.ts'),
  )
  const f: Field = { model, shadow, prelude, parts: [], knownItem }
  fields.set(model.uri.toString(), f)

  let pending: ReturnType<typeof setTimeout> | undefined

  const sync = () => {
    const next = shadowOf(model.getValue(), f.prelude)
    f.parts = next.parts
    if (shadow.getValue() !== next.source) shadow.setValue(next.source)

    clearTimeout(pending)
    pending = setTimeout(() => void check(f), 250)
  }

  sync()
  void check(f) // which starts the language service, so the first completion is quick
  const edits = model.onDidChangeContent(sync)

  return {
    sees(nextPrelude: string, nextKnown: boolean) {
      f.prelude = nextPrelude
      f.knownItem = nextKnown
      sync()
    },
    stop() {
      clearTimeout(pending)
      edits.dispose()
      fields.delete(model.uri.toString())
      shadow.dispose()
    },
  }
}
