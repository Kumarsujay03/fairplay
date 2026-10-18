import { useEffect, useRef } from 'preact/hooks'
import { socket } from '../socket'
import { emojiChar, emojiURL } from '../util'

const MAX_LIVE = 10 // simultaneous reactions on screen
const BURST = 8 // mini emoji that spray out when one lands

// Telegram-style reactions: the animated emoji pops in big with a springy
// bounce and a burst of mini emoji around it, plays its own animation, then
// drifts up over the chat and fades. Only transform and opacity animate, so a
// storm of reactions never triggers layout.
export function Reactions() {
  const layer = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = layer.current!
    const live: HTMLElement[] = []

    const origin = () => {
      const box = el.getBoundingClientRect()
      const dock = el.parentElement?.querySelector('.chat-dock') as HTMLElement | null
      const composer = dock?.querySelector('.composer, .chat-off') as HTMLElement | null
      const a = composer?.getBoundingClientRect()
      const col = dock?.getBoundingClientRect()
      if (col && a && col.width > 40 && a.height > 0) {
        return { x: col.left + col.width / 2 - box.left, y: a.top - box.top - 70, spread: Math.min(60, col.width / 2 - 50), room: a.top - box.top - 80 }
      }
      const compact = !!el.closest('.compact')
      return { x: box.width * (compact ? 0.5 : 0.8), y: box.height - (compact ? 90 : 220), spread: 60, room: box.height - 140 }
    }

    const off = socket.on((m) => {
      if (m.t !== 'react' || typeof m.e !== 'string') return
      const o = origin()
      const x = o.x + (Math.random() * 2 - 1) * o.spread
      const wrap = document.createElement('div')
      wrap.className = 'react-pop'
      wrap.style.left = `${x}px`
      wrap.style.top = `${o.y}px`
      wrap.title = m.name || ''

      // The plain emoji shows at once; the animation replaces it as soon as it
      // has loaded (the very first use of a rare emoji takes the host a moment).
      // If it never loads (offline host), the plain emoji simply stays.
      const glyph = document.createElement('span')
      glyph.className = 'react-glyph'
      glyph.textContent = emojiChar(m.e)
      const img = document.createElement('img')
      img.alt = ''
      img.width = 72
      img.height = 72
      img.draggable = false
      img.onload = () => wrap.classList.add('ready')
      img.src = emojiURL(m.e)
      if (img.complete && img.naturalWidth) wrap.classList.add('ready')
      wrap.append(glyph, img)
      el.appendChild(wrap)
      live.push(wrap)
      while (live.length > MAX_LIVE) live.shift()?.remove()

      // 1. Spring pop.
      const drift = (Math.random() * 2 - 1) * 40
      const rise = Math.max(160, Math.min(300, o.room))
      const anim = wrap.animate(
        [
          { transform: 'translate(-50%, -50%) scale(0.1)', opacity: 0, offset: 0 },
          { transform: 'translate(-50%, -50%) scale(1.35)', opacity: 1, offset: 0.1 },
          { transform: 'translate(-50%, -50%) scale(0.88)', opacity: 1, offset: 0.17 },
          { transform: 'translate(-50%, -50%) scale(1.06)', opacity: 1, offset: 0.23 },
          { transform: 'translate(-50%, -50%) scale(1)', opacity: 1, offset: 0.3 },
          // 2. Hold while the emoji plays, then drift up and fade.
          { transform: `translate(calc(-50% + ${drift * 0.3}px), calc(-50% - ${rise * 0.15}px)) scale(1)`, opacity: 1, offset: 0.5 },
          { transform: `translate(calc(-50% + ${drift}px), calc(-50% - ${rise}px)) scale(0.7)`, opacity: 0, offset: 1 },
        ],
        { duration: 3200, easing: 'cubic-bezier(.25,.8,.35,1)', fill: 'forwards' },
      )
      anim.onfinish = () => {
        wrap.remove()
        const i = live.indexOf(wrap)
        if (i >= 0) live.splice(i, 1)
      }

      // 3. Burst of mini emoji around the landing spot.
      for (let i = 0; i < BURST; i++) {
        const p = document.createElement('span')
        p.className = 'react-spark'
        p.textContent = emojiChar(m.e)
        p.style.left = `${x}px`
        p.style.top = `${o.y}px`
        el.appendChild(p)
        const angle = (i / BURST) * Math.PI * 2 + Math.random() * 0.5
        const dist = 50 + Math.random() * 40
        const dx = Math.cos(angle) * dist
        const dy = Math.sin(angle) * dist
        const a = p.animate(
          [
            { transform: 'translate(-50%, -50%) scale(0.2)', opacity: 0 },
            { transform: `translate(calc(-50% + ${dx * 0.6}px), calc(-50% + ${dy * 0.6}px)) scale(1)`, opacity: 1, offset: 0.35 },
            { transform: `translate(calc(-50% + ${dx}px), calc(-50% + ${dy + 14}px)) scale(0.4) rotate(${dx > 0 ? 40 : -40}deg)`, opacity: 0 },
          ],
          { duration: 750 + Math.random() * 250, delay: 60, easing: 'cubic-bezier(.2,.8,.3,1)', fill: 'forwards' },
        )
        a.onfinish = () => p.remove()
      }
    })
    return () => {
      off()
      el.innerHTML = ''
    }
  }, [])
  return <div class="reaction-layer" ref={layer} aria-hidden="true" />
}
