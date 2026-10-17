import { Code } from 'lucide-react'

import { CodeEditor, type CodeMarker } from '@/components/code/CodeEditor'
import { Button } from '@/components/ui/button'

/** Why Apply did not work: what the server said, and where in the code when it knows. */
export type ApplyFailure = { message: string; problems: CodeMarker[] }

type Props = {
  value: string
  onChange: (value: string) => void
  onApply: () => void
  applying: boolean
  applied: boolean // the canvas has what the code says
  failure?: ApplyFailure
  codeOff: boolean // an admin turned code off for the user
}

/**
 * The workflow as code (D76): printed from the canvas, to edit and apply back to it. Type
 * errors are shown, not blocking; only code that does not compile or does not run is
 * refused (D65). Without code access it is there to read, Apply off (D75).
 */
export function CodeView({ value, onChange, onApply, applying, applied, failure, codeOff }: Props) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-3 border-b px-4 py-2">
        <Button size="sm" onClick={onApply} disabled={codeOff || applying || applied}>
          <Code />
          {applying ? 'Applying…' : 'Apply'}
        </Button>
        <span className="text-xs text-muted-foreground">
          {codeOff
            ? 'An admin has turned code off for you: you can read the code, not apply it.'
            : applied
              ? 'The canvas shows this code.'
              : 'Not applied yet: Apply puts it on the canvas, as unsaved changes.'}
        </span>
      </div>

      <div className="min-h-0 flex-1 p-2">
        <CodeEditor
          value={value}
          onChange={onChange}
          label="Workflow code"
          rows={30}
          fill
          readOnly={codeOff}
          invalid={failure !== undefined}
          markers={failure?.problems}
          typeErrorNote="Apply still works, as only code that does not compile or run is refused"
        />
      </div>

      {failure && (
        <div role="alert" className="max-h-40 overflow-y-auto border-t px-4 py-2 text-sm">
          <p className="text-destructive">Not applied: {failure.message}</p>
          {failure.problems.length > 0 && (
            <ul className="mt-1 flex flex-col gap-0.5 font-mono text-xs">
              {failure.problems.map((p, i) => (
                <li key={i}>
                  {p.line}:{p.column} {p.message}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}
