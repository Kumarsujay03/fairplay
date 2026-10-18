import { useEffect, useState } from 'preact/hooks'
import { ApiError, del, get, me, post, proof } from '../api'
import { socket } from '../socket'
import * as S from '../store'
import { Icon, Logo } from '../components/Icons'
import { LobbyEntry } from '../components/Lobby'
import { Avatar, peerStatus } from '../components/Peers'
import { copy, fmtBytes, fmtTime, navigate, pingClass } from '../util'

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
          <h1 class="h24">Host session ended</h1>
          <div class="dim">FairPlay was restarted, so the host key changed. Open the new host link from your terminal.</div>
          <button class="btn btn-ghost" onClick={() => { S.kicked.value = null; setPhase('login') }}>
            Enter the new key
          </button>
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

function Console() {
  const [items, setItems] = useState<Item[]>([])
  const [tokens, setTokens] = useState(loadTokens)
  const [, tick] = useState(0)

  const refreshLibrary = () =>
    get<{ items: Item[]; status: Record<string, S.MediaStatus> }>('/api/host/library')
      .then((r) => {
        setItems(r.items)
        S.library.value = { ...r.status, ...S.library.value }
      })
      .catch((e) => S.showToast(e.message, 'error'))

  useEffect(() => {
    refreshLibrary()
    get<{ tunnel: S.Tunnel; settings: S.HostSettings }>('/api/host/info').then((r) => {
      S.tunnel.value = r.tunnel
      S.hostSettings.value = r.settings
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
        <span class="grow" />
        <span class="pill tag neutral">
          <Icon name="lock" size={14} /> Host verified
        </span>
        <span class={`pill badge ${statusLive ? 'live' : 'wait'}`}>
          <span class="dot" />
          {statusLive ? `ROOM LIVE · ${fmtTime(pos)}` : st?.media ? `PAUSED · ${fmtTime(pos)}` : 'NO VIDEO'}
        </span>
        <button class="btn btn-primary" onClick={() => navigate('/room')}>
          <Icon name="play" size={16} /> Open player
        </button>
      </header>

      <main class="console-grid">
        <section class="col gap24">
          <NowPlaying cur={cur} items={items} onRefresh={refreshLibrary} />
          <Playback pos={pos} />
          <Share guests={guests.length} />
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

function NowPlaying({ cur, items, onRefresh }: { cur: S.MediaStatus | null; items: Item[]; onRefresh: () => void }) {
  const [filter, setFilter] = useState('')
  const lib = S.library.value
  const shown = items.filter((i) => i.path.toLowerCase().includes(filter.toLowerCase()))
  const curLive = cur ? lib[cur.id] || cur : null
  return (
    <div class="card">
      <div class="card-head">
        <div class="h18">Now playing</div>
        <button class="icon-btn sm plain" onClick={onRefresh} title="Rescan folder">
          <Icon name="refresh" size={18} />
        </button>
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
          <button class="btn btn-ghost btn-sm" onClick={() => socket.send({ t: 'host', op: 'next' })}>
            <Icon name="next" size={14} /> Play next now
          </button>
        </div>
      )}

      <div class="col">
        <input class="input" placeholder="Search your videos" value={filter} onInput={(e) => setFilter((e.target as HTMLInputElement).value)} />
        <div class="library">
          {shown.length === 0 && <div class="caption muted pad">No videos found in the media folder.</div>}
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
                <button class="btn btn-sm btn-ghost" onClick={() => socket.send({ t: 'host', op: 'queue_add', media: it.id })}>
                  Queue
                </button>
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
      {m.error && <div class="caption warn-text">{m.error}</div>}
      {m.notes?.map((n) => <div class="caption muted" key={n}>{n}</div>)}
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
            <button class="btn btn-danger" onClick={() => send('inter', { sec: 300 })} disabled={!m?.playable}>
              Intermission 5 min
            </button>
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
