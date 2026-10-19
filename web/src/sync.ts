// SyncEngine keeps one <video> on the room clock.
//
// Every 250 ms it computes where the room is right now (from the anchor and
// the server clock) and corrects the local playhead:
//   drift < tolerance     -> leave it alone
//   drift < 1.5 s         -> nudge playbackRate between 0.95 and 1.05, and
//                            seek if that hasn't worked within 8 s
//   drift >= 1.5 s        -> hard seek, aiming slightly ahead to cover the
//                            time the seek itself takes
// Tolerance grows with network jitter so a shaky link is not corrected
// constantly. In solo mode the engine only reports, it never corrects.
//
// A watchdog calls onStall when the room is playing but this player has
// made no progress for a while (a stream that stopped loading, a decoder
// that hung), so the page can restart loading at the room position instead
// of sitting on "Buffering" while everyone else moves on.

import type { Clock } from './clock'
import type { RoomState } from './store'

const TICK = 250
const HARD = 1.5
const MAX_NUDGE = 0.05
const STALL_MS = 6000
const NUDGE_MAX_MS = 8000
const MAX_SEEK_COST = 1.0

export interface SyncReport {
  target: number
  drift: number // seconds, positive = ahead of the room
  buffering: boolean
}

export class SyncEngine {
  mode: 'follow' | 'solo' = 'follow'
  blocked = false // autoplay was refused; waiting for a tap
  onBlocked: (b: boolean) => void = () => {}
  /** Autoplay with sound was refused, so playback started muted instead. */
  onMutedStart: () => void = () => {}
  onReport: (r: SyncReport) => void = () => {}
  /** No progress while the room plays; count says how many times in a row. */
  onStall: (count: number) => void = () => {}

  private timer = 0
  private startTimer = 0
  private ownAction = 0 // timestamp of our last programmatic pause/play/seek
  private seekCost = 0.25 // learned seconds a seek takes to resume, capped at MAX_SEEK_COST
  private seekAt = 0
  private nudging = false
  private nudgeSince = 0
  private playPending = false
  private autoMuted = false // we muted it to get past the autoplay rules
  private stallSince = 0
  private stallCount = 0
  private lastTime = -1

  constructor(
    private video: HTMLVideoElement,
    private clock: Clock,
    private state: () => RoomState | null,
    private duration: () => number,
  ) {
    video.addEventListener('playing', this.onPlaying)
  }

  start() {
    this.stop()
    this.timer = window.setInterval(() => this.tick(), TICK)
    this.tick()
  }

  stop() {
    clearInterval(this.timer)
    clearTimeout(this.startTimer)
  }

  destroy() {
    this.stop()
    this.video.removeEventListener('playing', this.onPlaying)
  }

  /** True when the last pause/play/seek on the element came from us. */
  isOwnAction() {
    return performance.now() - this.ownAction < 400
  }

  /** Where the room is at this instant. */
  target(): number {
    const s = this.state()
    if (!s) return 0
    const now = this.clock.now()
    let t = !s.playing || now <= s.anchor ? s.pos : s.pos + ((now - s.anchor) / 1000) * s.rate
    const d = this.duration()
    if (d > 0 && t > d) t = d
    return Math.max(0, t)
  }

  follow() {
    this.mode = 'follow'
    this.tick(true)
  }

  solo() {
    this.mode = 'solo'
    this.setRate(1)
    clearTimeout(this.startTimer)
  }

  /** Call from a user gesture after autoplay was blocked. */
  unblock() {
    this.blocked = false
    this.onBlocked(false)
    this.tick(true)
  }

  tick(force = false) {
    const v = this.video
    const s = this.state()
    const buffering = v.readyState < 3 || v.seeking
    this.watchdog(s)
    if (!s || !s.media || v.readyState === 0) {
      this.onReport({ target: 0, drift: 0, buffering })
      return
    }
    const target = this.target()
    const drift = v.currentTime - target
    this.onReport({ target, drift, buffering })
    if (this.mode === 'solo') return

    const now = this.clock.now()
    const scheduled = s.playing && now < s.anchor

    if (!s.playing || scheduled) {
      this.setRate(1)
      if (!v.paused) this.own(() => v.pause())
      if (Math.abs(drift) > 0.08 || force) {
        if (Math.abs(v.currentTime - s.pos) > 0.04) this.seek(s.pos)
      }
      if (scheduled) {
        // Start exactly on the shared tick.
        clearTimeout(this.startTimer)
        this.startTimer = window.setTimeout(() => this.play(), Math.max(0, s.anchor - this.clock.now()))
      }
      return
    }

    if (v.paused && !this.blocked) this.play()

    // Drift tolerance: 80 ms on a calm link, wider with jitter.
    const tol = Math.min(0.4, Math.max(0.08, this.clock.jitter / 1000))
    const abs = Math.abs(drift)
    if (abs >= HARD) {
      this.setRate(1)
      // Let a seek that is still loading finish instead of chasing the room
      // with a new one every second, which restarts the download each time.
      const wait = v.seeking || buffering ? 4000 : 1000
      // Aim ahead by the learned seek cost only when behind. When ahead the
      // data is already here, the seek is quick, and aiming ahead again
      // would land ahead again, in a loop that kept viewers 2-3 s early.
      if (!this.seekAt || performance.now() - this.seekAt > wait) this.seek(drift < 0 ? target + this.seekCost : target + 0.1)
    } else if (abs > tol) {
      // Converge in roughly two seconds. A busy computer may not manage even
      // 5% faster; if it hasn't caught up after a while, jump instead.
      const now = performance.now()
      if (!this.nudging) this.nudgeSince = now
      this.nudging = true
      if (now - this.nudgeSince > NUDGE_MAX_MS && abs > 0.3 && !v.seeking) {
        this.setRate(1)
        this.nudging = false
        this.seek(drift < 0 ? target + this.seekCost : target + 0.1)
      } else {
        this.setRate(1 - Math.max(-MAX_NUDGE, Math.min(MAX_NUDGE, drift / 2)))
      }
    } else if (this.nudging && abs < tol / 2) {
      this.setRate(1)
      this.nudging = false
    }
  }

  private play() {
    const v = this.video
    if (!v.paused || this.mode === 'solo' || this.playPending) return
    this.playPending = true
    const done = () => (this.playPending = false)
    const started = () => {
      done()
      if (this.autoMuted && v.muted) this.onMutedStart()
      this.autoMuted = false
    }
    // Only "not allowed" means the browser refuses. Anything else (a new
    // source, a seek or a pause interrupting the request) is retried on the
    // next tick, so a hiccup never leaves the player stuck behind the
    // "tap to start" screen.
    const refused = (err: DOMException) => err?.name === 'NotAllowedError'
    this.own(() => {
      v.play()
        .then(started)
        .catch((err: DOMException) => {
          done()
          if (!refused(err)) return
          if (v.muted) return this.setBlocked()
          // Browsers refuse sound before the first click on the page (after a
          // reload, for example). Keep in sync muted, and let the viewer unmute.
          v.muted = true
          this.autoMuted = true
          this.playPending = true
          v.play()
            .then(started)
            .catch((e: DOMException) => {
              done()
              if (refused(e)) this.setBlocked()
            })
        })
    })
  }

  private setBlocked() {
    this.blocked = true
    this.onBlocked(true)
  }

  // Progress check, independent of drift: the room is playing, we follow it,
  // and the playhead hasn't moved for STALL_MS.
  private watchdog(s: RoomState | null) {
    const v = this.video
    const now = performance.now()
    const active = !!s?.media && s.playing && this.clock.now() >= s.anchor && this.mode === 'follow' && !this.blocked && !document.hidden
    const moved = Math.abs(v.currentTime - this.lastTime) > 0.05
    this.lastTime = v.currentTime
    if (!active || (moved && !v.paused && v.readyState >= 3)) {
      this.stallSince = 0
      if (moved) this.stallCount = 0
      return
    }
    if (!this.stallSince) this.stallSince = now
    if (now - this.stallSince < STALL_MS) return
    this.stallSince = now
    this.stallCount++
    this.onStall(this.stallCount)
  }

  private seek(t: number) {
    this.seekAt = performance.now()
    this.own(() => (this.video.currentTime = Math.max(0, t)))
  }

  private setRate(r: number) {
    if (Math.abs(this.video.playbackRate - r) > 0.001) this.video.playbackRate = r
  }

  private own(fn: () => void) {
    this.ownAction = performance.now()
    fn()
  }

  // Learn how long a seek takes to get playing again, so hard seeks land on
  // the room position instead of behind it.
  private onPlaying = () => {
    if (!this.seekAt) return
    const took = (performance.now() - this.seekAt) / 1000
    this.seekAt = 0
    if (took > 0 && took < 5) this.seekCost = Math.min(MAX_SEEK_COST, this.seekCost * 0.6 + took * 0.4)
  }
}
