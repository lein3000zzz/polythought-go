/**
 * What a confirmation changes, a line each (A3), such as a workflow's nodes and settings
 * against the graph it has: read it before you confirm. Nothing when it says nothing more.
 */
export function ConfirmationDetail({ detail, open = false }: { detail?: string; open?: boolean }) {
  if (!detail) return null

  return (
    <details open={open} className="text-xs">
      <summary className="cursor-pointer text-muted-foreground">What it changes</summary>
      <pre className="mt-1 max-h-64 overflow-auto rounded-md bg-muted p-2 font-mono whitespace-pre-wrap break-words">
        {detail}
      </pre>
    </details>
  )
}
