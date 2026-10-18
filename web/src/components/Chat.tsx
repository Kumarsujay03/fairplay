import { useEffect, useRef, useState } from 'preact/hooks'
import { chat, settings, you } from '../store'
import { socket } from '../socket'
import { Icon } from './Icons'
import { REACTIONS, MORE_REACTIONS, colorFor } from '../util'

const IDLE_FADE_MS = 12000
const NEAR_BOTTOM = 24 // px from the bottom that still counts as "following" the chat

// Floating chat. The panel itself is transparent; only bubbles have a
// background. About four messages fit; scroll up (wheel or swipe, no visible
// scrollbar) to read the whole session. New messages don't pull you down while
// you are reading older ones; a pill offers to jump back. The stack fades after
// a quiet spell unless you hover, type or are scrolled up.
export function Chat({ compact = false, open = true }: { compact?: boolean; open?: boolean }) {
  const [text, setText] = useState('')
  const [idle, setIdle] = useState(false)
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
    const t = setTimeout(() => setIdle(true), IDLE_FADE_MS)
    const added = msgs.length - lastCount.current
    lastCount.current = msgs.length
    const newest = msgs[msgs.length - 1]
    if (following || (newest && newest.sid === me) || added < 0 || added > 20) {
      // Following the chat, or you just sent, or history was replaced: stick to the bottom.
      requestAnimationFrame(() => toBottom(added === 1))
    } else if (added > 0) {
      setUnread((n) => n + added)
    }
    return () => clearTimeout(t)
  }, [msgs.length, msgs[0]?.id])

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

  const react = (emoji: string) => socket.send({ t: 'react', e: emoji })

  const chatOn = you.value?.chat !== false
  const shown = chatOn ? msgs : []
  const faded = !compact && idle && !hover && !focus && following

  if (!open && !compact) return null

  return (
    <div
      class={`chat ${compact ? 'chat-compact' : 'chat-float'} ${faded ? 'faded' : ''}`}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
    >
      <div class={`chat-list ${following ? '' : 'scrolled'}`} ref={list} aria-live="polite" onScroll={onScroll}>
        {shown.map((m) => {
          const self = m.sid === me
          if (m.sys) {
            return (
              <div class="bubble-row sys" key={m.id}>
                <span class="sys-msg">{m.text}</span>
              </div>
            )
          }
          return (
            <div class={`bubble-row ${self ? 'self' : ''}`} key={m.id}>
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
          maxLength={500}
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
        <button type="submit" class="send" aria-label="Send" disabled={!text.trim()}>
          <Icon name="send" size={16} />
        </button>
      </form>
      )}
      {settings.value.reactions && (
        <div class="reaction-bar">
          {(more ? [...REACTIONS, ...MORE_REACTIONS] : REACTIONS).map((e) => (
            <button key={e} class="chip" onClick={() => react(e)} aria-label={`React ${e}`}>
              {e}
            </button>
          ))}
          <button class="chip more" onClick={() => setMore(!more)} aria-label="More reactions">
            {more ? '−' : '+'}
          </button>
        </div>
      )}
    </div>
  )
}
