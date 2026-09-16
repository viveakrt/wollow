import { useMemo, useRef, useState } from 'react'
import { X, Check } from 'lucide-react'
import type { LabeledSpan, ParserFieldSpec } from '../../types'
import { clearSelection, selectionOffsets, sliceCodePoints } from '../../lib/selection'

/** One colour per field, cycling; the legend and the marks share them. */
const PALETTE = [
  'var(--color-tint-violet-bg)',
  'var(--color-tint-green-bg)',
  'var(--color-tint-orange-bg)',
  'var(--color-tint-cyan-bg)',
  'var(--color-tint-pink-bg)',
  'var(--color-accent-tint)',
]

const PALETTE_FG = [
  'var(--color-tint-violet)',
  'var(--color-tint-green)',
  'var(--color-tint-orange)',
  'var(--color-tint-cyan)',
  'var(--color-tint-pink)',
  'var(--color-accent-2)',
]

function fieldColor(index: number): { bg: string; fg: string } {
  return { bg: PALETTE[index % PALETTE.length], fg: PALETTE_FG[index % PALETTE_FG.length] }
}

interface Pending {
  start: number
  end: number
  text: string
  x: number
  y: number
}

/**
 * The sample email's text with the marked values highlighted. Select any run
 * of text and a small menu asks which value it is.
 *
 * The <pre> holds nothing but text nodes and <mark>s, so a Range's string is
 * exactly the source text and offsets line up with what the server anchors on.
 */
export function SpanEditor({
  text,
  fields,
  spans,
  onChange,
}: {
  text: string
  fields: ParserFieldSpec[]
  spans: LabeledSpan[]
  onChange: (spans: LabeledSpan[]) => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const preRef = useRef<HTMLPreElement>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const [error, setError] = useState<string | null>(null)

  const indexOf = useMemo(() => new Map(fields.map((f, i) => [f.name, i])), [fields])
  const labelOf = useMemo(() => new Map(fields.map((f) => [f.name, f.label])), [fields])
  const sorted = useMemo(() => [...spans].sort((a, b) => a.start - b.start), [spans])

  const segments = useMemo(() => {
    const out: { text: string; span?: LabeledSpan }[] = []
    let cursor = 0
    for (const s of sorted) {
      if (s.start > cursor) out.push({ text: sliceCodePoints(text, cursor, s.start) })
      out.push({ text: sliceCodePoints(text, s.start, s.end), span: s })
      cursor = s.end
    }
    const total = Array.from(text).length
    if (cursor < total) out.push({ text: sliceCodePoints(text, cursor, total) })
    return out
  }, [text, sorted])

  function handleMouseUp() {
    const pre = preRef.current
    const container = containerRef.current
    if (!pre || !container) return
    const sel = selectionOffsets(pre)
    if (!sel) {
      setPending(null)
      return
    }
    const range = window.getSelection()?.getRangeAt(0)
    const rect = range?.getBoundingClientRect()
    const host = container.getBoundingClientRect()
    setError(null)
    setPending({
      ...sel,
      x: rect ? Math.max(0, rect.left - host.left) : 0,
      y: rect ? rect.bottom - host.top + 6 : 0,
    })
  }

  function assign(name: string) {
    if (!pending) return
    const overlap = spans.find(
      (s) => s.name !== name && s.start < pending.end && pending.start < s.end,
    )
    if (overlap) {
      setError(`That overlaps "${labelOf.get(overlap.name) ?? overlap.name}". Clear it first.`)
      return
    }
    onChange([
      ...spans.filter((s) => s.name !== name),
      { name, start: pending.start, end: pending.end, sample: pending.text },
    ])
    setPending(null)
    clearSelection()
  }

  function clear(name: string) {
    onChange(spans.filter((s) => s.name !== name))
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-1.5">
        {fields.map((f) => {
          const assigned = spans.find((s) => s.name === f.name)
          const color = fieldColor(indexOf.get(f.name) ?? 0)
          return (
            <span
              key={f.name}
              className="inline-flex items-center gap-1.5 rounded-full border border-[var(--color-border)] px-2.5 py-1 text-xs"
              style={assigned ? { backgroundColor: color.bg, color: color.fg } : undefined}
              title={assigned ? `Marked: ${assigned.sample}` : f.required ? 'Required' : 'Optional'}
            >
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: color.fg, opacity: assigned ? 1 : 0.4 }}
              />
              {f.label}
              {f.required && !assigned && <span className="text-[var(--color-negative)]">*</span>}
              {assigned && (
                <>
                  <Check size={11} />
                  <button
                    type="button"
                    onClick={() => clear(f.name)}
                    title="Clear"
                    className="ml-0.5 rounded-full hover:opacity-70"
                  >
                    <X size={11} />
                  </button>
                </>
              )}
            </span>
          )
        })}
      </div>

      <p className="text-xs text-[var(--color-text-muted)]">
        Select a value in the email below, then choose what it is. The first line is the subject.
      </p>

      {error && <p className="text-xs text-[var(--color-negative)]">{error}</p>}

      <div ref={containerRef} className="relative">
        <pre
          ref={preRef}
          onMouseUp={handleMouseUp}
          className="max-h-[32rem] overflow-y-auto whitespace-pre-wrap break-words rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-4 font-mono text-[13px] leading-6 select-text"
        >
          {segments.map((seg, i) =>
            seg.span ? (
              <mark
                key={i}
                data-field={seg.span.name}
                title={labelOf.get(seg.span.name) ?? seg.span.name}
                style={{
                  backgroundColor: fieldColor(indexOf.get(seg.span.name) ?? 0).bg,
                  color: fieldColor(indexOf.get(seg.span.name) ?? 0).fg,
                }}
                className="rounded px-0.5"
              >
                {seg.text}
              </mark>
            ) : (
              seg.text
            ),
          )}
        </pre>

        {pending && (
          <div
            className="absolute z-20 w-64 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-2 shadow-lg"
            style={{ left: Math.min(pending.x, 320), top: pending.y }}
          >
            <div className="mb-1.5 flex items-center justify-between px-1">
              <span className="truncate text-xs font-medium" title={pending.text}>
                “{pending.text.length > 28 ? pending.text.slice(0, 28) + '…' : pending.text}” is the…
              </span>
              <button
                type="button"
                onClick={() => {
                  setPending(null)
                  clearSelection()
                }}
                className="rounded p-0.5 text-[var(--color-text-muted)] hover:bg-[var(--color-hover)]"
              >
                <X size={12} />
              </button>
            </div>
            <div className="flex flex-col">
              {fields.map((f) => {
                const color = fieldColor(indexOf.get(f.name) ?? 0)
                const assigned = spans.some((s) => s.name === f.name)
                return (
                  <button
                    key={f.name}
                    type="button"
                    onClick={() => assign(f.name)}
                    className="flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-[var(--color-hover)]"
                  >
                    <span className="h-2 w-2 rounded-full" style={{ backgroundColor: color.fg }} />
                    <span className="flex-1">{f.label}</span>
                    {f.required && <span className="text-[10px] text-[var(--color-text-muted)]">required</span>}
                    {assigned && <span className="text-[10px] text-[var(--color-text-muted)]">replace</span>}
                  </button>
                )
              })}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
