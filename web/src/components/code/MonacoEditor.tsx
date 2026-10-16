import { cn } from 'cn'
import { useEffect, useEffectEvent, useId, useRef, useState } from 'react'

import { useCodeTypes } from '@/api/code'
import { useTheme } from '@/lib/theme'

import type { CodeEditorProps } from './CodeEditor'
import { monaco, setSdk } from './monaco'

const lineHeight = 18

type TypeError = { line: number; column: number; message: string }

/** The Monaco editor of CodeEditor, loaded on first use. */
export default function MonacoEditor({
  id,
  value,
  onChange,
  label,
  rows,
  readOnly,
  invalid,
}: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const editor = useRef<monaco.editor.IStandaloneCodeEditor>(null)
  const { theme } = useTheme()
  const types = useCodeTypes().data
  const path = useId()
  const [errors, setErrors] = useState<TypeError[]>([])
  // What the editor starts with; later changes reach it through the effects below.
  const [initial] = useState(() => ({ value, label, readOnly, theme }))

  const changed = useEffectEvent((text: string) => {
    if (text !== value) onChange(text)
  })

  useEffect(() => {
    if (types) setSdk(types)
  }, [types])

  useEffect(() => {
    const model = monaco.editor.createModel(
      initial.value,
      'typescript',
      monaco.Uri.parse(`file:///code/${encodeURIComponent(path)}.ts`),
    )
    const ed = monaco.editor.create(host.current!, {
      model,
      ariaLabel: initial.label,
      readOnly: initial.readOnly,
      theme: initial.theme === 'dark' ? 'vs-dark' : 'vs',
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 12,
      lineHeight,
      tabSize: 2,
      scrollBeyondLastLine: false,
      fixedOverflowWidgets: true,
    })
    editor.current = ed

    const edits = model.onDidChangeContent(() => changed(model.getValue()))
    const markers = monaco.editor.onDidChangeMarkers((uris) => {
      if (!uris.some((u) => u.toString() === model.uri.toString())) return

      setErrors(
        monaco.editor
          .getModelMarkers({ resource: model.uri })
          .filter((m) => m.severity === monaco.MarkerSeverity.Error)
          .map((m) => ({ line: m.startLineNumber, column: m.startColumn, message: m.message })),
      )
    })

    return () => {
      edits.dispose()
      markers.dispose()
      ed.dispose()
      model.dispose()
      editor.current = null
    }
  }, [initial, path])

  // Code set from outside, like a starter following its form, keeps the undo history.
  useEffect(() => {
    const model = editor.current?.getModel()
    if (model && model.getValue() !== value) {
      model.pushEditOperations([], [{ range: model.getFullModelRange(), text: value }], () => null)
    }
  }, [value])

  useEffect(() => monaco.editor.setTheme(theme === 'dark' ? 'vs-dark' : 'vs'), [theme])
  useEffect(() => editor.current?.updateOptions({ readOnly }), [readOnly])

  return (
    <div className="flex flex-col gap-1.5">
      <div
        ref={host}
        id={id}
        data-testid="monaco"
        style={{ height: rows * lineHeight + 8 }}
        className={cn('overflow-hidden rounded-md border', invalid && 'border-destructive')}
      />
      {errors.length > 0 && (
        <details className="text-xs">
          <summary className="cursor-pointer text-amber-700 dark:text-amber-500">
            {errors.length === 1 ? '1 type error' : `${errors.length} type errors`}: saving still
            works, as the server refuses only code that does not compile
          </summary>
          <ul className="mt-1 flex flex-col gap-0.5 font-mono">
            {errors.map((e, i) => (
              <li key={i}>
                {e.line}:{e.column} {e.message}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
}
