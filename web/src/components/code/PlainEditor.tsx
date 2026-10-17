import { cn } from 'cn'

import { Textarea } from '@/components/ui/textarea'

import type { CodeEditorProps } from './CodeEditor'

/**
 * Code as plain text, in a monospace box: what shows while Monaco loads, and in tests,
 * where it cannot run.
 */
export function PlainEditor({
  id,
  value,
  onChange,
  label,
  rows,
  fill,
  readOnly,
  invalid,
}: CodeEditorProps) {
  return (
    <Textarea
      id={id}
      rows={rows}
      spellCheck={false}
      className={cn('font-mono text-xs', fill && 'h-full resize-none')}
      value={value}
      readOnly={readOnly}
      aria-label={id ? undefined : label}
      aria-invalid={invalid || undefined}
      onChange={(e) => onChange(e.target.value)}
    />
  )
}
