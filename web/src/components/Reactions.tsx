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
      const rise = Math.min(360, Math.max(180, top - 30))
      const size = 0.9 + Math.random() * 0.45 // a little variety in size
      const sway = 14 + Math.random() * 16 // px left and right while rising
      const turns = 1.2 + Math.random() * 0.8 // how many sways on the way up
      const phase = Math.random() * Math.PI * 2
      const tilt = 8 + Math.random() * 8
      n.style.left = `${cx}px`
      n.style.top = `${top}px`
      n.getAnimations().forEach((a) => a.cancel())

      // Pop in with a bounce, then float up swaying and wobbling, and fade out.
      const frames: Keyframe[] = [
        { transform: `translate(${x0}px, 12px) scale(${0.2 * size}) rotate(0deg)`, opacity: 0, offset: 0 },
        { transform: `translate(${x0}px, -14px) scale(${1.45 * size}) rotate(${-tilt}deg)`, opacity: 1, offset: 0.08 },
        { transform: `translate(${x0}px, -22px) scale(${0.92 * size}) rotate(${tilt / 2}deg)`, opacity: 1, offset: 0.15 },
        { transform: `translate(${x0}px, -30px) scale(${1.08 * size}) rotate(0deg)`, opacity: 1, offset: 0.22 },
      ]
      const steps = 8
      for (let i = 1; i <= steps; i++) {
        const t = i / steps
        const y = -30 - (rise - 30) * t
        // Offset by the starting value so the sway begins exactly where the pop ended.
        const w = phase + t * turns * Math.PI * 2
        const x = x0 + (Math.sin(w) - Math.sin(phase)) * sway
        const r = (Math.sin(w + 0.6) - Math.sin(phase + 0.6)) * tilt
        frames.push({
          transform: `translate(${x.toFixed(1)}px, ${y.toFixed(1)}px) scale(${(size * (1 - 0.15 * t)).toFixed(3)}) rotate(${r.toFixed(1)}deg)`,
          opacity: t < 0.7 ? 1 : 1 - (t - 0.7) / 0.3,
          offset: 0.22 + 0.78 * t,
        })
      }
      n.animate(frames, { duration: 3000 + Math.random() * 900, easing: 'linear', fill: 'forwards' })
    })
    return () => {
      off()
      el.innerHTML = ''
    }
  }, [])
  return <div class="reaction-layer" ref={layer} aria-hidden="true" />
}
