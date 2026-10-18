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
      const x0 = Math.random() * 40 - 20
      const x1 = x0 + Math.random() * 80 - 40
      const r = Math.random() * 30 - 15
      // Rise from just left of the chat column (desktop) or the right side of
      // the video (phone), so they never cover the messages.
      const compact = !!el.closest('.compact')
      n.style.left = compact ? `${65 + Math.random() * 20}%` : `calc(100% - ${360 + Math.random() * 90}px)`
      n.getAnimations().forEach((a) => a.cancel())
      n.animate(
        [
          { transform: `translate(${x0}px, 0) scale(0.6)`, opacity: 0 },
          { transform: `translate(${x0}px, -40px) scale(1.15)`, opacity: 1, offset: 0.12 },
          { transform: `translate(${(x0 + x1) / 2}px, -200px) rotate(${r}deg) scale(1)`, opacity: 0.9, offset: 0.6 },
          { transform: `translate(${x1}px, -340px) rotate(${r * 2}deg) scale(0.9)`, opacity: 0 },
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
