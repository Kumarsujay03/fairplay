import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { get } from '../api'
import { emojiChar, emojiURL, recentEmoji } from '../util'

interface Entry {
  c: string // codepoint
  n: string // name
  k: string // category
}

let indexPromise: Promise<Entry[]> | null = null
function loadIndex(): Promise<Entry[]> {
  if (!indexPromise) indexPromise = get<Entry[]>('/api/emoji').catch((e) => ((indexPromise = null), Promise.reject(e)))
  return indexPromise
}

const CATEGORIES: [string, string][] = [
  ['Smileys and emotions', '1f600'],
  ['People', '1f44b'],
  ['Animals and nature', '1f43e'],
  ['Food and drink', '1f355'],
  ['Travel and places', '1f680'],
  ['Activities and events', '1f389'],
  ['Objects', '1f4a1'],
  ['Symbols', '2764_fe0f'],
  ['Flags', '1f3c1'],
]

/** Every animated Noto emoji, searchable, with your recent ones on top. */
export function EmojiPicker({ onPick, onClose }: { onPick: (code: string) => void; onClose: () => void }) {
  const [all, setAll] = useState<Entry[] | null>(null)
  const [err, setErr] = useState('')
  const [q, setQ] = useState('')
  const [cat, setCat] = useState(CATEGORIES[0][0])
  const [preview, setPreview] = useState<string | null>(null)
  const box = useRef<HTMLDivElement>(null)
  const recents = recentEmoji()

  useEffect(() => {
    loadIndex().then(setAll).catch(() => setErr('Could not load emoji'))
    const onDown = (e: PointerEvent) => {
      if (box.current && !box.current.contains(e.target as Node) && !(e.target as HTMLElement).closest('.chip.more')) onClose()
    }
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    addEventListener('pointerdown', onDown)
    addEventListener('keydown', onKey)
    return () => {
      removeEventListener('pointerdown', onDown)
      removeEventListener('keydown', onKey)
    }
  }, [])

  const shown = useMemo(() => {
    if (!all) return []
    const s = q.trim().toLowerCase().replace(/^:|:$/g, '')
    if (s) return all.filter((e) => e.n.includes(s) || e.k.toLowerCase().includes(s)).slice(0, 200)
    return all.filter((e) => e.k === cat)
  }, [all, q, cat])

  return (
    <div class="emoji-picker" ref={box} role="dialog" aria-label="Choose a reaction">
      <div class="emoji-head">
        <input
          class="emoji-search"
          placeholder="Search emoji"
          value={q}
          autoFocus
          onInput={(e) => setQ((e.target as HTMLInputElement).value)}
          onKeyDown={(e) => e.stopPropagation()}
          aria-label="Search emoji"
        />
        <div class="emoji-preview" aria-hidden="true">
          {preview ? <img src={emojiURL(preview)} alt="" width={40} height={40} /> : <span>{emojiChar('1f525')}</span>}
        </div>
      </div>
      {!q && (
        <div class="emoji-tabs" role="tablist">
          {CATEGORIES.map(([name, icon]) => (
            <button key={name} role="tab" aria-selected={cat === name} class={cat === name ? 'on' : ''} title={name} onClick={() => setCat(name)}>
              {emojiChar(icon)}
            </button>
          ))}
        </div>
      )}
      <div class="emoji-grid-wrap">
        {!q && recents.length > 0 && (
          <>
            <div class="emoji-section">Recent</div>
            <div class="emoji-grid">
              {recents.slice(0, 16).map((c) => (
                <Cell key={'r' + c} code={c} name="" onPick={onPick} onHover={setPreview} />
              ))}
            </div>
          </>
        )}
        {!q && <div class="emoji-section">{cat}</div>}
        {err && <div class="caption muted pad">{err}</div>}
        {!all && !err && <div class="caption muted pad">Loading…</div>}
        <div class="emoji-grid">
          {shown.map((e) => (
            <Cell key={e.c} code={e.c} name={e.n} onPick={onPick} onHover={setPreview} />
          ))}
        </div>
        {all && q && shown.length === 0 && <div class="caption muted pad">No emoji match “{q}”</div>}
      </div>
      <div class="emoji-credit">Animated emoji: Noto, by Google, CC BY 4.0</div>
    </div>
  )
}

function Cell({ code, name, onPick, onHover }: { code: string; name: string; onPick: (c: string) => void; onHover: (c: string | null) => void }) {
  const timer = useRef(0)
  return (
    <button
      class="emoji-cell"
      title={name}
      aria-label={name || code}
      onClick={() => onPick(code)}
      // Show the animation after a short hover, so sweeping across the grid
      // doesn't make the host download every emoji.
      onPointerEnter={() => (timer.current = window.setTimeout(() => onHover(code), 350))}
      onPointerLeave={() => clearTimeout(timer.current)}
    >
      {emojiChar(code)}
    </button>
  )
}
