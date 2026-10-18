// Clock keeps this browser aligned with the server clock and measures ping.
//
// Each pong gives one sample: rtt = receive - send, and
//   offset = serverTime - (send + rtt / 2)
// Network queues only ever make a sample look slower, never faster, so the
// sample with the lowest round trip is the most accurate offset. Ping shown to
// users is the median, jitter is the spread.

export interface Sample {
  rtt: number
  offset: number
}

const KEEP = 12

export class Clock {
  private samples: Sample[] = []
  private offset = 0
  rtt = 0
  jitter = 0
  synced = false

  /** Monotonic-ish local time in ms since epoch. */
  local(): number {
    return performance.timeOrigin + performance.now()
  }

  /** Current server time in ms. */
  now(): number {
    return this.local() + this.offset
  }

  pong(sent: number, server: number, received = this.local()) {
    const rtt = received - sent
    if (rtt < 0 || rtt > 30000) return
    this.samples.push({ rtt, offset: server - (sent + rtt / 2) })
    if (this.samples.length > KEEP) this.samples.shift()
    const best = this.samples.reduce((a, b) => (b.rtt < a.rtt ? b : a))
    // Move gently once synced so the playhead target never jumps.
    this.offset = this.synced ? this.offset + (best.offset - this.offset) * 0.5 : best.offset
    const sorted = this.samples.map((s) => s.rtt).sort((a, b) => a - b)
    this.rtt = Math.round(sorted[Math.floor(sorted.length / 2)])
    const q1 = sorted[Math.floor(sorted.length * 0.25)]
    const q3 = sorted[Math.floor(sorted.length * 0.75)]
    this.jitter = Math.round(q3 - q1)
    if (this.samples.length >= 3) this.synced = true
  }

  reset() {
    this.samples = []
    this.synced = false
  }
}
