
import { Link } from 'react-router-dom'
import { ArrowUpRight, Receipt, ArrowLeftRight, HelpCircle } from 'lucide-react'
import type { MoneyLink } from '../types'

function formatINR(amount: number): string {
  return new Intl.NumberFormat('en-IN', {
    style: 'currency',
    currency: 'INR',
    maximumFractionDigits: 0,
  }).format(amount)
}

/**
 * The Mail-side end of the cross-product link: shows what Money made of this
 * message and jumps straight to it.
 *
 * 'unrecognized' deliberately still renders. Those are messages the classifier
 * flagged as finance mail that no parser could read — showing them is how an
 * unsupported issuer stays visible instead of silently vanishing.
 */
export function MoneyLinkChip({ link, compact = false }: { link: MoneyLink; compact?: boolean }) {
  if (link.parsedAs === 'transaction' && link.transactionId) {
    return (
      <ChipLink
        to={`/money/transactions?txn=${link.transactionId}`}
        icon={<ArrowLeftRight size={compact ? 11 : 13} />}
        tone="accent"
        compact={compact}
      >
        {link.amount != null ? `Transaction · ${formatINR(link.amount)}` : 'View transaction'}
        {!compact && <ArrowUpRight size={12} className="opacity-60" />}
      </ChipLink>
    )
  }

  if (link.parsedAs === 'bill' && link.billId) {
    const due = link.dueDate ? ` · due ${link.dueDate}` : ''
    return (
      <ChipLink
        to="/money/bills"
        icon={<Receipt size={compact ? 11 : 13} />}
        tone="warn"
        compact={compact}
      >
        {link.amount != null ? `Bill · ${formatINR(link.amount)}${due}` : `Bill${due}`}
        {!compact && <ArrowUpRight size={12} className="opacity-60" />}
      </ChipLink>
    )
  }

  if (compact) return null

  return (
    <span
      title="Money looked at this message but no parser recognized the issuer's format."
      className="inline-flex items-center gap-1.5 rounded-full border border-[var(--color-border)] px-2.5 py-1 text-xs text-[var(--color-text-muted)]"
    >
      <HelpCircle size={13} />
      Finance mail, unrecognized format
    </span>
  )
}

function ChipLink({
  to,
  icon,
  tone,
  compact,
  children,
}: {
  to: string
  icon: React.ReactNode
  tone: 'accent' | 'warn'
  compact: boolean
  children: React.ReactNode
}) {
  const toneClass =
    tone === 'accent'
      ? 'bg-[var(--color-accent-tint)] text-[var(--color-accent-2)]'
      : 'bg-[var(--color-tint-orange-bg)] text-[var(--color-tint-orange)]'

  return (
    <Link
      to={to}
      onClick={(e) => e.stopPropagation()}
      className={`inline-flex items-center gap-1.5 rounded-full font-medium transition-opacity hover:opacity-80 ${toneClass} ${compact ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs'
        }`}
    >
      {icon}
      {children}
    </Link>
  )
}
