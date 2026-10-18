import { useEffect, useState } from 'preact/hooks'
import { Icon } from './Icons'
import { copy } from '../util'
import * as S from '../store'

/** Draws a QR code as one SVG path (no injected markup, CSP friendly). */
function QRCode({ text, size = 240 }: { text: string; size?: number }) {
  const [path, setPath] = useState<{ d: string; n: number } | null>(null)
  useEffect(() => {
    let alive = true
    import('qrcode-generator').then(({ default: qrcode }) => {
      if (!alive) return
      const qr = qrcode(0, 'M')
      qr.addData(text)
      qr.make()
      const n = qr.getModuleCount()
      let d = ''
      for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) if (qr.isDark(r, c)) d += `M${c} ${r}h1v1h-1z`
      setPath({ d, n })
    })
    return () => {
      alive = false
    }
  }, [text])
  if (!path) return <div class="qr-box" style={{ width: size, height: size }} />
  const q = 2 // quiet zone in modules
  return (
    <svg class="qr-box" width={size} height={size} viewBox={`${-q} ${-q} ${path.n + 2 * q} ${path.n + 2 * q}`} shape-rendering="crispEdges" role="img" aria-label="QR code">
      <rect x={-q} y={-q} width={path.n + 2 * q} height={path.n + 2 * q} fill="#fff" />
      <path d={path.d} fill="#0b0d12" />
    </svg>
  )
}

/** Invite QR codes: scan with a phone camera to open the exact link. */
export function InviteQR({ label, links, onClose }: { label: string; links: { kind: string; url: string }[]; onClose: () => void }) {
  const [i, setI] = useState(0)
  const cur = links[i]
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    addEventListener('keydown', k)
    return () => removeEventListener('keydown', k)
  }, [])
  return (
    <div class="modal-back" onClick={onClose}>
      <div class="modal qr-modal" role="dialog" aria-label={`QR code for ${label}`} onClick={(e) => e.stopPropagation()}>
        <div class="card-head">
          <div class="h18">{label}’s invite</div>
          <button class="icon-btn sm plain" onClick={onClose} aria-label="Close">
            <Icon name="x" size={18} />
          </button>
        </div>
        {links.length > 1 && (
          <div class="seg">
            {links.map((l, n) => (
              <button key={l.kind} class={n === i ? 'on' : ''} onClick={() => setI(n)}>
                {l.kind}
              </button>
            ))}
          </div>
        )}
        <QRCode text={cur.url} />
        <div class="caption dim center-text">
          Scan with the phone’s camera. It opens the exact link, port included.
          {cur.kind === 'Wi-Fi' && ' The phone must be on the same Wi-Fi.'}
        </div>
        <div class="url-box">
          <span class="mono caption ellipsis grow">{cur.url.replace(/#t=.*/, '#t=…')}</span>
          <button class="btn btn-sm btn-ghost" onClick={async () => (await copy(cur.url)) && S.showToast('Link copied')}>
            <Icon name="copy" size={14} /> Copy
          </button>
        </div>
      </div>
    </div>
  )
}
