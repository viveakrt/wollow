export function Placeholder({ title, note }: { title: string; note: string }) {
  return (
    <div className="p-8">
      <h1 className="text-2xl font-semibold mb-1">{title}</h1>
      <p className="text-[var(--color-text-muted)]">{note}</p>
    </div>
  )
}
