import { useEffect, useRef } from 'preact/hooks'
import { socket } from '../socket'

const POOL = 24

// Floating reactions. A fixed pool of nodes is reused and animated with the
// Web Animations API on transform and opacity only, so even a reaction storm
// never triggers layout.
export function Reactions() {
  const layer = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = layer.current!
    const nodes: HTMLSpanElement[] = []
    for (let i = 0; i < POOL; i++) {
      const s = document.createElement('span')
      s.className = 'float-emoji'
      el.appendChild(s)
      nodes.push(s)
    }
    let next = 0
    const off = socket.on((m) => {
      if (m.t !== 'react') return
      const n = nodes[next]
      next = (next + 1) % POOL
      n.textContent = m.e
      n.title = m.name
      // Rise from the middle of the chat column, starting just above the
      // message box, so reactions float up over the chat. On phones (chat
      // below the video) and when chat is hidden, rise from the video instead.
      const box = el.getBoundingClientRect()
      const dock = el.parentElement?.querySelector('.chat-dock') as HTMLElement | null
      const composer = dock?.querySelector('.composer, .chat-off') as HTMLElement | null
      const anchor = composer?.getBoundingClientRect()
      const col = dock?.getBoundingClientRect()
      let cx: number, top: number, spread: number
      if (col && anchor && col.width > 40 && anchor.height > 0) {
        cx = col.left + col.width / 2 - box.left
        top = anchor.top - box.top - 36
        spread = Math.min(70, col.width / 2 - 30)
      } else {
        cx = box.width * (el.closest('.compact') ? 0.5 : 0.8)
        top = box.height - (el.closest('.compact') ? 70 : 180)
        spread = 60
      }
      const x0 = (Math.random() * 2 - 1) * spread
      const x1 = x0 + (Math.random() * 2 - 1) * 30
      const r = Math.random() * 30 - 15
      const rise = Math.min(340, Math.max(160, top - 40))
      n.style.left = `${cx}px`
      n.style.top = `${top}px`
      n.getAnimations().forEach((a) => a.cancel())
      n.animate(
        [
          { transform: `translate(${x0}px, 0) scale(0.6)`, opacity: 0 },
          { transform: `translate(${x0}px, -40px) scale(1.15)`, opacity: 1, offset: 0.12 },
          { transform: `translate(${(x0 + x1) / 2}px, ${-rise * 0.6}px) rotate(${r}deg) scale(1)`, opacity: 0.9, offset: 0.6 },
          { transform: `translate(${x1}px, ${-rise}px) rotate(${r * 2}deg) scale(0.9)`, opacity: 0 },
        ],
        { duration: 2600 + Math.random() * 600, easing: 'cubic-bezier(.2,.7,.3,1)', fill: 'forwards' },
      )
    })
    return () => {
      off()
      el.innerHTML = ''
    }
  }, [])
  return <div class="reaction-layer" ref={layer} aria-hidden="true" />
}
