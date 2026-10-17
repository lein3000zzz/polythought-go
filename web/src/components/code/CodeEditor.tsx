import { lazy, Suspense } from 'react'

import { PlainEditor } from './PlainEditor'

export type CodeEditorProps = {
  id?: string // for a label's htmlFor; the plain editor takes it
  value: string
  onChange: (value: string) => void
  label: string // what assistive technology calls the editor
  rows: number // its height, in lines
  fill?: boolean // as tall as its parent instead
  readOnly?: boolean
  invalid?: boolean // the server refused the code
  markers?: CodeMarker[] // what the server found wrong, underlined
  typeErrorNote?: string // why type errors do not matter here, after their count
}

/** A place in the code, from 1, and what is wrong there. */
export type CodeMarker = { line: number; column: number; message: string }

// Monaco and its TypeScript worker are most of a megabyte: loaded with the first editor.
const MonacoEditor = lazy(() => import('./MonacoEditor'))

/**
 * TypeScript in Monaco, with the user's @polythought/sdk for completion and type errors,
 * which it lists but does not stop saving for (D65). Plain text until it loads.
 */
export function CodeEditor(props: CodeEditorProps) {
  return (
    <Suspense fallback={<PlainEditor {...props} />}>
      <MonacoEditor {...props} />
    </Suspense>
  )
}
