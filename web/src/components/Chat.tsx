import { useEffect, useRef, useState } from 'preact/hooks'
import { chat, settings, you } from '../store'
import { socket } from '../socket'
import { Icon } from './Icons'
import { REACTIONS, MORE_REACTIONS, colorFor } from '../util'

const VISIBLE = 4
const IDLE_FADE_MS = 12000

// Floating chat. The panel itself is transparent; only bubbles have a
// background. Shows the last four messages, older ones fade, and the whole
// stack fades after a quiet spell unless you hover or type.
export function Chat({ compact = false, open = true }: { compact?: boolean; open?: boolean }) {
  const [text, setText] = useState('')
  const [idle, setIdle] = useState(false)
  const [hover, setHover] = useState(false)
  const [focus, setFocus] = useState(false)
  const [more, setMore] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const msgs = chat.value
  const me = you.value?.id

  useEffect(() => {
    setIdle(false)
    const t = setTimeout(() => setIdle(true), IDLE_FADE_MS)
    if (compact && list.current) list.current.scrollTop = list.current.scrollHeight
    return () => clearTimeout(t)
  }, [msgs.length])

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
  const shown = !chatOn ? [] : compact ? msgs : msgs.slice(-VISIBLE)
  const faded = !compact && idle && !hover && !focus

  if (!open && !compact) return null

  return (
    <div
      class={`chat ${compact ? 'chat-compact' : 'chat-float'} ${faded ? 'faded' : ''}`}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
    >
      <div class="chat-list" ref={list} aria-live="polite">
        {shown.map((m, i) => {
          const self = m.sid === me
          const age = shown.length - 1 - i
          const opacity = compact ? 1 : age >= VISIBLE - 1 ? 0.5 : 1
          if (m.sys) {
            return (
              <div class="bubble-row sys" key={m.id} style={{ opacity }}>
                <span class="sys-msg">{m.text}</span>
              </div>
            )
          }
          return (
            <div class={`bubble-row ${self ? 'self' : ''}`} key={m.id} style={{ opacity }}>
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
