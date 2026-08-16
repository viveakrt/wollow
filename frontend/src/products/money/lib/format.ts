export function formatINR(amount: number, compact = false): string {
  if (compact && Math.abs(amount) >= 100000) {
    const lakh = amount / 100000
    if (Math.abs(amount) >= 10000000) {
      return `₹${(amount / 10000000).toFixed(2)}Cr`
    }
    return `₹${lakh.toFixed(2)}L`
  }
  return new Intl.NumberFormat('en-IN', {
    style: 'currency',
    currency: 'INR',
    maximumFractionDigits: 0,
  }).format(amount)
}

/**
 * Money in whichever currency it is actually held in.
 *
 * A US stock is priced in dollars; rendering it with a ₹ sign because the rest
 * of the app is rupee-denominated states a number that is wrong by the
 * exchange rate — about 85x — while looking perfectly ordinary.
 */
export function formatMoney(amount: number, currency = 'INR', compact = false): string {
  if (currency === 'INR') return formatINR(amount, compact)
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency,
    maximumFractionDigits: Math.abs(amount) >= 1000 ? 0 : 2,
  }).format(amount)
}

export function formatDate(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleDateString('en-IN', { day: '2-digit', month: 'short', year: 'numeric' })
}

export function formatPercent(v: number): string {
  return `${v >= 0 ? '+' : ''}${v.toFixed(2)}%`
}
