import type { LucideIcon } from 'lucide-react'
import clsx from 'clsx'

export function StatTile({
  icon: Icon,
  label,
  value,
  sublabel,
  iconColor = 'text-violet-400',
  iconBg = 'bg-violet-500/15',
}: {
  icon: LucideIcon
  label: string
  value: string
  sublabel?: { text: string; positive?: boolean }
  iconColor?: string
  iconBg?: string
}) {
  return (
    <div className="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-4 flex flex-col gap-3 min-w-0">
      <div className="flex items-center gap-3">
        <div className={clsx('w-9 h-9 rounded-lg flex items-center justify-center shrink-0', iconBg)}>
          <Icon size={18} className={iconColor} />
        </div>
        <span className="text-sm text-[var(--color-text-muted)] truncate">{label}</span>
      </div>
      <div>
        <div className="text-xl font-semibold tracking-tight truncate">{value}</div>
        {sublabel && (
          <div
            className={clsx(
              'text-xs mt-1',
              sublabel.positive === undefined
                ? 'text-[var(--color-text-muted)]'
                : sublabel.positive
                  ? 'text-[var(--color-positive)]'
                  : 'text-[var(--color-negative)]',
            )}
          >
            {sublabel.text}
          </div>
        )}
      </div>
    </div>
  )
}
