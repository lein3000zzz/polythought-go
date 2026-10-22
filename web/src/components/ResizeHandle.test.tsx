import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'

import { ResizeHandle } from './ResizeHandle'

function Panel({ panel }: { panel: 'left' | 'right' | 'below' }) {
  const [size, setSize] = useState(240)

  return (
    <ResizeHandle
      panel={panel}
      label="Resize"
      size={size}
      min={200}
      max={400}
      onResize={setSize}
      onReset={() => setSize(240)}
    />
  )
}

const handle = () => screen.getByRole('separator', { name: 'Resize' })

describe('ResizeHandle (D369)', () => {
  it('says its size and bounds, as a window splitter', () => {
    render(<Panel panel="left" />)

    expect(handle()).toHaveAttribute('aria-orientation', 'vertical')
    expect(handle()).toHaveAttribute('aria-valuenow', '240')
    expect(handle()).toHaveAttribute('aria-valuemin', '200')
    expect(handle()).toHaveAttribute('aria-valuemax', '400')
  })

  it.each([
    { panel: 'left' as const, grow: '{ArrowRight}', shrink: '{ArrowLeft}' },
    { panel: 'right' as const, grow: '{ArrowLeft}', shrink: '{ArrowRight}' },
    { panel: 'below' as const, grow: '{ArrowUp}', shrink: '{ArrowDown}' },
  ])('moves from the keyboard, the panel $panel of it', async ({ panel, grow, shrink }) => {
    const user = userEvent.setup()
    render(<Panel panel={panel} />)
    handle().focus()

    await user.keyboard(grow)
    expect(handle()).toHaveAttribute('aria-valuenow', '256')
    await user.keyboard(`${shrink}${shrink}`)
    expect(handle()).toHaveAttribute('aria-valuenow', '224')

    await user.keyboard('{Home}')
    expect(handle()).toHaveAttribute('aria-valuenow', '200')
    await user.keyboard(shrink)
    expect(handle()).toHaveAttribute('aria-valuenow', '200')
    await user.keyboard('{End}')
    expect(handle()).toHaveAttribute('aria-valuenow', '400')
  })

  it('follows the pointer, within its bounds, and resets on a double-click', async () => {
    const user = userEvent.setup()
    render(<Panel panel="right" />)

    fireEvent.pointerDown(handle(), { button: 0, pointerId: 1, clientX: 1000 })
    fireEvent.pointerMove(handle(), { pointerId: 1, clientX: 950 })
    expect(handle()).toHaveAttribute('aria-valuenow', '290')
    expect(document.body.style.cursor).toBe('col-resize')

    fireEvent.pointerMove(handle(), { pointerId: 1, clientX: 100 })
    expect(handle()).toHaveAttribute('aria-valuenow', '400')
    fireEvent.pointerUp(handle(), { pointerId: 1, clientX: 100 })
    expect(document.body.style.cursor).toBe('')

    // Let go, it no longer follows.
    fireEvent.pointerMove(handle(), { pointerId: 1, clientX: 1000 })
    expect(handle()).toHaveAttribute('aria-valuenow', '400')

    await user.dblClick(handle())
    expect(handle()).toHaveAttribute('aria-valuenow', '240')
  })
})
