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

export function formatDate(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleDateString('en-IN', { day: '2-digit', month: 'short', year: 'numeric' })
}

export function formatPercent(v: number): string {
  return `${v >= 0 ? '+' : ''}${v.toFixed(2)}%`
}
