import { Link } from 'react-router-dom'
import {
  ArrowUpRight,
  Receipt,
  ArrowLeftRight,
  HelpCircle,
  Scale,
  TrendingUp,
  UserPlus,
  Wand2,
} from 'lucide-react'
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
 * 'unrecognized' deliberately still renders. Those are finance messages no
 * parser rule could read — showing them, with a way to define a parser from
 * the message itself, is how an untaught issuer stays visible instead of
 * silently vanishing. accountId/uid address the message so that link can be
 * built.
 */
export function MoneyLinkChip({
  link,
  compact = false,
  accountId,
  uid,
}: {
  link: MoneyLink
  compact?: boolean
  accountId?: number
  uid?: number
}) {
  const size = compact ? 11 : 13

  if (link.parsedAs === 'transaction' && link.transactionId) {
    return (
      <ChipLink
        to={`/money/transactions?txn=${link.transactionId}`}
        icon={<ArrowLeftRight size={size} />}
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
      <ChipLink to="/money/bills" icon={<Receipt size={size} />} tone="warn" compact={compact}>
        {link.amount != null ? `Bill · ${formatINR(link.amount)}${due}` : `Bill${due}`}
        {!compact && <ArrowUpRight size={12} className="opacity-60" />}
      </ChipLink>
    )
  }

  if (link.parsedAs === 'balance') {
    return (
      <ChipLink to="/money/accounts" icon={<Scale size={size} />} tone="accent" compact={compact}>
        Balance update
      </ChipLink>
    )
  }

  if (link.parsedAs === 'trade') {
    return (
      <ChipLink to="/money/investments" icon={<TrendingUp size={size} />} tone="accent" compact={compact}>
        Investment order
        {!compact && <ArrowUpRight size={12} className="opacity-60" />}
      </ChipLink>
    )
  }

  if (link.parsedAs === 'pending_account') {
    const who = link.pendingLast4
      ? `${link.pendingName || 'account'} •• ${link.pendingLast4}`
      : link.pendingName || 'an account'
    return (
      <ChipLink to="/money/accounts" icon={<UserPlus size={size} />} tone="warn" compact={compact}>
        {compact ? 'Needs account' : `Waiting for ${who} to be added`}
      </ChipLink>
    )
  }

  const defineTo =
    accountId && uid ? `/money/parsers/new?mailAccountId=${accountId}&uid=${uid}` : null

  if (compact) {
    if (!defineTo) return null
    return (
      <ChipLink to={defineTo} icon={<Wand2 size={size} />} tone="muted" compact>
        Define parser
      </ChipLink>
    )
  }

  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      <span
        title="Money looked at this message but none of your parsers could read it."
        className="inline-flex items-center gap-1.5 rounded-full border border-[var(--color-border)] px-2.5 py-1 text-xs text-[var(--color-text-muted)]"
      >
        <HelpCircle size={13} />
        Finance mail, no parser matched
      </span>
      {defineTo && (
        <ChipLink to={defineTo} icon={<Wand2 size={13} />} tone="accent" compact={false}>
          Define a parser from this email
          <ArrowUpRight size={12} className="opacity-60" />
        </ChipLink>
      )}
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
  tone: 'accent' | 'warn' | 'muted'
  compact: boolean
  children: React.ReactNode
}) {
  const toneClass =
    tone === 'accent'
      ? 'bg-[var(--color-accent-tint)] text-[var(--color-accent-2)]'
      : tone === 'warn'
        ? 'bg-[var(--color-tint-orange-bg)] text-[var(--color-tint-orange)]'
        : 'border border-[var(--color-border)] text-[var(--color-text-muted)]'

  return (
    <Link
      to={to}
      onClick={(e) => e.stopPropagation()}
      className={`inline-flex items-center gap-1.5 rounded-full font-medium transition-opacity hover:opacity-80 ${toneClass} ${
        compact ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs'
      }`}
    >
      {icon}
      {children}
    </Link>
  )
}
