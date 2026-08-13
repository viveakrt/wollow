import { useMemo, useState } from 'react'
import { CHART_OTHER, CHART_SERIES, categoryStyle } from '../theme/categories'

interface Slice {
  key: string
  label: string
  count: number
  color: string
}

interface CategoryDonutProps {
  data: { key: string; count: number }[]
  onSelect?: (category: string | null) => void
  selected?: string | null
}

// Top slices get a validated series colour in fixed order; the long tail folds
// into a single neutral "Other" rather than inventing new hues.
const MAX_SLICES = 6

export function CategoryDonut({ data, onSelect, selected }: CategoryDonutProps) {
  const [hovered, setHovered] = useState<string | null>(null)

  const { slices, total } = useMemo(() => {
    const sorted = [...data].sort((a, b) => b.count - a.count)
    const head = sorted.slice(0, MAX_SLICES)
    const tail = sorted.slice(MAX_SLICES)

    const out: Slice[] = head.map((d, i) => ({
      key: d.key,
      label: categoryStyle(d.key).label,
      count: d.count,
      color: CHART_SERIES[i % CHART_SERIES.length],
    }))

    const tailCount = tail.reduce((sum, d) => sum + d.count, 0)
    if (tailCount > 0) {
      out.push({ key: '__other__', label: 'Other', count: tailCount, color: CHART_OTHER })
    }

    return { slices: out, total: out.reduce((sum, s) => sum + s.count, 0) }
  }, [data])

  if (total === 0) {
    return (
      <p className="py-6 text-center text-xs text-[var(--color-text-subtle)]">
        No classified messages yet.
      </p>
    )
  }

  const size = 132
  const stroke = 18
  const radius = (size - stroke) / 2
  const circumference = 2 * Math.PI * radius
  // 2px visual gap between adjacent segments, expressed in arc length.
  const gap = 2

  let offset = 0

  return (
    <div>
      <div className="flex items-center gap-4">
        <svg
          width={size}
          height={size}
          viewBox={`0 0 ${size} ${size}`}
          role="img"
          aria-label={`Category breakdown of ${total} classified messages`}
          className="shrink-0"
        >
          <g transform={`rotate(-90 ${size / 2} ${size / 2})`}>
            {slices.map((slice) => {
              const fraction = slice.count / total
              const length = Math.max(fraction * circumference - gap, 0.5)
              const dash = `${length} ${circumference - length}`
              const thisOffset = offset
              offset += fraction * circumference
              const dim = hovered !== null && hovered !== slice.key

              return (
                <circle
                  key={slice.key}
                  cx={size / 2}
                  cy={size / 2}
                  r={radius}
                  fill="none"
                  stroke={slice.color}
                  strokeWidth={stroke}
                  strokeDasharray={dash}
                  strokeDashoffset={-thisOffset}
                  className="cursor-pointer transition-opacity"
                  style={{ opacity: dim ? 0.35 : 1 }}
                  onMouseEnter={() => setHovered(slice.key)}
                  onMouseLeave={() => setHovered(null)}
                  onClick={() =>
                    onSelect?.(
                      slice.key === '__other__' || selected === slice.key ? null : slice.key,
                    )
                  }
                >
                  <title>{`${slice.label}: ${slice.count.toLocaleString()} (${pct(slice.count, total)})`}</title>
                </circle>
              )
            })}
          </g>
          <text
            x="50%"
            y="47%"
            textAnchor="middle"
            className="fill-slate-900 text-[17px] font-semibold"
          >
            {compact(total)}
          </text>
          <text x="50%" y="61%" textAnchor="middle" className="fill-slate-400 text-[10px]">
            classified
          </text>
        </svg>

        {/* Legend carries the labels and values, so identity never rests on
            colour alone (and satisfies the sub-3:1 contrast relief rule). */}
        <ul className="min-w-0 flex-1 space-y-1">
          {slices.map((slice) => (
            <li key={slice.key}>
              <button
                type="button"
                onMouseEnter={() => setHovered(slice.key)}
                onMouseLeave={() => setHovered(null)}
                onClick={() =>
                  onSelect?.(
                    slice.key === '__other__' || selected === slice.key ? null : slice.key,
                  )
                }
                className={`flex w-full items-center gap-2 rounded px-1 py-0.5 text-left transition hover:bg-[var(--color-bg)] ${
                  selected === slice.key ? 'bg-[var(--color-surface-2)]' : ''
                }`}
              >
                <span
                  aria-hidden
                  className="h-2 w-2 shrink-0 rounded-full"
                  style={{ backgroundColor: slice.color }}
                />
                <span className="min-w-0 flex-1 truncate text-[11px] text-[var(--color-text-muted)]">
                  {slice.label}
                </span>
                <span className="shrink-0 text-[11px] tabular-nums text-[var(--color-text-subtle)]">
                  {pct(slice.count, total)}
                </span>
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

function pct(value: number, total: number): string {
  return `${Math.round((value / total) * 100)}%`
}

function compact(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`
  return String(n)
}
