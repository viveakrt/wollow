import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Search, Loader2 } from 'lucide-react'
import { api } from '../api'

/** A holding's market symbol: typed directly, or found by searching Yahoo Finance by name, ticker or ISIN. */
export function SymbolPicker({
  value,
  onChange,
  suggestQuery = '',
}: {
  value: string
  onChange: (symbol: string) => void
  suggestQuery?: string
}) {
  const [searching, setSearching] = useState(false)
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')

  useEffect(() => {
    const t = setTimeout(() => setDebounced(query.trim()), 350)
    return () => clearTimeout(t)
  }, [query])

  const results = useQuery({
    queryKey: ['money', 'market-search', debounced],
    queryFn: () => api.market.search(debounced),
    enabled: searching && debounced.length >= 2,
    staleTime: 5 * 60 * 1000,
  })

  return (
    <div>
      <div className="flex gap-2">
        <input
          value={value}
          onChange={(e) => onChange(e.target.value.toUpperCase())}
          placeholder="Blank = detect automatically"
          className={inputClass}
        />
        <button
          type="button"
          onClick={() => {
            if (!searching && !query) setQuery(suggestQuery)
            setSearching((v) => !v)
          }}
          className="flex shrink-0 items-center gap-1.5 rounded-lg border border-[var(--color-border-strong)] px-3 py-2 text-xs font-medium hover:bg-[var(--color-hover)]"
        >
          <Search size={13} />
          Find
        </button>
      </div>

      {searching && (
        <div className="mt-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-2">
          <input
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Company name, ticker or ISIN"
            className={inputClass}
          />
          <div className="mt-2 max-h-48 overflow-y-auto">
            {results.isFetching ? (
              <div className="flex items-center gap-2 px-2 py-2 text-xs text-[var(--color-text-muted)]">
                <Loader2 size={12} className="animate-spin" /> Searching…
              </div>
            ) : results.isError ? (
              <p className="px-2 py-2 text-xs text-[var(--color-negative)]">Search failed — try again.</p>
            ) : (results.data ?? []).length === 0 ? (
              debounced.length >= 2 && <p className="px-2 py-2 text-xs text-[var(--color-text-muted)]">No matches.</p>
            ) : (
              (results.data ?? []).map((hit) => (
                <button
                  key={hit.symbol}
                  type="button"
                  onClick={() => {
                    onChange(hit.symbol)
                    setSearching(false)
                  }}
                  className="flex w-full items-center justify-between gap-3 rounded px-2 py-1.5 text-left text-xs hover:bg-[var(--color-hover)]"
                >
                  <span className="min-w-0 truncate">
                    <span className="font-medium">{hit.symbol}</span>{' '}
                    <span className="text-[var(--color-text-muted)]">{hit.name}</span>
                  </span>
                  <span className="shrink-0 text-[var(--color-text-subtle)]">
                    {hit.exchange} · {hit.quoteType.toLowerCase()}
                  </span>
                </button>
              ))
            )}
          </div>
        </div>
      )}
      <p className="mt-1 text-xs text-[var(--color-text-subtle)]">
        e.g. AAPL, RELIANCE.NS, or AMFI:&lt;ISIN&gt; for a mutual fund. Type NONE to never fetch a price.
      </p>
    </div>
  )
}

const inputClass =
  'w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm focus:border-[var(--color-accent)] focus:outline-none'
