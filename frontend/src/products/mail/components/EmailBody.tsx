import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { ImageOff } from 'lucide-react'
import { plainTextToHtml, prepareEmailHtml } from '../lib/emailHtml'

interface EmailBodyProps {
  html: string
  text: string
  /** Maps a `cid:` reference to a URL this app serves. */
  resolveCid: (contentId: string) => string
  /** Changes whenever the message does, so the frame resets between messages. */
  messageKey: string
}

/**
 * Renders a message body inside a sandboxed iframe.
 *
 * The iframe carries `allow-same-origin` but deliberately *not* `allow-scripts`
 * — with scripting off, same-origin grants the mail no power at all, while
 * letting this component measure the rendered height and letting inline `cid:`
 * images resolve against the app's own origin (a CSP `'self'` in an opaque
 * origin would match nothing).
 */
export function EmailBody({ html, text, resolveCid, messageKey }: EmailBodyProps) {
  const [showRemoteImages, setShowRemoteImages] = useState(false)
  const [height, setHeight] = useState(320)
  const frameRef = useRef<HTMLIFrameElement>(null)

  // A new message must not inherit the previous one's "images shown" choice.
  useEffect(() => setShowRemoteImages(false), [messageKey])

  const prepared = useMemo(() => {
    if (html.trim()) {
      return prepareEmailHtml(html, { resolveCid, showRemoteImages })
    }
    return prepareEmailHtml(plainTextToHtml(text), { resolveCid, showRemoteImages: true })
    // resolveCid is rebuilt per render by the parent; the message key is what
    // actually decides whether the output can differ.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [html, text, showRemoteImages, messageKey])

  // Measure after the frame paints so the body is never scrolled internally —
  // one scroll region for the whole message reads far better than two.
  useLayoutEffect(() => {
    const frame = frameRef.current
    if (!frame) return

    const measure = () => {
      const doc = frame.contentDocument
      if (!doc?.body) return
      const next = Math.max(doc.body.scrollHeight, doc.documentElement?.scrollHeight ?? 0)
      if (next > 0) setHeight(next + 8)
    }

    measure()
    frame.addEventListener('load', measure)
    // Images finishing later change the height; re-measure a few times rather
    // than leaving a gap or a clipped body.
    const timers = [120, 400, 1200].map((ms) => window.setTimeout(measure, ms))
    return () => {
      frame.removeEventListener('load', measure)
      timers.forEach(window.clearTimeout)
    }
  }, [prepared.html])

  return (
    <div>
      {prepared.blockedRemoteContent && (
        <div className="mb-4 flex flex-wrap items-center gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2,var(--color-surface))] px-3 py-2 text-sm">
          <ImageOff size={16} className="shrink-0 text-[var(--color-text-muted)]" />
          <span className="text-[var(--color-text-muted)]">
            Images in this message were not loaded, so the sender can&rsquo;t tell you opened it.
          </span>
          <button
            type="button"
            onClick={() => setShowRemoteImages(true)}
            className="ml-auto rounded-md border border-[var(--color-border-strong)] px-2.5 py-1 text-xs font-medium text-[var(--color-text)] transition hover:bg-[var(--color-hover)]"
          >
            Show images
          </button>
        </div>
      )}

      <iframe
        ref={frameRef}
        title="Message body"
        srcDoc={prepared.html}
        sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox"
        referrerPolicy="no-referrer"
        className="w-full rounded-lg border border-[var(--color-border)] bg-white"
        style={{ height }}
      />
    </div>
  )
}
