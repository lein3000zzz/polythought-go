import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { setSystemDark } from '@/test/media'

import { setThemePreference, themeStorageKey, useTheme } from './theme'

const isDark = () => document.documentElement.classList.contains('dark')

describe('theme', () => {
  it('applies and remembers a choice', () => {
    const { result } = renderHook(() => useTheme())

    act(() => setThemePreference('dark'))
    expect(result.current).toEqual({ preference: 'dark', theme: 'dark' })
    expect(isDark()).toBe(true)
    expect(localStorage.getItem(themeStorageKey)).toBe('dark')

    act(() => setThemePreference('light'))
    expect(isDark()).toBe(false)
    expect(localStorage.getItem(themeStorageKey)).toBe('light')
  })

  it('follows the system until told otherwise, and when it switches', () => {
    const { result } = renderHook(() => useTheme())

    act(() => setSystemDark(true))
    expect(result.current).toEqual({ preference: 'system', theme: 'dark' })
    expect(isDark()).toBe(true)

    act(() => setThemePreference('light'))
    act(() => setSystemDark(false))
    act(() => setSystemDark(true))
    expect(result.current.theme, 'a choice beats the system').toBe('light')

    act(() => setThemePreference('system'))
    expect(result.current.theme).toBe('dark')
    expect(localStorage.getItem(themeStorageKey)).toBeNull()
  })

  it('follows a choice made in another tab', () => {
    const { result } = renderHook(() => useTheme())

    act(() => {
      localStorage.setItem(themeStorageKey, 'dark')
      window.dispatchEvent(new StorageEvent('storage', { key: themeStorageKey }))
    })

    expect(result.current).toEqual({ preference: 'dark', theme: 'dark' })
    expect(isDark()).toBe(true)
  })
})
