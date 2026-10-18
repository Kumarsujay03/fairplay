import { signal } from '@preact/signals'

export interface RoomState {
  media: string
  playing: boolean
  pos: number
  anchor: number
  rate: number
  reason: '' | 'host' | 'intermission' | 'buffering' | 'encoder' | 'ended' | 'loading'
  by?: string
  interUntil?: number
  v: number
}

export interface MediaStatus {
  id: string
  name: string
  state: 'idle' | 'probing' | 'encoding' | 'ready' | 'error'
  playable: boolean
  encoded: number
  duration: number
  speed: number
  error?: string
  encoder?: string
  rungs?: string[]
  audio?: { index: number; label: string; lang: string }[]
  subs?: { id: string; label: string; lang: string; url: string }[]
  notes?: string[]
  width?: number
  height?: number
  bitrate?: number
}

export interface Peer {
  id: string
  name: string
  role: 'host' | 'guest'
  cohost?: boolean
  chatOff?: boolean
  mode: 'follow' | 'solo'
  buf: boolean
  rtt: number
  jit: number
  drift: number
  pos: number
  q?: string
  device?: string
}

export interface ChatMsg {
  id: number
  sid: string
  name: string
  text: string
  ts: number
  sys?: boolean
}

export interface You {
  id: string
  name: string
  role: 'host' | 'guest'
  control: boolean
  chat: boolean
}

export interface Pending {
  id: string
  name: string
  label: string
  device: string
  ip: string
  secondDevice: boolean
  since: number
}

export interface InviteView {
  id: string
  label: string
  bound: boolean
  admitted: boolean
  revoked: boolean
  expired: boolean
  expiresAt: string
  name?: string
}

export interface HostSettings {
  roomName: string
  autoAdmit: boolean
  locked: boolean
  waitForAll: boolean
  reactions: boolean
  maxGuests: number
}

export interface Tunnel {
  provider: string
  state: 'off' | 'starting' | 'up' | 'error'
  url?: string
  error?: string
  lan?: string[]
}

export const you = signal<You | null>(null)
export const room = signal<RoomState | null>(null)
export const media = signal<MediaStatus | null>(null)
export const peers = signal<Peer[]>([])
export const chat = signal<ChatMsg[]>([])
export const queue = signal<{ id: string; name: string }[]>([])
export const settings = signal<{ roomName: string; reactions: boolean; waitForAll: boolean }>({
  roomName: 'FairPlay',
  reactions: true,
  waitForAll: false,
})
export const connected = signal(false)
export const kicked = signal<string | null>(null)
export const toast = signal<{ text: string; kind?: 'error' | 'info' } | null>(null)

// Host only
export const lobby = signal<Pending[]>([])
export const invites = signal<InviteView[]>([])
export const hostSettings = signal<HostSettings | null>(null)
export const tunnel = signal<Tunnel | null>(null)
export const library = signal<Record<string, MediaStatus>>({})

// Local viewer
export const ping = signal(0)
export const jitter = signal(0)
export const mode = signal<'follow' | 'solo'>('follow')

let toastTimer: number | undefined
export function showToast(text: string, kind: 'error' | 'info' = 'info') {
  toast.value = { text, kind }
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => (toast.value = null), 4000)
}
