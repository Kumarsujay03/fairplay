import { useEffect, useState } from 'preact/hooks'
import { ApiError, del, get, me, post, proof } from '../api'
import { socket } from '../socket'
import * as S from '../store'
import { Icon, Logo } from '../components/Icons'
import { LobbyEntry } from '../components/Lobby'
import { InviteQR } from '../components/QR'
import { Avatar, peerStatus } from '../components/Peers'
import { copy, fmtBytes, fmtTime, navigate, pingClass, qualityLabel } from '../util'

interface Item {
  id: string
  name: string
  path: string
  size: number
}

const TOKENS_KEY = 'fp.tokens'
function loadTokens(): Record<string, string> {
  try {
    return JSON.parse(sessionStorage.getItem(TOKENS_KEY) || '{}')
  } catch {
    return {}
  }
}
function saveToken(id: string, token: string) {
  const t = loadTokens()
  t[id] = token
  try {
    sessionStorage.setItem(TOKENS_KEY, JSON.stringify(t))
  } catch {}
}

export function Host() {
  const [phase, setPhase] = useState<'checking' | 'login' | 'in'>('checking')
  const [key, setKey] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const login = async (k: string) => {
    setBusy(true)
    setErr('')
    try {
      const p = await proof()
      await post('/api/host/login', { key: k.trim(), pow: p })
      S.kicked.value = null
      socket.reopen()
      setPhase('in')
    } catch (e) {
      setErr((e as Error).message)
      setPhase('login')
    } finally {
      setBusy(false)
    }
  }

  useEffect(() => {
    const check = () => {
      const m = location.hash.match(/[#&]k=([A-Za-z0-9_-]{10,64})/)
      if (m) history.replaceState(null, '', '/host')
      me().then((who) => {
        if (who.status === 'admitted' && who.role === 'host') {
          S.kicked.value = null
          socket.connect()
          setPhase('in')
        } else if (m) {
          // A new host link (for example after a restart) opened in the same tab.
          // Stay on the spinner until the new session exists.
          setPhase('checking')
          login(m[1])
        } else setPhase('login')
      })
    }
    check()
    addEventListener('hashchange', check)
    return () => removeEventListener('hashchange', check)
  }, [])

  if (S.kicked.value && phase === 'in') {
    return (
      <div class="center-page">
        <div class="join-card">
          <Logo />
          {/stopped|closed/i.test(S.kicked.value) ? (
            <>
              <h1 class="h24">FairPlay stopped</h1>
              <div class="dim">The room has ended and FairPlay is closed on this computer. You can close this tab.</div>
              <div class="caption muted">
                Start it again any time with <span class="mono">fairplay</span>
              </div>
            </>
          ) : (
            <>
              <h1 class="h24">Host session ended</h1>
              <div class="dim">FairPlay was restarted, so the host key changed. Open the new host link from your terminal.</div>
              <button class="btn btn-ghost" onClick={() => { S.kicked.value = null; setPhase('login') }}>
                Enter the new key
              </button>
            </>
          )}
        </div>
      </div>
    )
  }

  if (phase === 'checking') return <div class="center-page"><div class="spinner" /></div>
  if (phase === 'login') {
    return (
      <div class="center-page">
        <form
          class="join-card"
          onSubmit={(e) => {
            e.preventDefault()
            login(key)
          }}
        >
          <Logo />
          <h1 class="h24">Host console</h1>
          <div class="dim">Open the host link printed in your terminal, or paste the host key.</div>
          <input
            class="input"
            type="password"
            autoComplete="off"
            placeholder="Host key"
            value={key}
            onInput={(e) => setKey((e.target as HTMLInputElement).value)}
          />
          <button class="btn btn-primary btn-block" disabled={busy || !key}>
            {busy ? 'Checking…' : 'Open console'}
          </button>
          {err && <div class="caption warn-text">{err}</div>}
        </form>
      </div>
    )
  }
  return <Console />
}

interface UpdateInfo {
  current: string
  latest: string
  mode: 'auto' | 'check' | 'off'
  devBuild: boolean
  available: boolean
  error?: string
}

function Console() {
  const [items, setItems] = useState<Item[]>([])
  const [root, setRoot] = useState('')
  const [version, setVersion] = useState('')
  const [upd, setUpd] = useState<UpdateInfo | null>(null)
  const [tokens, setTokens] = useState(loadTokens)
  const [, tick] = useState(0)

  const refreshLibrary = () =>
    get<{ items: Item[]; status: Record<string, S.MediaStatus>; root: string }>('/api/host/library')
      .then((r) => {
        setItems(r.items || [])
        setRoot(r.root)
        S.library.value = { ...r.status, ...S.library.value }
      })
      .catch((e) => S.showToast(e.message, 'error'))

  useEffect(() => {
    refreshLibrary()
    get<{ tunnel: S.Tunnel; settings: S.HostSettings; version: string; update: UpdateInfo | null }>('/api/host/info').then((r) => {
      S.tunnel.value = r.tunnel
      S.hostSettings.value = r.settings
      setVersion(r.version)
      setUpd(r.update)
    })
    const t = setInterval(() => tick((n) => n + 1), 500)
    return () => clearInterval(t)
  }, [])

  const st = S.room.value
  const cur = S.media.value
  const guests = S.peers.value.filter((p) => p.role === 'guest')
  const statusLive = st?.playing
  const pos = st ? (!st.playing || socket.clock.now() < st.anchor ? st.pos : st.pos + (socket.clock.now() - st.anchor) / 1000) : 0

  return (
    <div class="console">
      <header class="console-top">
        <Logo />
        <span class="dim">Host console</span>
        <VersionTag version={version} upd={upd} />
        <span class="grow" />
        <span class="pill tag neutral">
          <Icon name="lock" size={14} /> Host verified
        </span>
        <span class={`pill badge ${statusLive ? 'live' : 'wait'}`}>
          <span class="dot" />
          {statusLive ? `ROOM LIVE · ${fmtTime(pos)}` : st?.media ? `PAUSED · ${fmtTime(pos)}` : 'NO VIDEO'}
        </span>
        <button
          class="btn btn-danger"
          title="End the room for everyone and close FairPlay on this computer"
          onClick={async () => {
            if (!confirm('Stop FairPlay? The room ends for everyone and FairPlay closes on this computer. Start it again with: fairplay')) return
            try {
              await post('/api/host/shutdown')
            } catch {}
            S.kicked.value = 'FairPlay has stopped on this computer. You can close this tab. Start it again with: fairplay'
            setTimeout(() => socket.close(), 100)
          }}
        >
          <Icon name="x" size={16} /> Stop FairPlay
        </button>
        <button class="btn btn-primary" onClick={() => navigate('/room')}>
          <Icon name="play" size={16} /> Open player
        </button>
      </header>

      <main class="console-grid">
        <section class="col gap24">
          <NowPlaying
            cur={cur}
            items={items}
            root={root}
            onRefresh={refreshLibrary}
            onFolder={(r) => {
              setItems(r.items || [])
              setRoot(r.root)
              S.library.value = { ...r.status, ...S.library.value }
            }}
          />
          <Playback pos={pos} />
          <Share guests={guests.length} />
          <Storage />
        </section>
        <section class="col gap24">
          {S.lobby.value.length > 0 && (
            <div class="card lobby-card">
              <div class="card-head">
                <div class="h18">Waiting to join · {S.lobby.value.length}</div>
              </div>
              {S.lobby.value.map((p) => (
                <LobbyEntry key={p.id} p={p} />
              ))}
            </div>
          )}
          <Invites tokens={tokens} setTokens={setTokens} />
          <Viewers />
          <RoomSettings />
        </section>
      </main>
    </div>
  )
}

interface LibraryReply {
  items: Item[]
  status: Record<string, S.MediaStatus>
  root: string
}

function VersionTag({ version, upd }: { version: string; upd: UpdateInfo | null }) {
  if (!version) return null
  if (upd?.available) {
    const how = upd.mode === 'auto' ? 'installs when you next start FairPlay' : 'run `fairplay update` to install'
    return (
      <span class="tag warn" title={how}>
        {upd.latest} available · {how}
      </span>
    )
  }
  return (
    <span class="caption muted" title={upd?.devBuild ? 'Development build, auto-update is off' : `Updates: ${upd?.mode ?? 'auto'}`}>
      {version}
      {upd && !upd.devBuild && upd.latest ? ' · up to date' : ''}
    </span>
  )
}

/** Folder picker: browse sub-folders and choose where videos come from. */
function FolderPicker({ root, onDone, onCancel }: { root: string; onDone: (r: LibraryReply) => void; onCancel: () => void }) {
  const [path, setPath] = useState(root)
  const [typed, setTyped] = useState(root)
  const [listing, setListing] = useState<{ path: string; parent?: string; dirs: string[]; videos: number } | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const open = async (p: string) => {
    setErr('')
    try {
      const r = await get<{ listing: { path: string; parent?: string; dirs: string[]; videos: number } }>(
        `/api/host/browse?path=${encodeURIComponent(p)}`,
      )
      setListing(r.listing)
      setPath(r.listing.path)
      setTyped(r.listing.path)
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  useEffect(() => {
    open(root)
  }, [])

  const choose = async () => {
    setBusy(true)
    try {
      onDone(await post<LibraryReply>('/api/host/media', { path }))
      S.showToast('Video folder changed')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const sep = path.includes('\\') && !path.includes('/') ? '\\' : '/'
  return (
    <div class="folder-picker col">
      <form
        class="row"
        onSubmit={(e) => {
          e.preventDefault()
          open(typed)
        }}
      >
        <input class="input grow mono" value={typed} onInput={(e) => setTyped((e.target as HTMLInputElement).value)} aria-label="Folder path" />
        <button class="btn btn-ghost btn-sm">Go</button>
      </form>
      <div class="folder-list">
        {listing?.parent && (
          <button class="folder-row" onClick={() => open(listing.parent!)}>
            <Icon name="up" size={16} /> Up one folder
          </button>
        )}
        {listing?.dirs.map((d) => (
          <button class="folder-row" key={d} onClick={() => open(path.replace(/[\\/]$/, '') + sep + d)}>
            <Icon name="film" size={16} class="muted" /> {d}
          </button>
        ))}
        {listing && listing.dirs.length === 0 && <div class="caption muted pad">No sub-folders here.</div>}
      </div>
      {err && <div class="caption warn-text">{err}</div>}
      <div class="row">
        <span class="caption dim grow">
          {listing ? `${listing.videos} video${listing.videos === 1 ? '' : 's'} directly in this folder (sub-folders are included too)` : ''}
        </span>
        <button class="btn btn-ghost btn-sm" onClick={onCancel}>
          Cancel
        </button>
        <button class="btn btn-primary btn-sm" onClick={choose} disabled={busy || !listing}>
          Use this folder
        </button>
      </div>
    </div>
  )
}

function NowPlaying({
  cur,
  items,
  root,
  onRefresh,
  onFolder,
}: {
  cur: S.MediaStatus | null
  items: Item[]
  root: string
  onRefresh: () => void
  onFolder: (r: LibraryReply) => void
}) {
  const [filter, setFilter] = useState('')
  const [picking, setPicking] = useState(false)
  const queuedAt = (id: string) => S.queue.value.findIndex((q) => q.id === id)
  const lib = S.library.value
  const shown = items.filter((i) => i.path.toLowerCase().includes(filter.toLowerCase()))
  const curLive = cur ? lib[cur.id] || cur : null
  return (
    <div class="card">
      <div class="card-head">
        <div class="h18">Now playing</div>
        <div class="row">
          {cur && (
            <button
              class="btn btn-danger btn-sm"
              title="Stop the video for everyone and stop preparing it"
              onClick={() => confirm(`Stop “${cur.name}” for everyone?`) && socket.send({ t: 'host', op: 'clear' })}
            >
              Stop &amp; clear
            </button>
          )}
          <button class="icon-btn sm plain" onClick={onRefresh} title="Rescan folder">
            <Icon name="refresh" size={18} />
          </button>
        </div>
      </div>
      {curLive ? <MediaSummary m={curLive} /> : <div class="dim">Pick a video below. It starts preparing right away.</div>}

      {S.queue.value.length > 0 && (
        <div class="col">
          <div class="label dim">Up next</div>
          {S.queue.value.map((q, i) => (
            <div class="queue-row" key={`${q.id}-${i}`}>
              <span class="mono muted">{i + 1}</span>
              <span class="grow ellipsis">{q.name}</span>
              {i > 0 && (
                <button class="icon-btn sm plain" title="Move up" onClick={() => socket.send({ t: 'host', op: 'queue_move', index: i, to: i - 1 })}>
                  <Icon name="up" size={16} />
                </button>
              )}
              <button class="icon-btn sm plain" title="Remove" onClick={() => socket.send({ t: 'host', op: 'queue_remove', index: i })}>
                <Icon name="x" size={16} />
              </button>
            </div>
          ))}
          <div class="row">
            <button class="btn btn-ghost btn-sm" onClick={() => socket.send({ t: 'host', op: 'next' })}>
              <Icon name="next" size={14} /> Play next now
            </button>
            <button class="btn btn-ghost btn-sm" onClick={() => socket.send({ t: 'host', op: 'queue_clear' })}>
              Clear queue
            </button>
          </div>
        </div>
      )}

      <div class="col">
        <div class="row folder-line">
          <Icon name="film" size={16} class="muted" />
          <span class="mono caption ellipsis grow" title={root}>
            {root || '…'}
          </span>
          {!picking && (
            <button class="btn btn-ghost btn-sm" onClick={() => setPicking(true)}>
              Change folder
            </button>
          )}
        </div>
        {picking && (
          <FolderPicker
            root={root}
            onCancel={() => setPicking(false)}
            onDone={(r) => {
              setPicking(false)
              onFolder(r)
            }}
          />
        )}
        <input class="input" placeholder="Search your videos" value={filter} onInput={(e) => setFilter((e.target as HTMLInputElement).value)} />
        <div class="library">
          {shown.length === 0 && (
            <div class="caption muted pad">
              {items.length === 0 ? 'No videos in this folder yet. Add some, then press refresh, or choose another folder.' : 'Nothing matches your search.'}
            </div>
          )}
          {shown.map((it) => {
            const s = lib[it.id]
            return (
              <div class={`lib-row ${cur?.id === it.id ? 'current' : ''}`} key={it.id}>
                <Icon name="film" size={18} class="muted" />
                <div class="grow col tight">
                  <span class="ellipsis">{it.name}</span>
                  <span class="caption muted ellipsis">
                    {it.path} · {fmtBytes(it.size)}
                    {s?.state === 'ready' && ' · ready'}
                    {s?.state === 'encoding' && ` · encoding ${s.duration ? Math.round((s.encoded / s.duration) * 100) : 0}%`}
                  </span>
                </div>
                {queuedAt(it.id) >= 0 ? (
                  <button
                    class="btn btn-sm btn-ghost queued"
                    title="Remove from the queue"
                    onClick={() => socket.send({ t: 'host', op: 'queue_remove', index: queuedAt(it.id) })}
                  >
                    Queued ✓ <span class="muted">· remove</span>
                  </button>
                ) : (
                  <button class="btn btn-sm btn-ghost" onClick={() => socket.send({ t: 'host', op: 'queue_add', media: it.id })}>
                    Queue
                  </button>
                )}
                <button class="btn btn-sm btn-primary" onClick={() => socket.send({ t: 'host', op: 'load', media: it.id })}>
                  Play
                </button>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

function MediaSummary({ m }: { m: S.MediaStatus }) {
  const pct = m.duration ? Math.min(100, (m.encoded / m.duration) * 100) : 0
  return (
    <div class="col">
      <div class="row">
        <div class="thumb" />
        <div class="grow col tight">
          <div class="h18 ellipsis">{m.name}</div>
          <div class="caption dim">
            {fmtTime(m.duration)}
            {m.width ? ` · ${m.width}×${m.height}` : ''}
            {m.encoder ? ` · ${m.encoder}` : ''}
          </div>
          <div class="row wrap">
            {m.state === 'ready' && <span class="tag ok"><Icon name="check" size={12} /> Ready</span>}
            {m.state === 'encoding' && <span class="tag warn">Encoding {pct.toFixed(0)}% · {m.speed?.toFixed(1)}×</span>}
            {m.state === 'probing' && <span class="tag neutral">Reading file…</span>}
            {m.state === 'error' && <span class="tag bad">Error</span>}
            {m.playable && m.state !== 'ready' && <span class="tag ok">Playable now</span>}
            {m.rungs?.map((r) => <span key={r} class="tag neutral">{r}</span>)}
            {(m.audio?.length || 0) > 1 && <span class="tag neutral">{m.audio!.length} audio tracks</span>}
            {(m.subs?.length || 0) > 0 && <span class="tag neutral">{m.subs!.length} subtitles</span>}
          </div>
        </div>
      </div>
      {m.state === 'encoding' && (
        <>
          <div class="progress">
            <div style={{ width: `${pct}%` }} />
          </div>
          <EncoderLead m={m} />
        </>
      )}
      {m.playable && S.media.value?.id === m.id && <RoomTracks m={m} />}
      {m.error && <div class="caption warn-text">{m.error}</div>}
      {m.notes?.map((n) => <div class="caption muted" key={n}>{n}</div>)}
    </div>
  )
}

/** The host's choice of audio, subtitles and quality for everyone. Viewers
 * follow it unless they pick their own in the player menu. */
function RoomTracks({ m }: { m: S.MediaStatus }) {
  const p = S.prefs.value
  const set = (patch: Partial<S.TrackPrefs>) => socket.send({ t: 'host', op: 'prefs', prefs: { ...p, ...patch } })
  const audio = m.audio || []
  const subs = m.subs || []
  return (
    <div class="room-tracks">
      <div class="label dim">Picture and sound for everyone</div>
      <div class="row wrap">
        {audio.length > 1 && (
          <label class="track-pick">
            <span class="caption muted">Audio</span>
            <select class="input select" value={String(p.audio)} onChange={(e) => set({ audio: parseInt((e.target as HTMLSelectElement).value) })}>
              <option value="-1">File default</option>
              {audio.map((a) => (
                <option key={a.index} value={String(a.index)}>
                  {a.label}
                </option>
              ))}
            </select>
          </label>
        )}
        <label class="track-pick">
          <span class="caption muted">Subtitles</span>
          <select class="input select" value={p.sub} onChange={(e) => set({ sub: (e.target as HTMLSelectElement).value })}>
            <option value="">Off</option>
            {subs.map((s) => (
              <option key={s.id} value={s.id}>
                {s.label}
              </option>
            ))}
          </select>
        </label>
        <label class="track-pick">
          <span class="caption muted">Quality</span>
          <select class="input select" value={p.quality} onChange={(e) => set({ quality: (e.target as HTMLSelectElement).value })}>
            <option value="auto">Auto (best per device and connection)</option>
            {(m.rungs || []).map((r) => (
              <option key={r} value={r}>
                {qualityLabel(r)}
              </option>
            ))}
          </select>
        </label>
      </div>
      <div class="caption muted">Viewers start with these and can change them for their own screen. Only you control play, pause and intermission.</div>
    </div>
  )
}

/** How far the encoder is ahead of where the room is watching. */
function EncoderLead({ m }: { m: S.MediaStatus }) {
  const st = S.room.value
  if (!st || st.media !== m.id) return null
  const now = socket.clock.now()
  const pos = !st.playing || now < st.anchor ? st.pos : st.pos + (now - st.anchor) / 1000
  const ahead = m.encoded - pos
  const cls = ahead > 60 ? 'ok' : ahead > 15 ? 'warn' : 'bad'
  return (
    <div class={`caption tag-text ${cls}`}>
      Encoder is {fmtTime(Math.max(0, ahead))} ahead of the room
      {ahead <= 15 ? ' · the room pauses briefly if it catches up' : ''}
    </div>
  )
}

/** Intermission button with an editable length. The length is a room
 * setting, so the player's intermission button uses it too. */
function IntermissionStart({ disabled, onStart }: { disabled: boolean; onStart: (minutes: number) => void }) {
  const s = S.hostSettings.value
  const saved = s?.interMinutes || 5
  const [min, setMin] = useState(saved)
  useEffect(() => setMin(saved), [saved])
  const commit = (v: number) => {
    const n = Math.max(1, Math.min(120, Math.round(v) || 5))
    setMin(n)
    if (s && n !== s.interMinutes) socket.send({ t: 'host', op: 'settings', settings: { ...s, interMinutes: n } })
    return n
  }
  return (
    <div class="row inter-start">
      <button class="btn btn-danger" onClick={() => onStart(commit(min))} disabled={disabled}>
        Intermission
      </button>
      <label class="row inter-len" title="Intermission length in minutes, 1 to 120">
        <input
          class="input num"
          type="number"
          min={1}
          max={120}
          value={min}
          aria-label="Intermission length in minutes"
          onInput={(e) => setMin(parseInt((e.target as HTMLInputElement).value))}
          onChange={(e) => commit(parseInt((e.target as HTMLInputElement).value))}
        />
        <span class="caption dim">min</span>
      </label>
    </div>
  )
}

function Playback({ pos }: { pos: number }) {
  const st = S.room.value
  const m = S.media.value
  const dur = m?.duration || 0
  const send = (op: string, extra: object = {}) => socket.send({ t: 'cmd', op, ...extra })
  return (
    <div class="card">
      <div class="h18">Room playback</div>
      <div class="row wrap">
        <button class="icon-btn" onClick={() => send('seek', { pos: pos - 10 })} title="Back 10s for everyone">
          <Icon name="back10" />
        </button>
        <button class="big-play sm" onClick={() => send(st?.playing ? 'pause' : 'play')} disabled={!m?.playable}>
          <Icon name={st?.playing ? 'pause' : 'play'} size={22} />
        </button>
        <button class="icon-btn" onClick={() => send('seek', { pos: pos + 10 })} title="Forward 10s for everyone">
          <Icon name="fwd10" />
        </button>
        <span class="mono">
          {fmtTime(pos)} / {fmtTime(dur)}
        </span>
        <span class="grow" />
        {st?.reason === 'intermission' ? (
          <button class="btn btn-primary" onClick={() => send('inter_end')}>
            End intermission
          </button>
        ) : (
          <>
            <IntermissionStart disabled={!m?.playable} onStart={(min) => send('inter', { sec: min * 60 })} />
            <button class="btn btn-ghost" onClick={() => send('inter', { sec: 0 })} disabled={!m?.playable}>
              Pause for all
            </button>
          </>
        )}
      </div>
      <input
        type="range"
        class="range-wide"
        min={0}
        max={dur || 1}
        step={1}
        value={pos}
        onChange={(e) => send('seek', { pos: parseFloat((e.target as HTMLInputElement).value) })}
        aria-label="Seek for everyone"
      />
      <div class="caption muted">
        Play, pause and seek here move the global timer for everyone. Guests who pause on their own stay behind until they tap “Back to live”.
      </div>
    </div>
  )
}

interface StorageInfo {
  path: string
  bytes: number
  max: number
  items: { key: string; name: string; bytes: number; done: boolean; inUse: boolean }[]
}

/** Where prepared videos live, how much space they take, and a way to free it. */
function Storage() {
  const [info, setInfo] = useState<StorageInfo | null>(null)
  const [busy, setBusy] = useState(false)
  const load = () => get<StorageInfo>('/api/host/storage').then((r) => setInfo({ ...r, items: r.items || [] })).catch(() => {})
  useEffect(() => {
    load()
    const t = setInterval(load, 30000)
    return () => clearInterval(t)
  }, [])
  if (!info) return null
  const clear = async () => {
    if (!confirm('Delete prepared videos to free space? The video playing now is kept. Others are prepared again when played.')) return
    setBusy(true)
    try {
      const r = await post<StorageInfo & { freed: number }>('/api/host/storage/clear')
      setInfo({ ...r, items: r.items || [] })
      S.showToast(`Freed ${fmtBytes(r.freed)}`)
    } finally {
      setBusy(false)
    }
  }
  const pct = info.max > 0 ? Math.min(100, (info.bytes / info.max) * 100) : 0
  const others = info.items.filter((i) => !i.inUse)
  return (
    <div class="card">
      <div class="card-head">
        <div class="h18">Storage</div>
        <button class="icon-btn sm plain" onClick={load} title="Refresh">
          <Icon name="refresh" size={18} />
        </button>
      </div>
      <div class="col tight">
        <div class="row">
          <span class="grow">
            Prepared videos use <strong>{fmtBytes(info.bytes)}</strong>
            {info.max > 0 && <span class="dim"> of {fmtBytes(info.max)}</span>}
          </span>
        </div>
        {info.max > 0 && (
          <div class="progress">
            <div style={{ width: `${pct}%` }} />
          </div>
        )}
        <span class="mono caption muted ellipsis" title={info.path}>
          {info.path}
        </span>
      </div>
      {info.items.length > 0 && (
        <div class="divider-list">
          {info.items.slice(0, 5).map((i) => (
            <div class="row storage-row" key={i.key}>
              <span class="grow ellipsis">{i.name}</span>
              {i.inUse && <span class="tag ok">in use</span>}
              {!i.done && !i.inUse && <span class="tag neutral">unfinished</span>}
              <span class="mono caption dim">{fmtBytes(i.bytes)}</span>
            </div>
          ))}
          {info.items.length > 5 && <div class="caption muted pad">+{info.items.length - 5} more</div>}
        </div>
      )}
      <button class="btn btn-ghost" onClick={clear} disabled={busy || others.length === 0}>
        {others.length === 0 ? 'Nothing else to clear' : `Clear prepared videos (${fmtBytes(others.reduce((a, i) => a + i.bytes, 0))})`}
      </button>
      <div class="caption muted">
        Older videos are removed automatically when the limit is reached. After closing FairPlay you can also run <span class="mono">fairplay cache clear</span>.
      </div>
    </div>
  )
}

function Share({ guests }: { guests: number }) {
  const t = S.tunnel.value
  const peers = S.peers.value.filter((p) => p.role === 'guest' && p.rtt > 0)
  const avg = peers.length ? Math.round(peers.reduce((a, p) => a + p.rtt, 0) / peers.length) : 0
  const worst = peers.reduce((w, p) => (p.rtt > w.rtt ? p : w), { rtt: 0, name: '' } as { rtt: number; name: string })
  const kbps = S.media.value?.bitrate || 0
  const need = ((kbps * Math.max(1, guests)) / 1000).toFixed(1)
  return (
    <div class="card">
      <div class="h18">Share</div>
      <div class="col">
        <div class="label dim">Internet ({t?.provider || 'tunnel'})</div>
        {t?.state === 'up' ? (
          <div class="row">
            <div class="url-box grow">
              <Icon name="link" size={16} class="muted" />
              <span class="mono ellipsis">{t.url}</span>
            </div>
            <span class="tag ok">connected</span>
          </div>
        ) : (
          <div class="caption dim">
            {t?.state === 'starting' && 'Starting the tunnel…'}
            {t?.state === 'error' && <span class="warn-text">{t.error}</span>}
            {t?.state === 'off' && 'Tunnel is off. Start FairPlay with -tunnel cloudflared to share over the internet.'}
          </div>
        )}
      </div>
      {(t?.lan?.length || 0) > 0 && (
        <div class="col">
          <div class="label dim">Same Wi-Fi</div>
          {t!.lan!.map((u) => (
            <span class="mono caption" key={u}>{u}</span>
          ))}
          <span class="caption muted">
            Phones: scan an invite’s <strong>QR</strong> code. If you type the address, include the port (<span class="mono">:{new URL(t!.lan![0]).port}</span>), or the browser goes to the wrong place.
          </span>
        </div>
      )}
      <div class="row wrap caption">
        <span class="dim">Room ping</span>
        <span class={`ping ${pingClass(avg)}`}>avg {avg} ms</span>
        {worst.rtt > 0 && (
          <span class={`ping ${pingClass(worst.rtt)}`}>
            worst {worst.rtt} ms ({worst.name})
          </span>
        )}
      </div>
      {kbps > 0 && (
        <div class="caption muted">
          Top quality for {Math.max(1, guests)} viewer{guests === 1 ? '' : 's'} needs about {need} Mbps of upload. Slower viewers drop to a lower quality automatically.
        </div>
      )}
      <div class="caption muted">The base link alone does nothing. Every guest needs a personal invite below.</div>
    </div>
  )
}

function Invites({ tokens, setTokens }: { tokens: Record<string, string>; setTokens: (t: Record<string, string>) => void }) {
  const [label, setLabel] = useState('')
  const [hours, setHours] = useState(24)
  const t = S.tunnel.value
  const online = new Set(S.peers.value.map((p) => p.name.toLowerCase()))

  const base = (kind: 'net' | 'lan') => (kind === 'net' ? t?.url : t?.lan?.[0]) || location.origin
  const linkFor = (id: string, kind: 'net' | 'lan') => `${base(kind)}/j/${id}#t=${tokens[id]}`

  const create = async (e: Event) => {
    e.preventDefault()
    try {
      const r = await post<{ invite: S.InviteView; token: string }>('/api/host/invites', { label, hours })
      saveToken(r.invite.id, r.token)
      setTokens(loadTokens())
      setLabel('')
      const kind = t?.state === 'up' ? 'net' : 'lan'
      const link = `${base(kind)}/j/${r.invite.id}#t=${r.token}`
      if (await copy(link)) S.showToast(`Invite for ${r.invite.label} copied`)
      S.invites.value = await get<S.InviteView[]>('/api/host/invites')
    } catch (err) {
      S.showToast((err as ApiError).message, 'error')
    }
  }

  const regen = async (id: string) => {
    const r = await post<{ token: string }>(`/api/host/invites/${id}/regen`)
    saveToken(id, r.token)
    setTokens(loadTokens())
    S.invites.value = await get<S.InviteView[]>('/api/host/invites')
    S.showToast('New link made. The old one no longer works.')
  }

  const revoke = async (id: string, label: string) => {
    if (!confirm(`Revoke ${label}'s invite? They will be removed from the room.`)) return
    await post(`/api/host/invites/${id}/revoke`)
    S.invites.value = await get<S.InviteView[]>('/api/host/invites')
  }

  const remove = async (id: string) => {
    await del(`/api/host/invites/${id}`)
    S.invites.value = await get<S.InviteView[]>('/api/host/invites')
  }

  const [qrFor, setQrFor] = useState<S.InviteView | null>(null)
  const qrLinks = (id: string) => {
    const out: { kind: string; url: string }[] = []
    if ((t?.lan?.length || 0) > 0) out.push({ kind: 'Wi-Fi', url: linkFor(id, 'lan') })
    if (t?.state === 'up') out.push({ kind: 'Internet', url: linkFor(id, 'net') })
    if (!out.length) out.push({ kind: 'This computer', url: `${location.origin}/j/${id}#t=${tokens[id]}` })
    return out
  }

  const doCopy = async (id: string, kind: 'net' | 'lan') => {
    if (await copy(linkFor(id, kind))) S.showToast('Link copied')
  }

  return (
    <div class="card">
      <div class="h18">Invites</div>
      <form class="row" onSubmit={create}>
        <input class="input grow" placeholder="Guest name, e.g. Priya" value={label} maxLength={24} onInput={(e) => setLabel((e.target as HTMLInputElement).value)} />
        <select class="input select" value={hours} onChange={(e) => setHours(parseInt((e.target as HTMLSelectElement).value))} aria-label="Expires after">
          <option value={6}>6 h</option>
          <option value={24}>24 h</option>
          <option value={72}>3 days</option>
          <option value={168}>7 days</option>
        </select>
        <button class="btn btn-primary" disabled={!label.trim()}>
          <Icon name="plus" size={16} /> Invite
        </button>
      </form>
      <div class="divider-list">
        {S.invites.value.length === 0 && <div class="caption muted pad">No invites yet. Each person gets their own link.</div>}
        {S.invites.value.map((inv) => {
          const isOn = online.has((inv.name || inv.label).toLowerCase())
          const status = inv.revoked
            ? { t: 'Revoked', c: 'bad' }
            : inv.expired
              ? { t: 'Expired', c: 'bad' }
              : isOn
                ? { t: 'Watching now', c: 'ok' }
                : inv.admitted
                  ? { t: 'Joined before', c: 'neutral' }
                  : inv.bound
                    ? { t: 'Opened, waiting', c: 'warn' }
                    : { t: 'Not opened yet', c: 'neutral' }
          const has = !!tokens[inv.id]
          return (
            <div class="invite-row" key={inv.id}>
              <Avatar id={inv.id} name={inv.label} />
              <div class="grow col tight">
                <span class="ellipsis">
                  {inv.label}
                  {inv.name && inv.name !== inv.label ? <span class="muted"> · as {inv.name}</span> : null}
                </span>
                <span class={`caption tag-text ${status.c}`}>{status.t}</span>
              </div>
              {!inv.revoked && !inv.expired && has && (
                <>
                  {t?.state === 'up' && (
                    <button class="btn btn-sm btn-ghost" onClick={() => doCopy(inv.id, 'net')} title="Copy internet link">
                      <Icon name="copy" size={14} /> Link
                    </button>
                  )}
                  {(t?.lan?.length || 0) > 0 && (
                    <button class="btn btn-sm btn-ghost" onClick={() => doCopy(inv.id, 'lan')} title="Copy Wi-Fi link">
                      Wi-Fi
                    </button>
                  )}
                  <button class="btn btn-sm btn-ghost" onClick={() => setQrFor(inv)} title="Show a QR code to scan with a phone">
                    QR
                  </button>
                </>
              )}
              {!inv.revoked && !has && (
                <button class="btn btn-sm btn-ghost" onClick={() => regen(inv.id)} title="Links are shown once. Make a new one to copy it again.">
                  New link
                </button>
              )}
              {!inv.revoked ? (
                <button class="icon-btn sm plain danger" onClick={() => revoke(inv.id, inv.label)} title="Revoke">
                  <Icon name="x" size={16} />
                </button>
              ) : (
                <button class="icon-btn sm plain" onClick={() => remove(inv.id)} title="Delete">
                  <Icon name="trash" size={16} />
                </button>
              )}
            </div>
          )
        })}
      </div>
      {qrFor && <InviteQR label={qrFor.label} links={qrLinks(qrFor.id)} onClose={() => setQrFor(null)} />}
      <div class="caption muted">
        One invite per person. The link carries a 128-bit key, locks to the first device that opens it, and every first join waits for you to
        admit it. Forwarded links show up flagged in the lobby.
      </div>
    </div>
  )
}

function Viewers() {
  const list = S.peers.value
  return (
    <div class="card">
      <div class="h18">Viewers · {list.length}</div>
      <div class="divider-list">
        {list.map((p) => {
          const s = peerStatus(p)
          return (
            <div class="peer-row" key={p.id}>
              <Avatar id={p.id} name={p.name} />
              <div class="grow col tight">
                <div class="row">
                  <span class="ellipsis">{p.name}</span>
                  {p.role === 'host' && <span class="tag neutral">Host</span>}
                  {p.cohost && <span class="tag warn">Co-host</span>}
                  {p.chatOff && <span class="tag neutral">chat hidden</span>}
                </div>
                <div class="row caption">
                  <span class={`tag ${s.cls}`}>{s.text}</span>
                  <span class={`ping ${pingClass(p.rtt)}`}>{p.rtt} ms</span>
                  {p.jit > 50 && <span class="muted">±{p.jit}</span>}
                  {p.q && <span class="muted">{p.q}</span>}
                  {p.device && <span class="muted ellipsis">{p.device}</span>}
                </div>
              </div>
              {p.role !== 'host' && (
                <>
                  <button
                    class="btn btn-sm btn-ghost"
                    onClick={() => socket.send({ t: 'host', op: 'chat_hide', sid: p.id, on: !p.chatOff })}
                    title="Hidden viewers don't see chat messages and can't post. Reactions still work."
                  >
                    {p.chatOff ? 'Show chat' : 'Hide chat'}
                  </button>
                  <button
                    class={`btn btn-sm ${p.cohost ? 'btn-primary' : 'btn-ghost'}`}
                    onClick={() => socket.send({ t: 'host', op: 'cohost', sid: p.id, on: !p.cohost })}
                    title="Co-hosts can play, pause, seek and start intermissions for everyone"
                  >
                    {p.cohost ? 'Co-host ✓' : 'Give control'}
                  </button>
                  <button
                    class="icon-btn sm plain danger"
                    onClick={() => confirm(`Remove ${p.name}?`) && socket.send({ t: 'host', op: 'kick', sid: p.id })}
                    title="Remove"
                  >
                    <Icon name="x" size={16} />
                  </button>
                </>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function RoomSettings() {
  const s = S.hostSettings.value
  if (!s) return null
  const set = (patch: Partial<S.HostSettings>) => socket.send({ t: 'host', op: 'settings', settings: { ...s, ...patch } })
  const toggles: [keyof S.HostSettings, string, string][] = [
    ['autoAdmit', 'Admit first-time opens automatically', 'Off is safest: you approve every new device. Second devices always need you.'],
    ['locked', 'Lock room', 'No new people can join. People already admitted can rejoin.'],
    ['waitForAll', 'Wait for everyone to buffer', 'If someone stalls, pause the room for a moment instead of leaving them behind.'],
    ['reactions', 'Show reactions on screen', ''],
  ]
  return (
    <div class="card">
      <div class="h18">Room</div>
      <label class="col tight">
        <span class="label dim">Room name</span>
        <input class="input" defaultValue={s.roomName} maxLength={40} onChange={(e) => set({ roomName: (e.target as HTMLInputElement).value })} />
      </label>
      {toggles.map(([k, label, hint]) => (
        <button class="toggle-row" key={k} onClick={() => set({ [k]: !s[k] } as Partial<S.HostSettings>)} role="switch" aria-checked={!!s[k]}>
          <span class="grow col tight left">
            <span class="dim">{label}</span>
            {hint && <span class="caption muted">{hint}</span>}
          </span>
          <span class={`switch ${s[k] ? 'on' : ''}`} />
        </button>
      ))}
      <label class="row">
        <span class="dim grow">Maximum guests</span>
        <input class="input num" type="number" min={1} max={50} value={s.maxGuests} onChange={(e) => set({ maxGuests: parseInt((e.target as HTMLInputElement).value) || 12 })} />
      </label>
      <button
        class="btn btn-danger"
        onClick={() => confirm('End the room? Every invite link stops working and everyone is disconnected.') && socket.send({ t: 'host', op: 'end_room' })}
      >
        End room & revoke all links
      </button>
    </div>
  )
}
