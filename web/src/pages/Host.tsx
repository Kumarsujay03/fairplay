import { useEffect, useRef, useState } from 'preact/hooks'
import { ApiError, del, get, me, post, proof } from '../api'
import { socket } from '../socket'
import * as S from '../store'
import { Icon, Logo } from '../components/Icons'
import { LobbyEntry } from '../components/Lobby'
import { InviteQR } from '../components/QR'
import { ask } from '../components/Dialog'
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

  const endRoom = async () =>
    (await ask({ title: 'End the room?', message: 'Every invite link stops working and everyone is disconnected. FairPlay keeps running for new invites.', confirm: 'End room', danger: true })) &&
    socket.send({ t: 'host', op: 'end_room' })
  const stopFairPlay = async () => {
    if (!(await ask({ title: 'Stop FairPlay?', message: 'The room ends for everyone and FairPlay closes on this computer. Start it again with: fairplay', confirm: 'Stop FairPlay', danger: true }))) return
    try {
      await post('/api/host/shutdown')
    } catch {}
    S.kicked.value = 'FairPlay has stopped on this computer. You can close this tab. Start it again with: fairplay'
    setTimeout(() => socket.close(), 100)
  }

  const [tab, setTab] = useState<'show' | 'people' | 'settings'>('show')
  const lobby = S.lobby.value.length
  const tunnel = S.tunnel.value

  return (
    <div class="console">
      <header class="console-top">
        <div class="row brand">
          <Logo size={34} />
          <span class="console-name">Host console</span>
          <VersionTag version={version} upd={upd} />
        </div>
        <nav class="tabs" aria-label="Console sections">
          {(['show', 'people', 'settings'] as const).map((t) => (
            <button key={t} class={`tab ${tab === t ? 'on' : ''}`} onClick={() => setTab(t)}>
              {t === 'show' ? 'Show' : t === 'people' ? 'People' : 'Settings'}
              {t === 'people' && lobby > 0 && <span class="tab-dot" title={`${lobby} waiting to join`} />}
            </button>
          ))}
        </nav>
        <span class="grow" />
        <span class={`status-pill ${statusLive ? 'live' : st?.media ? 'paused' : 'idle'}`} title="Host verified on this computer">
          <span class="dot" />
          {statusLive ? `Live · ${fmtTime(pos)}` : st?.media ? `Paused · ${fmtTime(pos)}` : 'No video'}
        </span>
        <button class="btn btn-primary" onClick={() => navigate('/room')}>
          <Icon name="play" size={16} /> Open player
        </button>
        <RowMenu
          label="More"
          items={[
            { label: 'End room & revoke all links', hint: 'Everyone is disconnected; the room stays open for new invites', danger: true, onClick: endRoom },
            { label: 'Stop FairPlay', hint: 'Ends the room and closes FairPlay on this computer', danger: true, onClick: stopFairPlay },
          ]}
        />
      </header>

      <main class="console-scroll">
        <div class="console-body">
        {tab === 'show' && (
          <div class="show-grid">
            <div class="slot slot-banner">
              <Banner cur={cur} pos={pos} />
            </div>
            <div class="slot slot-lib">
              <Library
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
            </div>
            <div class="slot slot-people">
              <People tokens={tokens} setTokens={setTokens} compact onMore={() => setTab('people')} />
            </div>
            <div class="slot slot-room">
              <RoomSettings onShare={() => setTab('settings')} />
            </div>
          </div>
        )}
        {tab === 'people' && (
          <div class="console-cols two">
            <People tokens={tokens} setTokens={setTokens} />
            <div class="card settings-card">
              <Share guests={guests.length} />
            </div>
          </div>
        )}
        {tab === 'settings' && (
          <div class="console-cols two">
            <div class="card settings-card">
              <Share guests={guests.length} />
              <RoomSettings />
            </div>
            <div class="card settings-card">
              <Storage />
            </div>
          </div>
        )}
        </div>
      </main>

      <footer class="console-foot">
        <span>Event log on this computer</span>
        {S.media.value?.encoder && <span>Encoder {S.media.value.encoder}</span>}
        {(tunnel?.lan?.length || 0) > 0 && <span>Wi-Fi {tunnel!.lan![0].replace(/^https?:\/\//, '')}</span>}
        <span>Internet {tunnel?.state === 'up' ? 'on' : tunnel?.state === 'starting' ? 'starting…' : 'off'}</span>
        <span class="grow" />
        <button class="link-quiet" onClick={stopFairPlay}>Stop FairPlay</button>
        <span>{version}</span>
      </footer>
    </div>
  )
}

/** A small "⋯" menu. Closes on a click anywhere else or on Escape. */
function RowMenu({ items, label = 'Actions' }: { items: { label: string; hint?: string; danger?: boolean; onClick: () => unknown }[]; label?: string }) {
  const [open, setOpen] = useState(false)
  // The menu floats above the page (fixed position), so a card edge or the
  // scrolling area never cuts it off; it opens upward when there is no room below.
  const [pos, setPos] = useState<{ top?: number; bottom?: number; right: number; maxHeight: number }>({ right: 0, maxHeight: 400 })
  const btn = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (!open) return
    const close = () => setOpen(false)
    const key = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    addEventListener('click', close)
    addEventListener('keydown', key)
    addEventListener('resize', close)
    addEventListener('scroll', close, true)
    return () => {
      removeEventListener('click', close)
      removeEventListener('keydown', key)
      removeEventListener('resize', close)
      removeEventListener('scroll', close, true)
    }
  }, [open])
  const toggle = () => {
    if (open) return setOpen(false)
    const r = btn.current!.getBoundingClientRect()
    const need = items.reduce((h, it) => h + (it.hint ? 58 : 38), 14)
    const below = innerHeight - r.bottom - 12
    const above = r.top - 12
    const right = Math.max(8, innerWidth - r.right)
    if (below >= need || below >= above) setPos({ top: r.bottom + 6, right, maxHeight: below })
    else setPos({ bottom: innerHeight - r.top + 6, right, maxHeight: above })
    setOpen(true)
  }
  if (!items.length) return null
  return (
    <div class="menu-wrap" onClick={(e) => e.stopPropagation()}>
      <button ref={btn} class={`icon-btn sm plain ${open ? 'active' : ''}`} onClick={toggle} aria-label={label} title={label} aria-haspopup="menu" aria-expanded={open}>
        <Icon name="more" size={18} />
      </button>
      {open && (
        <div class="dropdown" role="menu" style={{ top: pos.top, bottom: pos.bottom, right: pos.right, maxHeight: pos.maxHeight }}>
          {items.map((it) => (
            <button
              key={it.label}
              role="menuitem"
              class={`dropdown-item ${it.danger ? 'danger' : ''}`}
              onClick={() => {
                setOpen(false)
                it.onClick()
              }}
            >
              <span>{it.label}</span>
              {it.hint && <span class="caption muted">{it.hint}</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/** The film the room is showing, as a cinematic banner: title and tracks on
 * the picture, the transport that moves the global timer docked at its foot. */
function Banner({ cur, pos }: { cur: S.MediaStatus | null; pos: number }) {
  const lib = S.library.value
  const m = cur ? lib[cur.id] || cur : null
  const pct = m && m.duration ? Math.min(100, (m.encoded / m.duration) * 100) : 0
  return (
    <section class={`banner ${m ? '' : 'empty'}`}>
      <div class="banner-scrim" />
      <div class="banner-body">
        {m ? (
          <>
            <div class="banner-head">
              <div class="col tight grow">
                <span class="eyebrow">Now showing</span>
                <h1 class="banner-title ellipsis">{m.name}</h1>
                <div class="row wrap caption banner-meta">
                  <span class="dim">
                    {fmtTime(m.duration)}
                    {m.width ? ` · ${m.width}×${m.height}` : ''}
                  </span>
                  {m.state === 'ready' && <span class="tag ok"><Icon name="check" size={12} /> Ready</span>}
                  {m.state === 'encoding' && <span class="tag warn">Preparing {pct.toFixed(0)}% · {m.speed?.toFixed(1)}×</span>}
                  {m.state === 'probing' && <span class="tag neutral">Reading file…</span>}
                  {m.state === 'error' && <span class="tag bad">Error</span>}
                  {m.playable && m.state !== 'ready' && <span class="tag ok">Playable now</span>}
                  {m.rungs && m.rungs.length > 0 && <span class="tag glass">{m.rungs.join(' · ')}</span>}
                  {(m.audio?.length || 0) > 1 && <span class="tag glass">{m.audio!.map((a) => a.label).slice(0, 3).join(' · ')}</span>}
                  {(m.subs?.length || 0) > 0 && <span class="tag glass">{m.subs!.length} subtitles</span>}
                </div>
                {m.state === 'encoding' && <EncoderLead m={m} />}
                {m.error && <div class="caption warn-text">{m.error}</div>}
              </div>
              <div class="row wrap banner-actions">
                {m.playable && S.media.value?.id === m.id && <RoomTracks m={m} glass />}
                <button
                  class="btn btn-glass btn-sm"
                  title="Stop the video for everyone and stop preparing it"
                  onClick={async () =>
                    (await ask({ title: 'Stop this video?', message: `“${m.name}” stops for everyone and stops preparing. You can pick it again from the library.`, confirm: 'Stop & clear', danger: true })) &&
                    socket.send({ t: 'host', op: 'clear' })
                  }
                >
                  Stop &amp; clear
                </button>
              </div>
            </div>
            <Playback pos={pos} />
          </>
        ) : (
          <div class="banner-empty">
            <span class="eyebrow">Now showing</span>
            <h1 class="banner-title">Nothing yet</h1>
            <p class="dim">Pick a video from the library below. It starts preparing right away, and the room can start after a few seconds.</p>
          </div>
        )}
      </div>
    </section>
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

function Library({
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
  return (
    <div class="card">
      <div class="card-head">
        <div class="h18">Library</div>
        <div class="row">
          {!picking && (
            <button class="btn btn-quiet btn-sm" onClick={() => setPicking(true)} title={root}>
              Change folder
            </button>
          )}
          <button class="icon-btn sm plain" onClick={onRefresh} title="Rescan folder">
            <Icon name="refresh" size={18} />
          </button>
        </div>
      </div>
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
                {queuedAt(it.id) >= 0 && <span class="tag warn">queued</span>}
                <button class="icon-btn sm play-now" onClick={() => socket.send({ t: 'host', op: 'load', media: it.id })} title="Show this now" aria-label="Show this now">
                  <Icon name="play" size={16} />
                </button>
                <RowMenu
                  items={
                    queuedAt(it.id) >= 0
                      ? [{ label: 'Remove from queue', onClick: () => socket.send({ t: 'host', op: 'queue_remove', index: queuedAt(it.id) }) }]
                      : [{ label: 'Add to queue', hint: 'Plays after the current film', onClick: () => socket.send({ t: 'host', op: 'queue_add', media: it.id }) }]
                  }
                />
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

/** The host's choice of audio, subtitles and quality for everyone. Viewers
 * follow it unless they pick their own in the player menu. */
function RoomTracks({ m, glass = false }: { m: S.MediaStatus; glass?: boolean }) {
  const p = S.prefs.value
  const set = (patch: Partial<S.TrackPrefs>) => socket.send({ t: 'host', op: 'prefs', prefs: { ...p, ...patch } })
  const audio = m.audio || []
  const subs = m.subs || []
  return (
    <div class={`room-tracks ${glass ? 'glass' : ''}`}>
      {!glass && <div class="label dim">Picture and sound for everyone</div>}
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
            <option value="auto">Auto (per device)</option>
            {(m.rungs || []).map((r) => (
              <option key={r} value={r}>
                {qualityLabel(r)}
              </option>
            ))}
          </select>
        </label>
      </div>
      {!glass && <div class="caption muted">Viewers start with these and can change them for their own screen. Only you control play, pause and intermission.</div>}
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
      <button class="btn btn-glass btn-sm" onClick={() => onStart(commit(min))} disabled={disabled}>
        Intermission
      </button>
      <label class="row inter-len" title="Intermission length in minutes, 1 to 120">
        <input
          class="input num glass"
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
    <div class="transport col">
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
      <div class="row wrap">
        <button class="icon-btn glass" onClick={() => send('seek', { pos: pos - 10 })} title="Back 10s for everyone">
          <Icon name="back10" />
        </button>
        <button
          class="big-play sm"
          onClick={() => send(st?.playing ? 'pause' : 'play')}
          disabled={!m?.playable}
          title={st?.playing ? 'Pause for everyone' : 'Play for everyone'}
          aria-label={st?.playing ? 'Pause for everyone' : 'Play for everyone'}
        >
          <Icon name={st?.playing ? 'pause' : 'play'} size={22} />
        </button>
        <button class="icon-btn glass" onClick={() => send('seek', { pos: pos + 10 })} title="Forward 10s for everyone">
          <Icon name="fwd10" />
        </button>
        <span class="mono">
          {fmtTime(pos)} / {fmtTime(dur)}
        </span>
        <span class="grow" />
        {st?.reason === 'intermission' ? (
          <button class="btn btn-primary btn-sm" onClick={() => send('inter_end')}>
            End intermission
          </button>
        ) : (
          <>
            <IntermissionStart disabled={!m?.playable} onStart={(min) => send('inter', { sec: min * 60 })} />
            <button class="btn btn-glass btn-sm" onClick={() => send('inter', { sec: 0 })} disabled={!m?.playable}>
              Pause for all
            </button>
          </>
        )}
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
    if (!(await ask({ title: 'Free up space?', message: 'Prepared videos are deleted. The one playing now is kept; the others are prepared again when played.', confirm: 'Delete', danger: true }))) return
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
    <details class="settings-section" open>
      <summary class="section-title row">
        <span class="grow">Storage</span>
        <span class="caption muted">{fmtBytes(info.bytes)}</span>
        <Icon name="chevron" size={16} class="muted chev" />
      </summary>
      <div class="col section-body">
      <div class="row" style={{ justifyContent: 'flex-end' }}>
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
    </details>
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
    <div class="settings-section col">
      <div class="section-title">Sharing</div>
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

function People({ tokens, setTokens, compact = false, onMore }: { tokens: Record<string, string>; setTokens: (t: Record<string, string>) => void; compact?: boolean; onMore?: () => void }) {
  const [label, setLabel] = useState('')
  const [hours, setHours] = useState(24)
  const t = S.tunnel.value

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

  // A revoked or expired invite comes back with a fresh link for the same
  // person: valid 24 h, not bound to any device yet; the old link stays dead.
  const reinvite = async (inv: S.InviteView) => {
    try {
      const r = await post<{ token: string }>(`/api/host/invites/${inv.id}/regen`)
      saveToken(inv.id, r.token)
      setTokens(loadTokens())
      S.invites.value = await get<S.InviteView[]>('/api/host/invites')
      const link = `${base(t?.state === 'up' ? 'net' : 'lan')}/j/${inv.id}#t=${r.token}`
      S.showToast((await copy(link)) ? `New invite for ${inv.label} copied` : `New invite for ${inv.label} is ready`)
    } catch (err) {
      S.showToast((err as ApiError).message, 'error')
    }
  }

  const revoke = async (id: string, label: string) => {
    if (!(await ask({ title: `Revoke ${label}'s invite?`, message: 'The link stops working and they are removed from the room.', confirm: 'Revoke', danger: true }))) return
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

  const peers = S.peers.value
  const invites = S.invites.value
  const peerFor = (inv: S.InviteView) => peers.find((p) => p.name.toLowerCase() === (inv.name || inv.label).toLowerCase())
  const matched = new Set<string>()
  type Row = { key: string; name: string; inv?: S.InviteView; peer?: S.Peer }
  const rows: Row[] = []
  for (const p of peers.filter((p) => p.role === 'host')) rows.push({ key: p.id, name: p.name, peer: p })
  for (const inv of invites) {
    const peer = peerFor(inv)
    if (peer) matched.add(peer.id)
    rows.push({ key: inv.id, name: inv.label, inv, peer })
  }
  for (const p of peers) if (p.role !== 'host' && !matched.has(p.id)) rows.push({ key: p.id, name: p.name, peer: p })
  const watching = peers.length

  const inviteStatus = (inv: S.InviteView) =>
    inv.revoked
      ? { t: 'Revoked', c: 'bad' }
      : inv.expired
        ? { t: 'Expired', c: 'bad' }
        : inv.admitted
          ? { t: 'Joined before', c: 'neutral' }
          : inv.bound
            ? { t: 'Opened, waiting', c: 'warn' }
            : { t: 'Not opened yet', c: 'neutral' }

  const shownRows = compact ? rows.slice(0, 4) : rows
  const pending = S.lobby.value
  return (
    <div class="card">
      <div class="card-head">
        <div class="row">
          <div class="h18">People</div>
          <span class="tag ok">{watching} watching</span>
        </div>
        {compact && rows.length > shownRows.length && onMore && (
          <button class="btn btn-quiet btn-sm" onClick={onMore}>
            See all {rows.length}
          </button>
        )}
      </div>
      {pending.length > 0 && (
        <div class="col lobby-inline">
          <div class="label dim">Waiting to join</div>
          {pending.map((p) => (
            <LobbyEntry key={p.id} p={p} />
          ))}
        </div>
      )}
      <form class="row invite-form" onSubmit={create}>
        <input class="input grow" placeholder="Invite someone by name" value={label} maxLength={24} onInput={(e) => setLabel((e.target as HTMLInputElement).value)} />
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
        {rows.length === 0 && <div class="caption muted pad">No one yet. Invite someone above; the link is copied for you to send.</div>}
        {shownRows.map(({ key, name, inv, peer }) => {
          const ps = peer ? peerStatus(peer) : null
          const status = ps ? { t: ps.text, c: ps.cls } : inv ? inviteStatus(inv) : { t: '', c: 'neutral' }
          const has = inv ? !!tokens[inv.id] : false
          const live = inv && !inv.revoked && !inv.expired
          const items: { label: string; hint?: string; danger?: boolean; onClick: () => unknown }[] = []
          if (inv && live && has) {
            if (t?.state === 'up') items.push({ label: 'Copy internet link', onClick: () => doCopy(inv.id, 'net') })
            if ((t?.lan?.length || 0) > 0) items.push({ label: 'Copy Wi-Fi link', onClick: () => doCopy(inv.id, 'lan') })
            items.push({ label: 'Show QR code', hint: 'Scan it with a phone camera', onClick: () => setQrFor(inv) })
          }
          if (inv && live && !has) items.push({ label: 'New link', hint: 'Links are shown once; the old one stops working', onClick: () => regen(inv.id) })
          if (peer && peer.role !== 'host') {
            items.push({ label: peer.chatOff ? 'Show chat' : 'Hide chat', hint: 'Hidden viewers see no messages and cannot post', onClick: () => socket.send({ t: 'host', op: 'chat_hide', sid: peer.id, on: !peer.chatOff }) })
            items.push({ label: peer.cohost ? 'Take back control' : 'Give control', hint: 'Co-hosts can play, pause, seek and start intermissions', onClick: () => socket.send({ t: 'host', op: 'cohost', sid: peer.id, on: !peer.cohost }) })
            items.push({ label: 'Remove from room', danger: true, onClick: async () => (await ask({ title: `Remove ${peer.name}?`, message: 'They are disconnected from the room. Their invite still works unless you revoke it.', confirm: 'Remove', danger: true })) && socket.send({ t: 'host', op: 'kick', sid: peer.id }) })
          }
          if (inv && !inv.revoked) items.push({ label: 'Revoke invite', danger: true, onClick: () => revoke(inv.id, inv.label) })
          if (inv && (inv.revoked || inv.expired)) items.unshift({ label: 'Invite again', hint: 'A new link for the same person, valid 24 h. The old link stays dead.', onClick: () => reinvite(inv) })
          if (inv && inv.revoked) items.push({ label: 'Delete', onClick: () => remove(inv.id) })
          return (
            <div class="person-row" key={key}>
              <Avatar id={peer?.id || inv!.id} name={name} />
              <div class="grow col tight">
                <div class="row">
                  <span class="ellipsis">{name}</span>
                  {inv && inv.name && inv.name !== inv.label && <span class="caption muted ellipsis">as {inv.name}</span>}
                  {peer?.role === 'host' && <span class="tag neutral">Host</span>}
                  {peer?.cohost && <span class="tag warn">Co-host</span>}
                  {peer?.chatOff && <span class="tag neutral">chat hidden</span>}
                </div>
                <div class="row caption">
                  {peer ? (
                    <>
                      <span class={`tag ${status.c}`}>{status.t}</span>
                      <span class={`ping ${pingClass(peer.rtt)}`}>{peer.rtt} ms</span>
                      {peer.q && <span class="muted">{peer.q}</span>}
                      {peer.device && <span class="muted ellipsis">{peer.device}</span>}
                    </>
                  ) : (
                    <span class={`tag-text ${status.c}`}>{status.t}</span>
                  )}
                </div>
              </div>
              <RowMenu items={items} />
            </div>
          )
        })}
      </div>
      {qrFor && <InviteQR label={qrFor.label} links={qrLinks(qrFor.id)} onClose={() => setQrFor(null)} />}
      {!compact && (
        <div class="caption muted">
          One link per person. It locks to the first device that opens it, and every first join waits for you in the lobby. Forwarded links show up flagged.
        </div>
      )}
    </div>
  )
}

function RoomSettings({ onShare }: { onShare?: () => void } = {}) {
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
    <div class={onShare ? 'card' : 'settings-section col'}>
      <div class="card-head">
        <div class={onShare ? 'h18' : 'section-title'}>Room</div>
        {onShare && (
          <button class="btn btn-quiet btn-sm" onClick={onShare}>
            Share links
          </button>
        )}
      </div>
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
    </div>
  )
}
