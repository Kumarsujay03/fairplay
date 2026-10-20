import { useEffect, useRef, useState } from 'preact/hooks'
import { chat, settings, you } from '../store'
import { socket } from '../socket'
import { Icon } from './Icons'
import { DEFAULT_REACTIONS, colorFor, emojiChar, emojiURL, recentEmoji, rememberEmoji } from '../util'
import { EmojiPicker } from './EmojiPicker'

// How long the bubbles stay after a message: reading time, not a fixed number.
// 4 s plus 60 ms per character of the newest message, between 5 and 12 s, so
// "ok" is gone in about 5 s and a full 200-character line stays about 12 s.
const fadeAfter = (text: string) => Math.min(12000, Math.max(5000, 4000 + 60 * text.length))
// Touchscreens fire a "mouse enter" on the first tap and never a "leave", and
// keep the composer focused after a send, so neither may hold the chat open there.
const touch = typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches
export const MAX_CHAT = 200 // characters; the server enforces the same limit
const NEAR_BOTTOM = 24 // px from the bottom that still counts as "following" the chat

// Floating chat. The panel itself is transparent; only bubbles have a
// background. About four messages fit; scroll up (wheel or swipe, no visible
// scrollbar) to read the whole session. New messages don't pull you down while
// you are reading older ones; a pill offers to jump back.
//
// What shows when: a new message brings the bubbles up for its reading time, and
// they are also up whenever the player's controls are (uiOn). The text box,
// send button and reaction bar appear only when the pointer is over the chat
// area or the box has focus; on a touchscreen they come up with the controls.
// A reaction never brings the chat up: it floats over the picture on its own.
export function Chat({ compact = false, open = true, uiOn = true }: { compact?: boolean; open?: boolean; uiOn?: boolean }) {
  const [text, setText] = useState('')
  const [idle, setIdle] = useState(false)
  // Messages before this index have been on screen once already. When the
  // chat is quiet and a new one arrives, only the new bubbles show; the older
  // ones stay hidden until the controls or the pointer bring the stack up.
  const [seenBefore, setSeenBefore] = useState(() => chat.value.length)
  const [hover, setHover] = useState(false)
  const [focus, setFocus] = useState(false)
  const [more, setMore] = useState(false)
  const [following, setFollowing] = useState(true)
  const [unread, setUnread] = useState(0)
  const lastCount = useRef(0)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const msgs = chat.value
  const me = you.value?.id

  const toBottom = (smooth = false) => {
    const el = list.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
    setFollowing(true)
    setUnread(0)
  }

  useEffect(() => {
    setIdle(false)
    const newest = msgs[msgs.length - 1]
    const t = setTimeout(() => setIdle(true), fadeAfter(newest?.text || ''))
    const added = msgs.length - lastCount.current
    lastCount.current = msgs.length
    if (following || (newest && newest.sid === me) || added < 0 || added > 20) {
      // Following the chat, or you just sent, or history was replaced: stick to the bottom.
      requestAnimationFrame(() => toBottom(added === 1))
    } else if (added > 0) {
      setUnread((n) => n + added)
    }
    return () => clearTimeout(t)
  }, [msgs.length, msgs[0]?.id])

  useEffect(() => {
    if (idle) setSeenBefore(msgs.length)
  }, [idle])

  // While following, stay pinned to the newest message whenever the list
  // changes height: older bubbles coming back with the controls, or the text
  // box unfolding, must not leave the newest one cut off at the bottom.
  useEffect(() => {
    const el = list.current
    if (el && following) el.scrollTop = el.scrollHeight
  })

  const onScroll = () => {
    const el = list.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM
    setFollowing(atBottom)
    if (atBottom) setUnread(0)
    setIdle(false)
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Enter' && document.activeElement === document.body) {
        e.preventDefault()
        input.current?.focus()
      }
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [])

  const send = (e?: Event) => {
    e?.preventDefault()
    const t = text.trim()
    if (!t) return
    socket.send({ t: 'chat', text: t })
    setText('')
  }

  const react = (code: string) => {
    socket.send({ t: 'react', e: code })
    rememberEmoji(code)
    setBar(barEmoji())
  }
  const [bar, setBar] = useState(barEmoji)
  // Warm the browser cache so the first reaction animates instantly.
  useEffect(() => {
    for (const c of DEFAULT_REACTIONS) new Image().src = emojiURL(c)
  }, [])

  const chatOn = you.value?.chat !== false
  const shown = chatOn ? msgs : []
  const pointer = (hover && !touch) || focus
  const faded = !compact && idle && !pointer && !uiOn && following
  const composerHidden = !compact && !pointer && !(touch && uiOn)
  const quiet = !compact && !pointer && !uiOn // only bubbles newer than the last fade are shown

  if (!open && !compact) return null

  return (
    <div
      class={`chat ${compact ? 'chat-compact' : 'chat-float'} ${faded ? 'faded' : ''} ${composerHidden ? 'composer-hidden' : ''} ${quiet ? 'quiet' : ''}`}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
    >
      <div class={`chat-list ${following ? '' : 'scrolled'}`} ref={list} aria-live="polite" onScroll={onScroll}>
        {shown.map((m, i) => {
          const self = m.sid === me
          const seen = i < seenBefore ? 'seen' : ''
          if (m.sys) {
            return (
              <div class={`bubble-row sys ${seen}`} key={m.id}>
                <span class="sys-msg">{m.text}</span>
              </div>
            )
          }
          return (
            <div class={`bubble-row ${self ? 'self' : ''} ${seen}`} key={m.id}>
              <div class={`bubble ${self ? 'self' : 'other'}`}>
                <div class="bubble-name" style={self ? undefined : { color: colorFor(m.sid) }}>
                  {self ? 'You' : m.name}
                </div>
                <div class="bubble-text">{m.text}</div>
              </div>
            </div>
          )
        })}
      </div>
      {unread > 0 && (
        <button class="new-pill" onClick={() => toBottom(true)}>
          {unread} new message{unread === 1 ? '' : 's'} ↓
        </button>
      )}
      {!chatOn && <div class="chat-off caption">The host has turned off chat for you</div>}
      {chatOn && (
      <form class="composer" onSubmit={send}>
        <input
          ref={input}
          value={text}
          maxLength={MAX_CHAT}
          placeholder="Say something…"
          aria-label="Chat message"
          onInput={(e) => setText((e.target as HTMLInputElement).value)}
          onFocus={() => setFocus(true)}
          onBlur={() => setFocus(false)}
          onKeyDown={(e) => {
            e.stopPropagation() // keep player shortcuts out of the text box
            if (e.key === 'Escape') (e.target as HTMLInputElement).blur()
          }}
        />
        {text.length > MAX_CHAT - 40 && <span class={`chat-count ${text.length >= MAX_CHAT ? 'full' : ''}`}>{MAX_CHAT - text.length}</span>}
        <button type="submit" class="send" aria-label="Send" disabled={!text.trim()}>
          <Icon name="send" size={16} />
        </button>
      </form>
      )}
      {settings.value.reactions && (
        <div class="reaction-bar">
          {bar.map((c) => (
            <Chip key={c} code={c} onClick={() => react(c)} />
          ))}
          <button class={`chip more ${more ? 'on' : ''}`} onClick={() => setMore(!more)} aria-label="All emoji" title="All emoji">
            {more ? '×' : '+'}
          </button>
          {more && <EmojiPicker onPick={react} onClose={() => setMore(false)} />}
        </div>
      )}
    </div>
  )
}

/** Your 6 most recent reactions, topped up with the defaults. */
function barEmoji(): string[] {
  const out: string[] = []
  for (const c of [...recentEmoji(), ...DEFAULT_REACTIONS]) {
    if (!out.includes(c)) out.push(c)
    if (out.length === 6) break
  }
  return out
}

/** A reaction button that plays its animation while hovered. */
function Chip({ code, onClick }: { code: string; onClick: () => void }) {
  const [hover, setHover] = useState(false)
  return (
    <button
      class="chip"
      onClick={onClick}
      onPointerEnter={() => setHover(true)}
      onPointerLeave={() => setHover(false)}
      aria-label={`React ${emojiChar(code)}`}
    >
      {hover ? <img src={emojiURL(code)} alt={emojiChar(code)} width={26} height={26} draggable={false} /> : emojiChar(code)}
    </button>
  )
}
