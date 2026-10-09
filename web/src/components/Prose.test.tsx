import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Prose } from './Prose'

describe('Prose', () => {
  it('makes paragraphs and lists, with or without a lead-in line', () => {
    render(
      <Prose
        text={
          'First paragraph.\n\n- one\n- two\n\nTo get a URL:\n- Discord: copy it\n- Slack: add the app\n\nLast\nline'
        }
      />,
    )

    expect(screen.getByText('First paragraph.').tagName).toBe('P')
    expect(screen.getAllByRole('list')).toHaveLength(2)
    expect(screen.getByText('one').tagName).toBe('LI')
    expect(screen.getByText('To get a URL:').tagName).toBe('P')
    expect(screen.getByText('Slack: add the app').tagName).toBe('LI')
    expect(screen.getByText(/Last/)).toHaveClass('whitespace-pre-line')
  })
})
