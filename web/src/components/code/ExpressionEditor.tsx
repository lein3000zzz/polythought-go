import { cn } from 'cn'
import { useEffect, useEffectEvent, useId, useMemo, useRef, useState } from 'react'

import { useCodeTypes } from '@/api/code'
import { scopeDeclarations } from '@/features/workflow/expressionScope'
import { expressionParts } from '@/features/workflow/template'
import { useTheme } from '@/lib/theme'

import { languageId, watch } from './expressions'
import type { ExpressionInputProps } from './ExpressionInput'
import { monaco, setSdk } from './monaco'

const lineHeight = 20

/** The Monaco editor of ExpressionInput, loaded on first use. */
export default function ExpressionEditor({
  id,
  value,
  onChange,
  label,
  scope,
  placeholder,
  invalid,
  multiline,
  rows = 3,
}: ExpressionInputProps) {
  const host = useRef<HTMLDivElement>(null)
  const editor = useRef<monaco.editor.IStandaloneCodeEditor>(null)
  const watching = useRef<ReturnType<typeof watch>>(null)
  const { theme } = useTheme()
  const types = useCodeTypes().data
  const path = useId()
  const prelude = useMemo(() => scopeDeclarations(scope.item, scope.settings, scope.items), [scope])
  const [initial] = useState(() => ({ value, label, placeholder, theme, prelude, scope }))

  const changed = useEffectEvent((text: string) => {
    if (text !== value) onChange(text)
  })

  useEffect(() => {
    if (types) setSdk(types)
  }, [types])

  useEffect(() => {
    const model = monaco.editor.createModel(
      initial.value,
      languageId,
      monaco.Uri.parse(`file:///settings/${encodeURIComponent(path)}.tpl`),
    )
    const ed = monaco.editor.create(host.current!, {
      model,
      ariaLabel: initial.label,
      placeholder: initial.placeholder,
      theme: initial.theme === 'dark' ? 'vs-dark' : 'vs',
      fontSize: 13,
      lineHeight,
      lineNumbers: 'off',
      glyphMargin: false,
      folding: false,
      lineDecorationsWidth: 0,
      lineNumbersMinChars: 0,
      minimap: { enabled: false },
      overviewRulerLanes: 0,
      overviewRulerBorder: false,
      hideCursorInOverviewRuler: true,
      renderLineHighlight: 'none',
      scrollBeyondLastLine: false,
      scrollbar: {
        vertical: multiline ? 'auto' : 'hidden',
        horizontal: 'hidden',
        alwaysConsumeMouseWheel: false,
      },
      wordWrap: multiline ? 'on' : 'off',
      wordBasedSuggestions: 'off',
      // Suggestions open as below, not by Monaco's own heuristics, which miss words
      // inside the embedded TypeScript.
      quickSuggestions: false,
      suggestOnTriggerCharacters: false,
      contextmenu: false,
      tabFocusMode: true, // Tab moves on, as in any other field
      // A hidden text area takes the typing, as everywhere else on the page: with
      // EditContext, typed text does not count as typed, and suggestions close at once.
      editContext: false,
      padding: { top: 5, bottom: 5 },
      automaticLayout: true,
      fixedOverflowWidgets: true,
    })
    editor.current = ed

    // One line is one line: Enter takes a suggestion or does nothing, and pasted lines join.
    if (!multiline) ed.addCommand(monaco.KeyCode.Enter, () => {}, '!suggestWidgetVisible')

    const edits = model.onDidChangeContent((e) => {
      const text = model.getValue()
      if (!multiline && text.includes('\n')) {
        model.setValue(text.replace(/\r?\n/g, ' '))
        return
      }

      changed(text)

      // A letter or a dot typed inside {{ }} opens what may come there.
      const typed = e.changes.length === 1 ? e.changes[0].text : ''
      const at = ed.getPosition()
      const offset = at && model.getOffsetAt(at)
      if (
        /^[\w$.]$/.test(typed) &&
        offset !== null &&
        expressionParts(text).some((p) => offset >= p.from && offset <= p.to)
      ) {
        // Once the cursor has moved past what was typed.
        setTimeout(() => ed.trigger('expressions', 'editor.action.triggerSuggest', {}))
      }
    })

    watching.current = watch(model, initial.prelude, initial.scope.item !== undefined)

    return () => {
      watching.current?.stop()
      watching.current = null
      edits.dispose()
      ed.dispose()
      model.dispose()
      editor.current = null
    }
  }, [initial, path, multiline])

  useEffect(() => {
    watching.current?.sees(prelude, scope.item !== undefined)
  }, [prelude, scope.item])

  useEffect(() => {
    const model = editor.current?.getModel()
    if (model && model.getValue() !== value) {
      model.pushEditOperations([], [{ range: model.getFullModelRange(), text: value }], () => null)
    }
  }, [value])

  useEffect(() => monaco.editor.setTheme(theme === 'dark' ? 'vs-dark' : 'vs'), [theme])

  return (
    <div
      ref={host}
      id={id}
      style={{ height: (multiline ? rows : 1) * lineHeight + 12 }}
      className={cn(
        // nokey: the canvas leaves the keys typed here alone, Space and Backspace among them.
        'nokey w-full min-w-0 overflow-hidden rounded-lg border border-input px-1.5 focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50',
        invalid && 'border-destructive',
      )}
    />
  )
}
