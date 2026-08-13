import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'

export type ThemeChoice = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'wollow.theme'

interface ThemeContextValue {
  choice: ThemeChoice
  /** What the choice actually resolves to right now. */
  resolved: 'light' | 'dark'
  setChoice: (choice: ThemeChoice) => void
  /** Cycles system -> light -> dark -> system. */
  cycle: () => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

function readStored(): ThemeChoice {
  const raw = localStorage.getItem(STORAGE_KEY)
  return raw === 'light' || raw === 'dark' ? raw : 'system'
}

function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [choice, setChoiceState] = useState<ThemeChoice>(readStored)
  const [systemDark, setSystemDark] = useState(systemPrefersDark)

  // 'system' has no data-theme stamp at all, so the CSS falls through to the
  // prefers-color-scheme media query. Only an explicit choice stamps the root.
  useEffect(() => {
    const root = document.documentElement
    if (choice === 'system') {
      root.removeAttribute('data-theme')
      localStorage.removeItem(STORAGE_KEY)
    } else {
      root.setAttribute('data-theme', choice)
      localStorage.setItem(STORAGE_KEY, choice)
    }
  }, [choice])

  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = (e: MediaQueryListEvent) => setSystemDark(e.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])

  const setChoice = useCallback((next: ThemeChoice) => setChoiceState(next), [])

  const cycle = useCallback(() => {
    setChoiceState((prev) => (prev === 'system' ? 'light' : prev === 'light' ? 'dark' : 'system'))
  }, [])

  const resolved: 'light' | 'dark' =
    choice === 'system' ? (systemDark ? 'dark' : 'light') : choice

  const value = useMemo(
    () => ({ choice, resolved, setChoice, cycle }),
    [choice, resolved, setChoice, cycle],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used within a ThemeProvider')
  return ctx
}
