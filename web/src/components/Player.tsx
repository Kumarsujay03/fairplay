import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import type Hls from 'hls.js'
import * as S from '../store'
import { socket } from '../socket'
import { SyncEngine, type SyncReport } from '../sync'
import { Icon } from './Icons'
import { Chat } from './Chat'
import { Reactions } from './Reactions'
import { Peers, ViewerStack } from './Peers'
import { LobbyToasts } from './Lobby'
import { fmtGap, fmtTime, navigate, pingClass, qualityLabel, qualityName } from '../util'

interface Level {
  index: number
  name: string
}

export function Player() {
  const wrap = useRef<HTMLDivElement>(null)
  const videoRef = useRef<HTMLVideoElement>(null)
  const engine = useRef<SyncEngine | null>(null)
  const hlsRef = useRef<Hls | null>(null)

  const [report, setReport] = useState<SyncReport>({ target: 0, drift: 0, buffering: false })
  const [cur, setCur] = useState(0)
  const [paused, setPaused] = useState(true)
  const [buffered, setBuffered] = useState(0)
  const [muted, setMuted] = useState(false)
  const [volume, setVolume] = useState(1)
  const [blocked, setBlocked] = useState(false)
  const [soundOff, setSoundOff] = useState(false) // started muted because the browser blocked sound
  const [levels, setLevels] = useState<Level[]>([])
  const [autoLevel, setAutoLevel] = useState('')
  const [audioCount, setAudioCount] = useState(0)
  // Personal overrides. null = follow the host's choice for the room.
  const [myQ, setMyQ] = useState<string | null>(null)
  const [myAudio, setMyAudio] = useState<number | null>(null)
  const [mySub, setMySub] = useState<string | null>(null)
  const room = S.prefs.value
  const quality = myQ ?? room.quality
  const qualityRef = useRef(quality)
  qualityRef.current = quality
  const audio = myAudio ?? room.audio
  const sub = mySub ?? room.sub
  const [menu, setMenu] = useState(false)
  const [peersOpen, setPeersOpen] = useState(false)
  const [chatOpen, setChatOpen] = useState(true)
  const [showUi, setShowUi] = useState(true)
  const lastTouch = useRef(0)
  const [isFs, setIsFs] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)
  const [inPip, setInPip] = useState(false)
  const pipSupported =
    (typeof document !== 'undefined' && !!document.pictureInPictureEnabled) ||
    typeof (HTMLVideoElement.prototype as any).webkitSetPresentationMode === 'function'
  const [portraitPhone, setPortraitPhone] = useState(() => matchMedia('(max-width: 720px) and (orientation: portrait)').matches)

  // Fullscreen always uses the cinema layout (chat floating over the video).
  const compact = portraitPhone && !isFs
  const touch = typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches
  const st = S.room.value
  const m = S.media.value
  const me = S.you.value
  const canControl = !!me?.control
  const mode = S.mode.value
  const duration = m?.duration || 0
  const playable = !!m?.playable && m.id === st?.media
  const ping = S.ping.value

  // ---- engine lifecycle ----
  useEffect(() => {
    const v = videoRef.current!
    const e = new SyncEngine(v, socket.clock, () => S.room.value, () => S.media.value?.duration || 0)
    e.onReport = setReport
    e.onBlocked = setBlocked
    e.onMutedStart = () => setSoundOff(true)
    // Stuck while the room plays: restart loading at the room position, and
    // rebuild the player if that doesn't help.
    e.onStall = (n) => {
      const hls = hlsRef.current
      if (hls && bufferedAhead(v) > 2) {
        // The video is downloaded but the picture doesn't move: this computer
        // can't decode it fast enough.
        lighten()
        return
      }
      if (hls && n <= 2) {
        hls.stopLoad()
        if (n === 2) hls.recoverMediaError()
        hls.startLoad(e.target())
      } else {
        setReloadKey((k) => k + 1)
      }
    }
    engine.current = e
    e.start()

    const onPause = () => {
      setPaused(true)
      // Pauses from outside our UI (media keys, PiP window) count as the user's.
      // Pauses the browser makes on its own (a hidden tab, a stalled or
      // reloaded stream) don't: they would pause the whole room for a host.
      if (!e.isOwnAction() && !v.ended && !document.hidden && v.readyState >= 3 && e.mode === 'follow' && S.room.value?.playing) userPause()
    }
    const onPlay = () => setPaused(false)
    const onTime = () => {
      setCur(v.currentTime)
      const b = v.buffered
      for (let i = 0; i < b.length; i++) {
        if (b.start(i) <= v.currentTime + 0.5 && b.end(i) >= v.currentTime) {
          setBuffered(b.end(i))
          break
        }
      }
    }
    const onVol = () => {
      setMuted(v.muted)
      setVolume(v.volume)
    }
    v.addEventListener('pause', onPause)
    v.addEventListener('play', onPlay)
    v.addEventListener('timeupdate', onTime)
    v.addEventListener('volumechange', onVol)
    const onPipIn = () => setInPip(true)
    const onPipOut = () => setInPip(false)
    v.addEventListener('enterpictureinpicture', onPipIn)
    v.addEventListener('leavepictureinpicture', onPipOut)
    try {
      const saved = parseFloat(localStorage.getItem('fp.volume') || '')
      if (saved >= 0 && saved <= 1) v.volume = saved
    } catch {}

    const mq = matchMedia('(max-width: 720px) and (orientation: portrait)')
    const onMq = () => setPortraitPhone(mq.matches)
    mq.addEventListener('change', onMq)
    const onFs = () => {
      const on = !!document.fullscreenElement
      setIsFs(on)
      if (!on) (screen.orientation as any)?.unlock?.()
    }
    document.addEventListener('fullscreenchange', onFs)
    // Coming back to a background tab: catch up at once.
    const onVis = () => {
      if (!document.hidden) e.tick(true)
    }
    document.addEventListener('visibilitychange', onVis)

    // Report our status to the room once a second.
    const statusTimer = setInterval(() => sendStatus(), 1000)
    return () => {
      clearInterval(statusTimer)
      e.destroy()
      v.removeEventListener('pause', onPause)
      v.removeEventListener('play', onPlay)
      v.removeEventListener('timeupdate', onTime)
      v.removeEventListener('volumechange', onVol)
      v.removeEventListener('enterpictureinpicture', onPipIn)
      v.removeEventListener('leavepictureinpicture', onPipOut)
      mq.removeEventListener('change', onMq)
      document.removeEventListener('fullscreenchange', onFs)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [])

  const sendStatus = () => {
    const v = videoRef.current
    const e = engine.current
    if (!v || !e) return
    const r = { target: e.target(), drift: v.currentTime - e.target() }
    const hls = hlsRef.current
    const lv = hls && hls.currentLevel >= 0 ? hls.levels[hls.currentLevel] : null
    socket.send({
      t: 'status',
      mode: e.mode,
      buf: v.readyState < 3 || v.seeking,
      pos: v.currentTime,
      rtt: socket.clock.rtt,
      jit: socket.clock.jitter,
      drift: Math.round(r.drift * 1000),
      q: lv ? qualityName(lv.width, lv.height) : '',
    })
  }

  // ---- picture lag ----
  // The clock (driven by the sound) can be exactly on time while the picture
  // on screen is seconds behind it, when this computer can't decode the
  // video fast enough (for example while it is also preparing the video).
  // Watch which frame is actually shown, and when the picture falls behind,
  // switch to a lighter quality and restart the decoder at the right spot.
  useEffect(() => {
    const v = videoRef.current as HTMLVideoElement & {
      requestVideoFrameCallback?: (cb: (now: number, meta: { mediaTime: number }) => void) => number
      cancelVideoFrameCallback?: (id: number) => void
    }
    if (!v.requestVideoFrameCallback) return
    let id = 0
    let behindSince = 0
    let lastFrame = performance.now()
    const onFrame = (_now: number, meta: { mediaTime: number }) => {
      lastFrame = performance.now()
      const lag = v.currentTime - meta.mediaTime
      if (!v.paused && !v.seeking && lag > 0.6) {
        if (!behindSince) behindSince = lastFrame
        else if (lastFrame - behindSince > 2500) {
          behindSince = 0
          lighten()
        }
      } else {
        behindSince = 0
      }
      id = v.requestVideoFrameCallback!(onFrame)
    }
    id = v.requestVideoFrameCallback(onFrame)
    // No new frame at all for a while, although the clock runs: same cure.
    const timer = setInterval(() => {
      if (!v.paused && !v.seeking && v.readyState >= 3 && performance.now() - lastFrame > 3000) {
        lastFrame = performance.now()
        lighten()
      }
    }, 1000)
    return () => {
      v.cancelVideoFrameCallback?.(id)
      clearInterval(timer)
    }
  }, [])

  /** One quality lighter (for this viewer only), and a fresh decoder. */
  function lighten() {
    const v = videoRef.current!
    const hls = hlsRef.current
    if (hls) {
      const cur = hls.currentLevel >= 0 ? hls.currentLevel : hls.loadLevel
      if (cur > 0) {
        hls.autoLevelCapping = cur - 1
        hls.currentLevel = cur - 1 // switch now, dropping the backlog
        // Back to automatic choice (now capped) unless a quality was picked.
        if (qualityRef.current === 'auto') setTimeout(() => (hls.loadLevel = -1), 500)
        S.showToast('This computer is busy, using a lighter quality so the picture keeps up')
        return
      }
    }
    // Already the lightest: a seek restarts the decoder on the current frame.
    v.currentTime = v.currentTime + 0.05
  }

  // ---- media source ----
  useEffect(() => {
    const v = videoRef.current!
    if (!playable || !m) return
    const src = `/hls/${m.id}/master.m3u8`
    let cancelled = false
    let hls: Hls | null = null
    setLevels([])
    setMyQ(null)
    setMyAudio(null)
    setMySub(null)

    const useNative = !('MediaSource' in window || 'ManagedMediaSource' in window) && v.canPlayType('application/vnd.apple.mpegurl')
    if (useNative) {
      v.src = src
      v.addEventListener('loadedmetadata', () => engine.current?.tick(true), { once: true })
    } else {
      import('hls.js').then(({ default: HlsCtor }) => {
        if (cancelled) return
        hls = new HlsCtor({
          enableWorker: true,
          startPosition: engine.current?.target() ?? 0,
          maxBufferLength: 30,
          maxMaxBufferLength: 60,
          backBufferLength: 30,
          capLevelToPlayerSize: true, // phones settle at 720p, laptops at 1080p (pixel density counted)
          ignoreDevicePixelRatio: false,
          liveDurationInfinity: false,
          abrEwmaDefaultEstimate: 3_000_000,
          capLevelOnFPSDrop: true, // a busy computer that drops frames gets a lighter quality
        })
        hlsRef.current = hls
        hls.on(HlsCtor.Events.MANIFEST_PARSED, () => {
          setLevels(
            hls!.levels
              .map((l, i) => ({ index: i, name: qualityName(l.width, l.height), w: l.width }))
              .sort((a, b) => b.w - a.w)
              .map(({ index, name }) => ({ index, name })),
          )
          engine.current?.tick(true)
        })
        hls.on(HlsCtor.Events.AUDIO_TRACKS_UPDATED, () => setAudioCount(hls!.audioTracks.length))
        hls.on(HlsCtor.Events.LEVEL_SWITCHED, (_e, d) => {
          const l = hls!.levels[d.level]
          if (l) setAutoLevel(qualityName(l.width, l.height))
        })
        let recover = 0
        let lastMediaError = 0
        hls.on(HlsCtor.Events.ERROR, (_e, d) => {
          console.warn('FairPlay playback:', d.type, d.details, d.fatal ? 'fatal' : '', d.reason || d.error?.message || '', d.frag ? `level ${d.frag.level} frag ${d.frag.sn} @${d.frag.start.toFixed(1)}s` : '')
          if (!d.fatal) return
          if (d.type === HlsCtor.ErrorTypes.NETWORK_ERROR) {
            setTimeout(() => hls?.startLoad(), 1000)
            return
          }
          if (d.type === HlsCtor.ErrorTypes.MEDIA_ERROR) {
            // Up to three recoveries a minute; a two-hour film may hit a few.
            if (performance.now() - lastMediaError > 60_000) recover = 0
            lastMediaError = performance.now()
            if (recover++ < 3) {
              hls!.recoverMediaError()
              return
            }
          }
          S.showToast('Playback error, reloading the stream', 'error')
          setTimeout(() => setReloadKey((k) => k + 1), 1500)
        })
        hls.loadSource(src)
        hls.attachMedia(v)
      })
    }
    return () => {
      cancelled = true
      hls?.destroy()
      hlsRef.current = null
      v.removeAttribute('src')
      v.load()
    }
  }, [m?.id, playable, reloadKey])

  // Quality: the host's room choice or your own; "auto" lets bandwidth decide.
  useEffect(() => {
    const hls = hlsRef.current
    if (!hls || !levels.length) return
    if (quality === 'auto') {
      // Phones and tablets stop at 720p: it looks the same on a small screen
      // and costs far less battery and data. Laptops and TVs go up to 1080p.
      // Picking a quality by hand still allows anything.
      const sd = hls.levels.reduce((best, l, i) => (l.width <= 1280 && (best < 0 || l.width > hls.levels[best].width) ? i : best), -1)
      hls.autoLevelCapping = touch && sd >= 0 ? sd : -1
      if (!hls.autoLevelEnabled) hls.currentLevel = -1
      return
    }
    const l = levels.find((x) => x.name === quality)
    if (l && hls.currentLevel !== l.index) hls.currentLevel = l.index
    else if (!l && !hls.autoLevelEnabled) hls.currentLevel = -1
  }, [quality, levels])

  // Audio track: the host's room choice or your own (-1 keeps the file's default).
  useEffect(() => {
    const hls = hlsRef.current
    if (!hls || audio < 0 || audio >= hls.audioTracks.length || hls.audioTrack === audio) return
    hls.audioTrack = audio
  }, [audio, audioCount])

  // Subtitle selection
  useEffect(() => {
    const v = videoRef.current!
    for (let i = 0; i < v.textTracks.length; i++) {
      const t = v.textTracks[i]
      t.mode = t.id === sub || (t as any).label + '' === sub ? 'showing' : 'disabled'
    }
  }, [sub, m?.subs?.length])

  // A solo viewer who catches up with the room (by playing, or because the
  // room paused) rejoins it automatically, so nobody drifts ahead.
  const wasBehind = useRef(false)
  useEffect(() => {
    if (mode !== 'solo' || canControl || !S.room.value?.media) {
      wasBehind.current = false
      return
    }
    // Read the element itself: right after a seek the `cur` state is stale.
    const t = videoRef.current?.currentTime ?? 0
    const gap = report.target - t
    if (gap > 1.5) wasBehind.current = true
    if (wasBehind.current && !paused && gap <= 0.25) {
      wasBehind.current = false
      setMode('follow')
      S.showToast('You caught up with the room')
    }
  }, [cur, report.target, mode, paused, canControl])

  // Auto-hide controls while playing
  useEffect(() => {
    if (!showUi || paused || menu || peersOpen) return
    const t = setTimeout(() => setShowUi(false), 3000)
    return () => clearTimeout(t)
  }, [showUi, paused, menu, peersOpen, cur > 0])

  // Keyboard shortcuts
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest('input, textarea, select')) return
      const v = videoRef.current!
      switch (e.key) {
        case ' ':
        case 'k':
          e.preventDefault()
          togglePlay()
          break
        case 'ArrowLeft':
        case 'j':
          skip(-10)
          break
        case 'ArrowRight':
        case 'l':
          skip(10)
          break
        case 'm':
          v.muted = !v.muted
          break
        case 'f':
          toggleFs()
          break
        case 'c':
          setChatOpen((o) => !o)
          break
        case 'g':
          backToLive()
          break
        case 'ArrowUp':
          e.preventDefault()
          v.volume = Math.min(1, v.volume + 0.1)
          break
        case 'ArrowDown':
          e.preventDefault()
          v.volume = Math.max(0, v.volume - 0.1)
          break
        default:
          return
      }
      setShowUi(true)
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  })

  // ---- actions ----
  const setMode = (next: 'follow' | 'solo') => {
    S.mode.value = next
    if (next === 'follow') engine.current?.follow()
    else engine.current?.solo()
    sendStatus()
  }

  function userPause() {
    const v = videoRef.current!
    if (canControl) {
      socket.send({ t: 'cmd', op: 'pause' })
      return
    }
    setMode('solo')
    v.pause()
  }

  function togglePlay() {
    const v = videoRef.current!
    if (blocked) return unblock()
    if (canControl && S.mode.value === 'follow') {
      socket.send({ t: 'cmd', op: S.room.value?.playing ? 'pause' : 'play' })
      return
    }
    if (S.mode.value === 'follow') {
      if (S.room.value?.playing) userPause()
      return
    }
    if (v.paused) v.play().catch(() => {})
    else v.pause()
  }

  // How far a viewer may go. The host and co-hosts can go anywhere that is
  // encoded; everyone else can go back but never ahead of the room, where
  // chunks may not exist yet (and it would spoil the film).
  function seekLimit(): number {
    const encodedMax = m && m.state !== 'ready' ? Math.max(0, m.encoded - 2) : duration || Infinity
    if (canControl) return encodedMax
    return Math.min(encodedMax, engine.current?.target() ?? 0)
  }

  function seekTo(t: number) {
    const v = videoRef.current!
    const limit = seekLimit()
    t = Math.max(0, Math.min(t, limit))
    if (canControl && S.mode.value === 'follow') {
      socket.send({ t: 'cmd', op: 'seek', pos: t })
      return
    }
    if (!canControl && t >= limit - 1) {
      // Asked to go to (or past) where the room is: just rejoin it.
      if (S.mode.value === 'solo') backToLive()
      return
    }
    setMode('solo')
    v.currentTime = t
  }

  function skip(d: number) {
    const base = canControl && S.mode.value === 'follow' ? engine.current!.target() : videoRef.current!.currentTime
    seekTo(base + d)
  }

  function backToLive() {
    setMode('follow')
    S.showToast('Back with the room')
  }

  function unmute() {
    const v = videoRef.current!
    v.muted = false
    setSoundOff(false)
  }

  function unblock() {
    const v = videoRef.current!
    v.play()
      .then(() => engine.current?.unblock())
      .catch(() => {
        v.muted = true
        v.play().then(() => engine.current?.unblock()).catch(() => {})
      })
  }

  function toggleFs() {
    const el = wrap.current as any
    const v = videoRef.current as any
    if (document.fullscreenElement) {
      document.exitFullscreen()
      return
    }
    if (el.requestFullscreen) {
      el.requestFullscreen({ navigationUI: 'hide' })
        .then(() => {
          // Phones: turn sideways for the movie. Desktops reject this, which is fine.
          if (matchMedia('(pointer: coarse)').matches) (screen.orientation as any)?.lock?.('landscape').catch(() => {})
        })
        .catch(() => {})
    } else if (v.webkitEnterFullscreen) v.webkitEnterFullscreen() // iPhone: the system player
  }

  async function togglePip() {
    const v = videoRef.current! as HTMLVideoElement & {
      webkitSetPresentationMode?: (m: string) => void
      webkitPresentationMode?: string
    }
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture()
      else if (typeof v.requestPictureInPicture === 'function' && document.pictureInPictureEnabled) await v.requestPictureInPicture()
      else if (v.webkitSetPresentationMode) {
        // Safari on iPhone and iPad
        v.webkitSetPresentationMode(v.webkitPresentationMode === 'picture-in-picture' ? 'inline' : 'picture-in-picture')
      }
    } catch (err) {
      S.showToast(v.readyState < 1 ? 'Picture in picture works once the video is playing' : `Picture in picture is not available: ${(err as Error).message}`, 'error')
    }
  }

  const setVol = (val: number) => {
    const v = videoRef.current!
    v.volume = val
    v.muted = val === 0
    if (val > 0) setSoundOff(false)
    try {
      localStorage.setItem('fp.volume', String(val))
    } catch {}
  }

  // The host's picks set the room default; everyone else's are personal.
  const isHost = me?.role === 'host'
  const pick = (field: 'quality' | 'audio' | 'sub', value: string | number) => {
    if (isHost) {
      socket.send({ t: 'host', op: 'prefs', prefs: { ...S.prefs.value, [field]: value } })
      if (field === 'quality') setMyQ(null)
      if (field === 'audio') setMyAudio(null)
      if (field === 'sub') setMySub(null)
      return
    }
    if (field === 'quality') setMyQ(value as string)
    if (field === 'audio') setMyAudio(value as number)
    if (field === 'sub') setMySub(value as string)
  }
  const audioName = (i: number) => (i < 0 ? 'Default' : (m?.audio || []).find((a) => a.index === i)?.label || `Track ${i + 1}`)
  const subName = (id: string) => (id === '' ? 'Off' : (m?.subs || []).find((s) => s.id === id)?.label || id)
  const qName = qualityLabel

  // ---- derived UI ----
  const behind = report.target - cur
  const badge = useMemo(() => {
    if (!S.connected.value) return { cls: 'wait', text: 'Reconnecting…' }
    if (!st?.media) return { cls: 'wait', text: 'Waiting for the host to pick a video' }
    if (st.reason === 'intermission') return { cls: 'stop', text: 'INTERMISSION · paused for all' }
    if (mode === 'solo') {
      if (Math.abs(behind) < 1) return { cls: 'behind', text: 'Watching solo' }
      return { cls: 'behind', text: behind > 0 ? `You’re ${fmtGap(behind)} behind the room` : `You’re ${fmtGap(behind)} ahead of the room` }
    }
    if (!playable) return { cls: 'wait', text: 'Preparing video…' }
    if (st.reason === 'buffering') return { cls: 'wait', text: `Waiting for ${st.by || 'someone'} to buffer` }
    if (st.reason === 'encoder') return { cls: 'wait', text: 'Waiting for the encoder to get ahead' }
    if (st.reason === 'ended') return { cls: 'wait', text: 'Finished' }
    if (!st.playing) return { cls: 'wait', text: st.pos > 0 && st.by ? `Paused by ${st.by}` : 'Waiting for the host to start' }
    if (report.buffering) return { cls: 'behind', text: 'Buffering…' }
    if (Math.abs(report.drift) > 0.3) return { cls: 'live', text: 'LIVE · syncing…' }
    return { cls: 'live', text: `LIVE · in sync · ${ping} ms` }
  }, [st, mode, playable, behind, report.buffering, report.drift > 0.3 || report.drift < -0.3, ping, S.connected.value])

  const seekMax = m && m.state !== 'ready' ? m.encoded : duration
  const subs = m?.subs || []
  const audioTracks = m?.audio || []

  return (
    <div
      ref={wrap}
      class={`player ${showUi || paused ? 'ui-on' : 'ui-off'} ${compact ? 'compact' : ''} ${chatOpen ? 'chat-on' : ''}`}
      onMouseMove={() => {
        // Phones send a fake mouse move with every tap; the tap itself decides.
        if (performance.now() - lastTouch.current > 800) setShowUi(true)
      }}
      onTouchStart={(e) => {
        lastTouch.current = performance.now()
        // A tap on the picture toggles the controls (the video's click
        // handler); a touch anywhere else just keeps them on screen.
        if (e.target !== videoRef.current) setShowUi(true)
      }}
    >
      <div class="stage">
        <video
          ref={videoRef}
          playsInline
          preload="auto"
          disablePictureInPicture={false}
          // On touchscreens a tap shows or hides the controls; with a mouse it plays/pauses.
          onClick={() => (soundOff ? unmute() : compact || touch || performance.now() - lastTouch.current < 800 ? setShowUi((on) => !on) : togglePlay())}
          onDblClick={toggleFs}
        >
          {playable &&
            subs.map((s) => <track key={s.id} id={s.id} kind="subtitles" src={`/hls/${m!.id}/${s.url}`} srclang={s.lang} label={s.label} />)}
        </video>

        <div class="scrim top" />
        <div class="scrim bottom" />

        {/* Top bar */}
        <div class="topbar">
          <div class="row title-block">
            <div class="col tight">
              <div class="room-title ellipsis">{S.settings.value.roomName}</div>
              <div class="caption dim ellipsis">{m?.name || 'No video yet'}</div>
            </div>
            <span class={`pill badge ${badge.cls}`}>
              <span class="dot" />
              <span class="ellipsis">{badge.text}</span>
            </span>
            {mode === 'solo' && (
              <button class="btn btn-primary btn-sm" onClick={backToLive}>
                Back to live ▸
              </button>
            )}
          </div>
          <div class="row">
            <ViewerStack onClick={() => setPeersOpen(!peersOpen)} />
            {me?.role === 'host' && (
              <button class="icon-btn" title="Host console" aria-label="Host console" onClick={() => navigate('/host')}>
                <Icon name="settings" />
              </button>
            )}
          </div>
        </div>

        {peersOpen && <Peers onClose={() => setPeersOpen(false)} />}
        {me?.role === 'host' && <LobbyToasts />}

        {/* Center overlays */}
        <CenterOverlay
          blocked={blocked}
          onUnblock={unblock}
          playable={playable}
          paused={paused}
          canControl={canControl}
          mode={mode}
          onPlay={togglePlay}
        />

        {soundOff && (
          <button class="unmute-pill" onClick={unmute}>
            <Icon name="mute" size={18} /> Sound is off · tap to unmute
          </button>
        )}

        <Reactions />

        {/* Chat */}
        {!compact && (
          <div class="chat-dock">
            <Chat open={chatOpen} />
          </div>
        )}

        {/* Controls */}
        <div class="controls" onClick={(e) => e.stopPropagation()}>
          <Seekbar
            duration={duration}
            current={cur}
            buffered={buffered}
            seekMax={seekMax || 0}
            room={mode === 'solo' ? report.target : -1}
            limit={canControl ? -1 : Math.min(report.target, seekMax || Infinity)}
            onSeek={seekTo}
          />
          <div class="control-row">
            <button class="icon-btn" onClick={togglePlay} aria-label={paused ? 'Play' : 'Pause'} title="Play / pause (space)">
              <Icon name={paused ? 'play' : 'pause'} />
            </button>
            <button class="icon-btn" onClick={() => skip(-10)} aria-label="Back 10 seconds" title="Back 10s (←)">
              <Icon name="back10" />
            </button>
            <button class="icon-btn" onClick={() => skip(10)} aria-label="Forward 10 seconds" title="Forward 10s (→)">
              <Icon name="fwd10" />
            </button>
            <div class="volume row">
              <button class="icon-btn" onClick={() => (soundOff ? unmute() : (videoRef.current!.muted = !muted))} aria-label={muted ? 'Unmute' : 'Mute'}>
                <Icon name={muted || volume === 0 ? 'mute' : 'volume'} />
              </button>
              <input
                type="range"
                min={0}
                max={1}
                step={0.02}
                value={muted ? 0 : volume}
                aria-label="Volume"
                onInput={(e) => setVol(parseFloat((e.target as HTMLInputElement).value))}
              />
            </div>
            <span class="mono time">
              {fmtTime(cur)} / {fmtTime(duration)}
            </span>
            <span class="grow" />
            <button
              class={`live-toggle ${mode === 'follow' ? 'on' : ''}`}
              onClick={() => (mode === 'follow' ? null : backToLive())}
              title={mode === 'follow' ? 'You are watching with the room' : 'Jump back to the room'}
            >
              <Icon name="radio" size={16} />
              {compact ? (mode === 'follow' ? 'Live' : 'Solo') : mode === 'follow' ? 'Live with room' : 'Watching solo'}
            </button>
            {canControl && (
              <button
                class={`icon-btn danger ${st?.reason === 'intermission' ? 'active' : ''}`}
                title={`Intermission: pause for everyone for ${S.settings.value.interMinutes || 5} min`}
                onClick={() =>
                  socket.send(st?.reason === 'intermission' ? { t: 'cmd', op: 'inter_end' } : { t: 'cmd', op: 'inter', sec: (S.settings.value.interMinutes || 5) * 60 })
                }
              >
                <Icon name="intermission" />
              </button>
            )}
            {!compact && (
              <button class={`icon-btn ${chatOpen ? '' : 'active'}`} onClick={() => setChatOpen(!chatOpen)} title="Chat (c)">
                <Icon name="chat" />
              </button>
            )}
            <div class="menu-anchor">
              <button class={`icon-btn ${menu ? 'active' : ''}`} onClick={() => setMenu(!menu)} title="Quality, audio, subtitles">
                <Icon name="settings" />
              </button>
              {menu && (compact || touch) && <div class="menu-back" onClick={() => setMenu(false)} />}
              {menu && (
                <div class="menu" onMouseLeave={() => !touch && setMenu(false)}>
                  {isHost ? (
                    <div class="menu-note">Your choices apply to everyone. Viewers can still change their own.</div>
                  ) : (
                    <div class="menu-note">These change only your screen.</div>
                  )}
                  <div class="menu-group">
                    <div class="menu-title">Quality</div>
                    {!isHost && (
                      <button class={myQ === null ? 'sel' : ''} onClick={() => setMyQ(null)}>
                        Host’s choice · {qName(room.quality)}
                      </button>
                    )}
                    <button class={(isHost || myQ !== null) && quality === 'auto' ? 'sel' : ''} onClick={() => pick('quality', 'auto')}>
                      Auto {quality === 'auto' && autoLevel ? `· ${autoLevel} now` : ''}
                      <span class="menu-sub">Best for this screen and connection</span>
                    </button>
                    {levels.map((l) => (
                      <button key={l.index} class={(isHost || myQ !== null) && quality === l.name ? 'sel' : ''} onClick={() => pick('quality', l.name)}>
                        {qualityLabel(l.name)}
                      </button>
                    ))}
                  </div>
                  {audioTracks.length > 1 && audioCount > 1 && (
                    <div class="menu-group">
                      <div class="menu-title">Audio</div>
                      {!isHost && (
                        <button class={myAudio === null ? 'sel' : ''} onClick={() => setMyAudio(null)}>
                          Host’s choice · {audioName(room.audio)}
                        </button>
                      )}
                      {audioTracks.map((a) => (
                        <button
                          key={a.index}
                          class={(isHost || myAudio !== null) && (audio === a.index || (audio < 0 && a.index === 0)) ? 'sel' : ''}
                          onClick={() => pick('audio', a.index)}
                        >
                          {a.label}
                        </button>
                      ))}
                    </div>
                  )}
                  <div class="menu-group">
                    <div class="menu-title">Subtitles</div>
                    {!isHost && (
                      <button class={mySub === null ? 'sel' : ''} onClick={() => setMySub(null)}>
                        Host’s choice · {subName(room.sub)}
                      </button>
                    )}
                    <button class={(isHost || mySub !== null) && sub === '' ? 'sel' : ''} onClick={() => pick('sub', '')}>
                      Off
                    </button>
                    {subs.map((s) => (
                      <button key={s.id} class={(isHost || mySub !== null) && sub === s.id ? 'sel' : ''} onClick={() => pick('sub', s.id)}>
                        {s.label}
                      </button>
                    ))}
                  </div>
                  <div class="menu-group">
                    <div class="menu-title">Connection</div>
                    <div class="menu-stat">
                      Ping <span class={`ping ${pingClass(ping)}`}>{ping} ms</span>
                    </div>
                    <div class="menu-stat">
                      Jitter <span class="ping">{S.jitter.value} ms</span>
                    </div>
                    <div class="menu-stat">
                      Offset from room <span class="ping">{Math.round(report.drift * 1000)} ms</span>
                    </div>
                  </div>
                </div>
              )}
            </div>
            {pipSupported && (
              <button class={`icon-btn ${inPip ? 'active' : ''}`} onClick={togglePip} title="Picture in picture" aria-label="Picture in picture">
                <Icon name="pip" />
              </button>
            )}
            <button class="icon-btn" onClick={toggleFs} title="Fullscreen (f)">
              <Icon name={isFs ? 'x' : 'fullscreen'} />
            </button>
          </div>
        </div>
      </div>

      {compact && (
        <div class="below">
          <div class="below-title">
            <div class="h18 ellipsis">{S.settings.value.roomName}</div>
            <div class="caption dim ellipsis">
              {m?.name || 'No video yet'} · {S.peers.value.length} watching · {ping} ms
            </div>
          </div>
          <Chat compact />
        </div>
      )}
    </div>
  )
}

function CenterOverlay(p: {
  blocked: boolean
  onUnblock: () => void
  playable: boolean
  paused: boolean
  canControl: boolean
  mode: 'follow' | 'solo'
  onPlay: () => void
}) {
  const st = S.room.value
  const m = S.media.value
  const [, force] = useState(0)
  useEffect(() => {
    if (st?.reason !== 'intermission' || !st.interUntil) return
    const t = setInterval(() => force((n) => n + 1), 500)
    return () => clearInterval(t)
  }, [st?.reason, st?.interUntil])

  if (p.blocked) {
    return (
      <div class="center">
        <button class="big-play" onClick={p.onUnblock} aria-label="Start watching">
          <Icon name="play" size={36} />
        </button>
        <div class="center-hint">Tap to start watching with the room</div>
      </div>
    )
  }
  if (!st?.media) {
    return (
      <div class="center">
        <div class="center-card">
          <Icon name="film" size={40} />
          <div class="h18">Waiting for the host to pick a video</div>
          {S.you.value?.role === 'host' && (
            <button class="btn btn-primary" onClick={() => navigate('/host')}>
              Open host console
            </button>
          )}
        </div>
      </div>
    )
  }
  if (st.reason === 'intermission') {
    const left = st.interUntil ? Math.max(0, (st.interUntil - socket.clock.now()) / 1000) : -1
    return (
      <div class="center dimmed">
        <div class="center-card intermission">
          <Icon name="intermission" size={48} class="amber" />
          <div class="display">Intermission</div>
          <div class="dim">Paused for everyone{st.by ? ` by ${st.by}` : ''} · resumes together</div>
          {left >= 0 && <div class="countdown mono">{fmtTime(left)}</div>}
          <div class="caption muted">Paused at {fmtTime(st.pos)}</div>
          {p.canControl && (
            <div class="row">
              <button class="btn btn-primary" onClick={() => socket.send({ t: 'cmd', op: 'inter_end' })}>
                Resume for everyone
              </button>
              <button class="btn btn-ghost" onClick={() => socket.send({ t: 'cmd', op: 'inter_extend', sec: 300 })}>
                +5 min
              </button>
            </div>
          )}
        </div>
      </div>
    )
  }
  if (!p.playable && m) {
    const pct = m.duration > 0 ? Math.min(100, (m.encoded / m.duration) * 100) : 0
    return (
      <div class="center">
        <div class="center-card">
          {m.state === 'error' ? (
            <>
              <div class="h18">This video could not be prepared</div>
              <div class="caption dim">{m.error}</div>
            </>
          ) : (
            <>
              <div class="spinner" />
              <div class="h18">Preparing video…</div>
              <div class="caption dim">
                {m.state === 'probing' ? 'Reading the file' : `Encoding ${pct.toFixed(0)}%${m.speed ? ` · ${m.speed.toFixed(1)}× realtime` : ''}`}
              </div>
              <div class="caption muted">Playback starts as soon as the first chunks are ready</div>
            </>
          )}
        </div>
      </div>
    )
  }
  if (p.mode === 'solo' && p.paused) {
    return (
      <div class="center">
        <button class="big-play" onClick={p.onPlay} aria-label="Resume">
          <Icon name="play" size={36} />
        </button>
        <div class="center-hint">Paused for you only · the room keeps playing</div>
      </div>
    )
  }
  if (p.mode === 'follow' && st && !st.playing && st.reason !== 'buffering' && st.reason !== 'encoder') {
    return (
      <div class="center">
        {p.canControl ? (
          <>
            <button class="big-play" onClick={p.onPlay} aria-label="Play for everyone">
              <Icon name="play" size={36} />
            </button>
            <div class="center-hint">{st.reason === 'ended' ? 'Play again for everyone' : 'Start for everyone'}</div>
          </>
        ) : (
          <div class="center-hint">{st.reason === 'ended' ? 'That’s the end' : st.pos > 0 && st.by ? `Paused by ${st.by}` : 'The host will start soon'}</div>
        )}
      </div>
    )
  }
  return null
}

function Seekbar(p: {
  duration: number
  current: number
  buffered: number
  seekMax: number
  room: number
  limit: number // viewers can't go past this (the room position); -1 = no limit
  onSeek: (t: number) => void
}) {
  const bar = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<number | null>(null)
  const [drag, setDrag] = useState<number | null>(null)
  const dragging = useRef(false)
  const d = p.duration || 1
  const pct = (t: number) => `${Math.max(0, Math.min(100, (t / d) * 100))}%`
  const at = (e: PointerEvent) => {
    const r = bar.current!.getBoundingClientRect()
    const t = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width)) * d
    return p.limit >= 0 ? Math.min(t, p.limit) : t
  }
  const shown = drag ?? p.current
  return (
    <div
      class="seekbar"
      ref={bar}
      role="slider"
      aria-label="Seek"
      aria-valuemin={0}
      aria-valuemax={Math.round(p.duration)}
      aria-valuenow={Math.round(p.current)}
      tabIndex={0}
      onPointerDown={(e) => {
        try {
          ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
        } catch {
          // Some pointers (pens, synthetic events) can't be captured; dragging still works.
        }
        dragging.current = true
        setDrag(at(e))
      }}
      onPointerMove={(e) => {
        setHover(at(e))
        if (dragging.current) setDrag(at(e))
      }}
      onPointerUp={(e) => {
        // A ref, not state: a quick tap releases before the re-render.
        if (dragging.current) p.onSeek(at(e))
        dragging.current = false
        setDrag(null)
      }}
      onPointerCancel={() => {
        dragging.current = false
        setDrag(null)
      }}
      onPointerLeave={() => setHover(null)}
    >
      <div class="track">
        {p.seekMax < p.duration && <div class="encoded" style={{ width: pct(p.seekMax) }} />}
        <div class="buffered" style={{ width: pct(p.buffered) }} />
        <div class="played" style={{ width: pct(shown) }} />
        {p.room >= 0 && <div class="room-mark" style={{ left: pct(p.room) }} title="Where the room is" />}
        {p.limit >= 0 && p.limit < d - 1 && (
          <div class="locked" style={{ left: pct(p.limit), width: `calc(100% - ${pct(p.limit)})` }} title="You can't go ahead of the room" />
        )}
      </div>
      <div class="knob" style={{ left: pct(shown) }} />
      {hover !== null && (
        <div class="hover-time mono" style={{ left: pct(hover) }}>
          {fmtTime(hover)}
        </div>
      )}
    </div>
  )
}

function bufferedAhead(v: HTMLVideoElement): number {
  const b = v.buffered
  for (let i = 0; i < b.length; i++) {
    if (b.start(i) <= v.currentTime + 0.3 && b.end(i) > v.currentTime) return b.end(i) - v.currentTime
  }
  return 0
}
