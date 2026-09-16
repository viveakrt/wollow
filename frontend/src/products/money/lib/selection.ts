/**
 * A text selection inside an element, as code-point offsets into that
 * element's text content.
 *
 * Offsets are code points rather than UTF-16 units because the server
 * anchors on runes: a "₹" or an emoji in the subject is one rune there and
 * one code point here, but two UTF-16 units — which would put every offset
 * after it off by one.
 */
export interface TextSelection {
  start: number
  end: number
  text: string
}

function codePoints(s: string): number {
  return Array.from(s).length
}

/** Slices by code point, matching how the server slices by rune. */
export function sliceCodePoints(text: string, start: number, end: number): string {
  return Array.from(text).slice(start, end).join('')
}

/**
 * Reads the current selection relative to `container`, which must render its
 * text as plain text nodes (optionally wrapped in inline elements) so that
 * `Range.toString()` equals the underlying text.
 */
export function selectionOffsets(container: HTMLElement): TextSelection | null {
  const sel = window.getSelection()
  if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return null
  const range = sel.getRangeAt(0)
  if (!container.contains(range.startContainer) || !container.contains(range.endContainer)) return null

  const before = range.cloneRange()
  before.selectNodeContents(container)
  before.setEnd(range.startContainer, range.startOffset)

  const text = range.toString()
  if (!text.trim()) return null
  const start = codePoints(before.toString())
  return { start, end: start + codePoints(text), text }
}

export function clearSelection() {
  window.getSelection()?.removeAllRanges()
}
