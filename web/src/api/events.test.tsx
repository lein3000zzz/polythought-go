import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { FakeEventSource } from '@/test/eventSource'

import type { Schemas } from './client'
import { applyExecution, executionListSize, useLiveEvents } from './events'
import { keys } from './keys'

const execution = (
  id: string,
  extra: Partial<Schemas['Execution']> = {},
): Schemas['Execution'] => ({
  id,
  workflowVersion: 1,
  triggeredBy: 'every',
  status: 'succeeded',
  startedAt: '2026-10-09T12:00:00Z',
  durationMs: 10,
  runs: [],
  ...extra,
})

describe('applyExecution', () => {
  it('puts a new execution at the top of a loaded list, once', () => {
    const qc = new QueryClient()
    qc.setQueryData(keys.executions('wf-1'), [execution('ex-1')])

    applyExecution(qc, { workflowId: 'wf-1', execution: execution('ex-2') })
    applyExecution(qc, { workflowId: 'wf-1', execution: execution('ex-2') })

    expect(
      qc.getQueryData<Schemas['Execution'][]>(keys.executions('wf-1'))?.map((e) => e.id),
    ).toEqual(['ex-2', 'ex-1'])
  })

  it('keeps lists at their size', () => {
    const qc = new QueryClient()
    const full = Array.from({ length: executionListSize }, (_, i) => execution(`ex-${i}`))
    qc.setQueryData(keys.executions('wf-1'), full)

    applyExecution(qc, { workflowId: 'wf-1', execution: execution('ex-new') })

    const list = qc.getQueryData<Schemas['Execution'][]>(keys.executions('wf-1'))
    expect(list).toHaveLength(executionListSize)
    expect(list?.[0].id).toBe('ex-new')
  })

  it('leaves lists alone that nobody loaded', () => {
    const qc = new QueryClient()

    applyExecution(qc, { workflowId: 'wf-1', execution: execution('ex-1') })

    expect(qc.getQueryData(keys.executions('wf-1'))).toBeUndefined()
    expect(qc.getQueryData(keys.execution('wf-1', 'ex-1'))).toMatchObject({ id: 'ex-1' })
  })

  it('keeps a cached copy that has item samples', () => {
    const qc = new QueryClient()
    const withSamples = execution('ex-1', {
      runs: [
        {
          nodeId: 'manual',
          status: 'succeeded',
          startedAt: '2026-10-09T12:00:00Z',
          finishedAt: '2026-10-09T12:00:00Z',
          samples: { main: [{ json: {} }] },
        },
      ],
    })
    qc.setQueryData(keys.execution('wf-1', 'ex-1'), withSamples)

    applyExecution(qc, { workflowId: 'wf-1', execution: execution('ex-1') })

    expect(qc.getQueryData(keys.execution('wf-1', 'ex-1'))).toBe(withSamples)
  })
})

describe('useLiveEvents', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  function Live() {
    useLiveEvents()
    return null
  }

  it('applies events and reopens a refused stream later', () => {
    vi.useFakeTimers()

    const qc = new QueryClient()
    qc.setQueryData(keys.executions('wf-1'), [])
    const invalidate = vi.spyOn(qc, 'invalidateQueries')

    const { unmount } = render(
      <QueryClientProvider client={qc}>
        <Live />
      </QueryClientProvider>,
    )

    expect(FakeEventSource.open).toHaveLength(1)
    expect(FakeEventSource.open[0].url).toBe('/api/v1/events')

    act(() =>
      FakeEventSource.open[0].emit('execution', {
        workflowId: 'wf-1',
        execution: execution('ex-1'),
      }),
    )
    expect(qc.getQueryData<Schemas['Execution'][]>(keys.executions('wf-1'))).toHaveLength(1)

    act(() => FakeEventSource.open[0].refuse())
    expect(invalidate).toHaveBeenCalledWith({ queryKey: keys.me })
    expect(FakeEventSource.open).toHaveLength(0)

    act(() => vi.advanceTimersByTime(15_000))
    expect(FakeEventSource.open).toHaveLength(1)

    unmount()
    expect(FakeEventSource.open).toHaveLength(0)
  })
})
