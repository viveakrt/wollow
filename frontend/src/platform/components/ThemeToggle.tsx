import { Monitor, Moon, Sun } from 'lucide-react'
import { useTheme } from '../state/theme'

const NEXT_LABEL = {
  system: 'Match system theme — click for light',
  light: 'Light theme — click for dark',
  dark: 'Dark theme — click to match system',
} as const

export function ThemeToggle({ size = 18 }: { size?: number }) {
  const { choice, cycle } = useTheme()
  const Icon = choice === 'system' ? Monitor : choice === 'light' ? Sun : Moon

  return (
    <button
      type="button"
      onClick={cycle}
      title={NEXT_LABEL[choice]}
      aria-label={NEXT_LABEL[choice]}
      className="flex items-center justify-center rounded-lg p-2 text-[var(--color-text-muted)] transition-colors hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]"
    >
      <Icon size={size} />
    </button>
  )
}
