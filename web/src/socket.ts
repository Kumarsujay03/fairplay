import { Clock } from './clock'
import { me, post, proof } from './api'
import * as S from './store'

type Handler = (m: any) => void

// Socket is the single realtime connection. It owns ping and reconnects with
// backoff. After a reconnect the server's hello replays everything.
export class Socket {
  readonly clock = new Clock()
  private ws: WebSocket | null = null
  private retry = 0
  private pingTimer = 0
  private stopped = false
  private extra: Handler[] = []

  connect() {
    if (this.ws && this.ws.readyState <= WebSocket.OPEN) return
    this.stopped = false
    const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`
    const ws = new WebSocket(url)
    this.ws = ws
    ws.onopen = () => {
      if (this.ws !== ws) return
      this.retry = 0
      S.connected.value = true
      this.clock.reset()
      // A quick burst gives a good clock estimate within half a second.
      for (let i = 0; i < 5; i++) setTimeout(() => this.ping(), i * 80)
      clearInterval(this.pingTimer)
      this.pingTimer = window.setInterval(() => this.ping(), 2000)
    }
    ws.onmessage = (e) => {
      if (this.ws !== ws) return
      let m: any
      try {
        m = JSON.parse(e.data)
      } catch {
        return
      }
      this.dispatch(m)
    }
    ws.onclose = async () => {
      if (this.ws !== ws) return // a replaced socket must not touch the new one
      S.connected.value = false
      clearInterval(this.pingTimer)
      if (this.stopped || S.kicked.value) return
      // If our session is gone (kicked while offline, server restart) go home.
      if (this.retry >= 2) {
        const who = await me().catch(() => null)
        if (who && who.status !== 'admitted') {
          if (await this.rejoin()) {
            this.retry = 0
            this.connect()
            return
          }
          S.kicked.value = 'Your session ended. Open your invite link again to rejoin.'
          return
        }
      }
      const delay = Math.min(10000, 500 * 2 ** this.retry++)
      setTimeout(() => this.connect(), delay)
    }
  }

  /** Rejoin with this tab's invite. Works when the device was already admitted. */
  private async rejoin(): Promise<boolean> {
    try {
      const id = sessionStorage.getItem('fp.last')
      const token = id && sessionStorage.getItem(`fp.inv.${id}`)
      if (!id || !token) return false
      const name = S.you.value?.name || ''
      const res = await post<{ status: string }>('/api/join', { id, token, name, pow: await proof() })
      return res.status === 'admitted'
    } catch {
      return false
    }
  }

  /** Drop the current socket and connect fresh (new session cookie). */
  reopen() {
    this.stopped = true
    this.ws?.close()
    this.ws = null
    S.kicked.value = null
    this.retry = 0
    this.connect()
  }

  close() {
    this.stopped = true
    clearInterval(this.pingTimer)
    this.ws?.close()
  }

  on(h: Handler) {
    this.extra.push(h)
    return () => (this.extra = this.extra.filter((x) => x !== h))
  }

  send(m: object) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m))
  }

  private ping() {
    this.send({ t: 'ping', c: this.clock.local() })
  }

  private dispatch(m: any) {
    switch (m.t) {
      case 'pong':
        this.clock.pong(m.c, m.s)
        S.ping.value = this.clock.rtt
        S.jitter.value = this.clock.jitter
        break
      case 'hello':
        S.you.value = m.you
        S.room.value = m.state
        S.settings.value = m.settings
        S.chat.value = m.chat || []
        S.media.value = m.media || null
        S.queue.value = m.queue || []
        S.peers.value = m.peers || []
        if (m.prefs) S.prefs.value = m.prefs
        break
      case 'prefs':
        S.prefs.value = m.prefs
        break
      case 'state':
        if (!S.room.value || m.state.v >= S.room.value.v || m.state.media !== S.room.value.media) S.room.value = m.state
        break
      case 'media':
        S.media.value = m.media
        break
      case 'chat':
        S.chat.value = [...S.chat.value, m.m].slice(-200)
        break
      case 'peers':
        S.peers.value = m.peers || []
        break
      case 'queue':
        S.queue.value = m.queue || []
        break
      case 'settings':
        S.settings.value = m.settings
        break
      case 'you':
        S.you.value = m.you
        if (m.you.chat === false) S.chat.value = []
        break
      case 'chathist':
        S.chat.value = m.chat || []
        break
      case 'lobby': {
        const before = new Set(S.lobby.value.map((p) => p.id))
        S.lobby.value = m.pending || []
        S.invites.value = m.invites || []
        if (m.settings) S.hostSettings.value = m.settings
        const fresh = S.lobby.value.find((p) => !before.has(p.id))
        if (fresh) S.showToast(`${fresh.name} wants to join`)
        break
      }
      case 'hostsettings':
        S.hostSettings.value = m.settings
        break
      case 'tunnel':
        S.tunnel.value = m.tunnel
        break
      case 'libst':
        S.library.value = { ...S.library.value, [m.status.id]: m.status }
        break
      case 'kicked':
        S.kicked.value = m.reason || 'You were removed from the room'
        this.stopped = true
        break
      case 'err':
        S.showToast(m.text, 'error')
        break
    }
    for (const h of this.extra) h(m)
  }
}

export const socket = new Socket()
