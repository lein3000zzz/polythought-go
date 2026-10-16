import { CircleAlert, Trash2, X } from 'lucide-react'
import { useMemo } from 'react'

import type { Problem } from '@/api/client'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import type { FlowNode, NodeSpec } from './graph'
import { ParamField } from './ParamField'
import { expressionProblem, takesExpressions } from './params'
import { fieldMessage, useNodeProblems } from './problems'
import { fieldChoices, fieldProblem, type Shape } from './shapes'

type Props = {
  node: FlowNode
  received?: Shape // what its items look like, when known
  onChange: (patch: Partial<Pick<NodeSpec, 'name' | 'continueOnFail' | 'notes'>>) => void
  onParam: (name: string, value: unknown) => void
  onDelete: () => void
}

/** Settings of the selected node. Changes apply as they are made. */
export function NodePanel({ node, received, onChange, onParam, onDelete }: Props) {
  const { spec, type } = node.data
  const problems = useNodeProblems(node.id)
  const declared = type?.params ?? []
  const params = spec.params ?? {}
  const unknown = Object.keys(params).filter((name) => !declared.some((p) => p.name === name))
  const nodeProblems = problems.filter((p) => !p.param || unknown.includes(p.param))
  // What its settings' expressions see: the items it gets, and its settings.
  const expressionScope = useMemo(
    () => ({ item: received, settings: type?.params ?? [] }),
    [received, type?.params],
  )

  return (
    <div className="flex flex-col gap-5 p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="truncate font-medium">{type?.label || spec.type}</h2>
          <p className="text-xs text-muted-foreground">
            {spec.type} · ID {spec.id}
          </p>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Delete node" onClick={onDelete}>
          <Trash2 />
        </Button>
      </div>

      {type?.description && <p className="text-sm text-muted-foreground">{type.description}</p>}

      {!type && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertDescription>
            This server has no node type “{spec.type}”. Delete the node or replace it.
          </AlertDescription>
        </Alert>
      )}

      {nodeProblems.length > 0 && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertDescription>
            <ul>
              {nodeProblems.map((p) => (
                <li key={p.message}>{fieldMessage(p)}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}

      <div className="flex flex-col gap-2">
        <Label htmlFor="node-name">Name</Label>
        <Input
          id="node-name"
          value={spec.name ?? ''}
          placeholder={type?.label || spec.type}
          onChange={(e) => onChange({ name: e.target.value })}
        />
      </div>

      <div className="flex flex-col gap-2">
        <Label htmlFor="node-notes">Notes</Label>
        <Textarea
          id="node-notes"
          rows={2}
          value={spec.notes ?? ''}
          placeholder="What this step is for, for whoever reads the workflow"
          onChange={(e) => onChange({ notes: e.target.value })}
        />
      </div>

      {declared.map((p) => (
        <ParamField
          key={p.name}
          spec={p}
          // The ports' setting shows its default as text to edit, so renaming a value in
          // place keeps its connections.
          value={params[p.name] ?? (p.name === type?.outputsParam ? p.default : undefined)}
          onChange={(value) => onParam(p.name, value)}
          problem={
            problems
              .filter((pr) => pr.param === p.name)
              .map(fieldMessage)
              .join(' ') ||
            expressionProblem(type, p, params[p.name]) ||
            fieldProblem(received, p, params[p.name] ?? p.default)
          }
          fields={p.field && received ? fieldChoices(received, p.fieldKinds) : undefined}
          expressions={takesExpressions(type, p)}
          expressionScope={expressionScope}
        />
      ))}

      {unknown.map((name) => (
        <div key={name} className="flex items-center justify-between gap-2 text-sm">
          <span>
            <span className="font-medium">{name}</span>
            <span className="text-muted-foreground"> is not a setting of this node</span>
          </span>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Remove ${name}`}
            onClick={() => onParam(name, undefined)}
          >
            <X />
          </Button>
        </div>
      ))}

      {type && !type.trigger && (
        <div className="flex flex-col gap-1">
          <div className="flex items-center justify-between gap-2">
            <Label htmlFor="node-continue">Continue on failure</Label>
            <Switch
              id="node-continue"
              checked={spec.continueOnFail ?? false}
              onCheckedChange={(on) => onChange({ continueOnFail: on })}
            />
          </div>
          <p className="text-xs text-muted-foreground">
            Send items that fail to an error port instead of stopping the run.
          </p>
        </div>
      )}
    </div>
  )
}

type WorkflowPanelProps = {
  problems: readonly Problem[]
}

/** Shown when no single node is selected: how to edit, and problems not about a node. */
export function WorkflowPanel({ problems }: WorkflowPanelProps) {
  const general = problems.filter((p) => !p.nodeId)

  return (
    <div className="flex flex-col gap-4 p-4 text-sm text-muted-foreground">
      {general.length > 0 && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertDescription>
            <ul>
              {general.map((p) => (
                <li key={p.message}>{fieldMessage(p)}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      <p>Add nodes from the node list on the left, by clicking or dragging them onto the canvas.</p>
      <p>Drag from an output port to an input port to connect two nodes.</p>
      <p>
        Select a node to change its settings. Select a node or connection and press Delete to remove
        it.
      </p>
    </div>
  )
}
