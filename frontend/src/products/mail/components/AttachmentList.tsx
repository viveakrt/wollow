import { FileText, Image as ImageIcon, Paperclip, Sheet } from 'lucide-react'
import { formatBytes } from '../lib/emailHtml'
import type { Attachment } from '../types'

interface AttachmentListProps {
  attachments: Attachment[]
  /** Builds the download URL for one part. */
  urlFor: (attachment: Attachment, download: boolean) => string
}

/**
 * The downloadable attachments on a message.
 *
 * Inline parts (the images an HTML body references with `cid:`) are filtered
 * out: they are already rendered in the body, and listing them again turns a
 * one-attachment invoice mail into a wall of tracking pixels and logo slices.
 * An inline part with a real filename is kept, since senders routinely attach
 * genuine documents with `Content-Disposition: inline`.
 */
export function AttachmentList({ attachments, urlFor }: AttachmentListProps) {
  const visible = attachments.filter(
    (a) => !a.inline || (!a.contentId && !a.fileName.startsWith('part-')),
  )
  if (visible.length === 0) return null

  return (
    <div className="border-t border-[var(--color-border)] px-6 py-4">
      <div className="mb-2 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-[var(--color-text-subtle)]">
        <Paperclip size={13} />
        {visible.length} attachment{visible.length === 1 ? '' : 's'}
      </div>
      <ul className="flex flex-wrap gap-2">
        {visible.map((attachment) => (
          <li key={attachment.partId}>
            <a
              href={urlFor(attachment, true)}
              download={attachment.fileName}
              className="flex max-w-xs items-center gap-2.5 rounded-lg border border-[var(--color-border)] px-3 py-2 transition hover:border-[var(--color-border-strong)] hover:bg-[var(--color-hover)]"
            >
              <AttachmentIcon contentType={attachment.contentType} />
              <span className="min-w-0">
                <span className="block truncate text-sm text-[var(--color-text)]">{attachment.fileName}</span>
                <span className="block text-xs text-[var(--color-text-subtle)]">
                  {formatBytes(attachment.size)}
                </span>
              </span>
            </a>
          </li>
        ))}
      </ul>
    </div>
  )
}

function AttachmentIcon({ contentType }: { contentType: string }) {
  const props = { size: 18, className: 'shrink-0 text-[var(--color-text-muted)]' }
  if (contentType.startsWith('image/')) return <ImageIcon {...props} />
  if (contentType.includes('spreadsheet') || contentType.includes('excel') || contentType.includes('csv')) {
    return <Sheet {...props} />
  }
  return <FileText {...props} />
}
