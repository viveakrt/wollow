/**
 * Turning a mail HTML body into something safe to show.
 *
 * Email HTML is arbitrary markup written by strangers, so it gets three
 * independent layers of containment and none of them is trusted alone:
 *
 *  1. this module strips executable markup and rewrites URLs,
 *  2. the caller renders the result in a `sandbox`ed iframe with no
 *     `allow-scripts`, so nothing here can run even if the strip missed it,
 *  3. the document carries its own CSP `<meta>`, which is what actually
 *     enforces remote-image blocking — an attribute rename alone would be
 *     defeated by any markup shape this parser doesn't know about.
 */

/** Elements that either execute or reach out to the network on their own. */
const FORBIDDEN_TAGS = ['script', 'iframe', 'object', 'embed', 'applet', 'base', 'meta', 'link', 'form']

/** Attributes carrying a URL that may need rewriting or blocking. */
const URL_ATTRIBUTES = ['src', 'href', 'srcset', 'poster', 'background', 'action', 'data']

export interface PreparedEmail {
  /** A complete document, ready for an iframe `srcdoc`. */
  html: string
  /** True when at least one remote resource was withheld. */
  blockedRemoteContent: boolean
}

export interface PrepareOptions {
  /** Resolves a `cid:` reference to a URL this app serves. */
  resolveCid: (contentId: string) => string
  /** When false, remote images are withheld until the reader asks for them. */
  showRemoteImages: boolean
}

/**
 * Rewrites a mail HTML body into a self-contained, script-free document.
 *
 * `cid:` references are pointed at the part endpoint so inline images render;
 * remote images are held back by default, because loading them tells the sender
 * their mail was opened.
 */
export function prepareEmailHtml(rawHtml: string, options: PrepareOptions): PreparedEmail {
  const doc = new DOMParser().parseFromString(rawHtml, 'text/html')
  let blockedRemoteContent = false

  for (const tag of FORBIDDEN_TAGS) {
    for (const node of Array.from(doc.querySelectorAll(tag))) node.remove()
  }

  for (const element of Array.from(doc.querySelectorAll('*'))) {
    for (const attr of Array.from(element.attributes)) {
      const name = attr.name.toLowerCase()

      // Inline event handlers are inert without allow-scripts, but they are
      // also never anything but an attack, so they go.
      if (name.startsWith('on')) {
        element.removeAttribute(attr.name)
        continue
      }

      if (name === 'style') {
        const { value, blocked } = rewriteStyle(attr.value, options)
        if (blocked) blockedRemoteContent = true
        element.setAttribute('style', value)
        continue
      }

      if (!URL_ATTRIBUTES.includes(name)) continue

      const url = attr.value.trim()
      if (isDangerousUrl(url)) {
        element.removeAttribute(attr.name)
        continue
      }

      if (url.toLowerCase().startsWith('cid:')) {
        element.setAttribute(attr.name, options.resolveCid(decodeCid(url.slice(4))))
        continue
      }

      // href stays put even while images are blocked: a link is only followed
      // when the reader clicks it, so it leaks nothing on open.
      if (!options.showRemoteImages && isRemoteUrl(url) && name !== 'href') {
        blockedRemoteContent = true
        element.removeAttribute(attr.name)
        element.setAttribute(`data-blocked-${name}`, url)
      }
    }
  }

  // <style> blocks can pull in images and fonts too.
  for (const style of Array.from(doc.querySelectorAll('style'))) {
    const { value, blocked } = rewriteStyle(style.textContent ?? '', options)
    if (blocked) blockedRemoteContent = true
    style.textContent = value
  }

  // Links open in a new tab: the iframe has no allow-top-navigation, so an
  // unmarked link would silently do nothing when clicked.
  for (const anchor of Array.from(doc.querySelectorAll('a[href]'))) {
    anchor.setAttribute('target', '_blank')
    anchor.setAttribute('rel', 'noopener noreferrer external')
  }

  return {
    html: wrapDocument(doc.body.innerHTML, options.showRemoteImages),
    blockedRemoteContent,
  }
}

/** Rewrites `url(...)` inside a style attribute or `<style>` block. */
function rewriteStyle(css: string, options: PrepareOptions): { value: string; blocked: boolean } {
  let blocked = false
  const value = css.replace(/url\(\s*(['"]?)([^'")]+)\1\s*\)/gi, (match, _quote, rawUrl: string) => {
    const url = rawUrl.trim()
    if (isDangerousUrl(url)) return 'none'
    if (url.toLowerCase().startsWith('cid:')) {
      return `url("${options.resolveCid(decodeCid(url.slice(4)))}")`
    }
    if (!options.showRemoteImages && isRemoteUrl(url)) {
      blocked = true
      return 'none'
    }
    return match
  })
  return { value, blocked }
}

/** A cid: reference may be percent-encoded; a malformed one is used as-is. */
function decodeCid(raw: string): string {
  try {
    return decodeURIComponent(raw)
  } catch {
    return raw
  }
}

function isDangerousUrl(url: string): boolean {
  // Whitespace and control characters go first: "java\nscript:" and
  // "java\tscript:" are both real evasions that browsers still parse as a
  // scheme. data:text/html is excluded because it would be a whole document.
  // eslint-disable-next-line no-control-regex -- matching them is the point
  const normalized = url.replace(/[\x00-\x20]/g, '').toLowerCase()
  return (
    normalized.startsWith('javascript:') ||
    normalized.startsWith('vbscript:') ||
    normalized.startsWith('data:text/html')
  )
}

function isRemoteUrl(url: string): boolean {
  return /^(https?:)?\/\//i.test(url)
}

/**
 * Wraps the sanitized fragment in a document with its own CSP.
 *
 * The mail is always rendered dark-on-white regardless of app theme: email HTML
 * hardcodes its own colours against an assumed white page, so inheriting a dark
 * background produces black-on-black text often enough to be useless.
 */
function wrapDocument(bodyHtml: string, showRemoteImages: boolean): string {
  const imgSrc = showRemoteImages ? "'self' data: https: http:" : "'self' data:"
  const csp = [
    "default-src 'none'",
    `img-src ${imgSrc}`,
    "style-src 'unsafe-inline'",
    'font-src data:',
    "media-src 'none'",
  ].join('; ')

  return `<!doctype html>
<html><head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<base target="_blank">
<style>
  html, body { margin: 0; padding: 0; background: #ffffff; color: #111827; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    font-size: 14px; line-height: 1.5;
    padding: 4px 2px 16px;
    overflow-wrap: break-word; word-break: break-word;
  }
  img, video { max-width: 100%; height: auto; }
  table { max-width: 100%; }
  a { color: #1d4ed8; }
  blockquote { margin: 0 0 0 8px; padding-left: 12px; border-left: 3px solid #d1d5db; color: #4b5563; }
  /* Leave a visible gap where a withheld image was, so the layout doesn't
     collapse into something that reads as a rendering bug. */
  [data-blocked-src], [data-blocked-srcset], [data-blocked-background] {
    display: inline-block; min-width: 12px; min-height: 12px;
    border: 1px dashed #d1d5db; border-radius: 3px; background: #f9fafb;
  }
</style>
</head><body>${bodyHtml}</body></html>`
}

/**
 * Renders a plain-text body as HTML, turning bare URLs into links so a
 * text-only message is as usable as an HTML one.
 */
export function plainTextToHtml(text: string): string {
  const escaped = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')

  const linked = escaped.replace(/(https?:\/\/[^\s<>"]+)/g, (url) => {
    // Trailing punctuation is almost always sentence punctuation, not URL.
    const trimmed = url.replace(/[.,;:!?)\]]+$/, '')
    const tail = url.slice(trimmed.length)
    return `<a href="${trimmed}">${trimmed}</a>${tail}`
  })

  return `<pre style="white-space: pre-wrap; font-family: inherit; margin: 0;">${linked}</pre>`
}

/** Human-readable byte size for an attachment chip. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** exponent
  return `${value >= 10 || exponent === 0 ? Math.round(value) : value.toFixed(1)} ${units[exponent]}`
}
