import type { ReactNode } from 'react'
import clsx from 'clsx'

export function Card({
  children,
  className,
  title,
  action,
}: {
  children: ReactNode
  className?: string
  title?: string
  action?: ReactNode
}) {
  return (
    <div
      className={clsx(
        'bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-5',
        className,
      )}
    >
      {(title || action) && (
        <div className="flex items-center justify-between mb-4">
          {title && <h3 className="text-sm font-semibold text-[var(--color-text)]">{title}</h3>}
          {action}
        </div>
      )}
      {children}
    </div>
  )
}
