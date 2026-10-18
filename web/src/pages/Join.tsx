import { useEffect, useRef, useState } from 'preact/hooks'
import { ApiError, me, post, proof } from '../api'
import { Logo, Icon } from '../components/Icons'
import { navigate } from '../util'

type Phase = 'checking' | 'ready' | 'joining' | 'waiting' | 'denied' | 'error'

interface Proof {
  id: string
  nonce: string
  at: number
}

// The invite page. The token lives in the URL fragment, which browsers never
// send to any server, and is removed from the address bar right away.
export function Join({ id }: { id: string }) {
  const [phase, setPhase] = useState<Phase>('checking')
  const [error, setError] = useState('')
  const [label, setLabel] = useState('')
  const [roomName, setRoomName] = useState('')
  const [online, setOnline] = useState(0)
  const [name, setName] = useState('')
  const [second, setSecond] = useState(false)
  const token = useRef('')
  const nextProof = useRef<Promise<Proof> | null>(null)

  const freshProof = () => proof().then((p) => ({ ...p, at: Date.now() }))

  useEffect(() => {
    const key = `fp.inv.${id}`
    const m = location.hash.match(/[#&]t=([A-Za-z0-9_-]{10,64})/)
    if (m) {
      token.current = m[1]
      try {
        sessionStorage.setItem(key, m[1])
      } catch {}
      history.replaceState(null, '', location.pathname)
    } else {
      try {
        token.current = sessionStorage.getItem(key) || ''
      } catch {}
    }
    ;(async () => {
      const who = await me().catch(() => null)
      if (who?.status === 'admitted') return navigate('/room')
      if (who?.status === 'pending') return setPhase('waiting')
      if (!token.current) {
        setError('This link is incomplete. Ask the host to send it again.')
        return setPhase('error')
      }
      try {
        const p = await freshProof()
        nextProof.current = freshProof() // solve the join check in the background
        const info = await post<{ label: string; roomName: string; online: number; locked: boolean }>('/api/peek', {
          id,
          token: token.current,
          pow: { id: p.id, nonce: p.nonce },
        })
        setLabel(info.label)
        setName(info.label)
        setRoomName(info.roomName)
        setOnline(info.online)
        if (info.locked) {
          setError('The host has locked the room for now.')
          return setPhase('error')
        }
        setPhase('ready')
      } catch (e) {
        setError((e as Error).message)
        setPhase('error')
      }
    })()
  }, [id])

  // While waiting in the lobby, poll until the host decides.
  useEffect(() => {
    if (phase !== 'waiting') return
    const t = setInterval(async () => {
      const who = await me().catch(() => null)
      if (who?.status === 'admitted') navigate('/room')
      else if (who?.status === 'denied') setPhase('denied')
      else if (who?.status === 'none') {
        setError('Your request expired. Open your invite link again.')
        setPhase('error')
      }
    }, 1500)
    return () => clearInterval(t)
  }, [phase])

  const submit = async (e: Event) => {
    e.preventDefault()
    setPhase('joining')
    try {
      let p = await (nextProof.current || freshProof())
      if (Date.now() - p.at > 100_000) p = await freshProof() // challenges expire after 2 minutes
      nextProof.current = null
      const res = await post<{ status: string; secondDevice: boolean }>('/api/join', {
        id,
        token: token.current,
        name,
        pow: { id: p.id, nonce: p.nonce },
      })
      try {
        sessionStorage.setItem('fp.last', id)
      } catch {}
      if (res.status === 'admitted') return navigate('/room')
      setSecond(res.secondDevice)
      setPhase('waiting')
    } catch (err) {
      nextProof.current = freshProof()
      setError(err instanceof ApiError ? err.message : 'Could not join, check your connection')
      setPhase(err instanceof ApiError && err.status === 409 ? 'ready' : 'error')
    }
  }

  return (
    <div class="center-page">
      <div class="join-card">
        <Logo />
        {phase === 'checking' && (
          <>
            <div class="spinner" />
            <div class="dim">Checking your invite…</div>
          </>
        )}
        {(phase === 'ready' || phase === 'joining') && (
          <>
            <h1 class="h24">
              You’re invited to
              <br />
              {roomName}
            </h1>
            <div class="dim">
              {online > 0 ? `${online} ${online === 1 ? 'person is' : 'people are'} already in the room` : 'Be the first one in'}
            </div>
            <span class="tag ok">
              <Icon name="check" size={14} /> Personal invite for “{label}”
            </span>
            <form onSubmit={submit}>
              <input
                class="input"
                value={name}
                maxLength={24}
                required
                autoFocus
                placeholder="Your display name"
                aria-label="Your display name"
                onInput={(e) => setName((e.target as HTMLInputElement).value)}
              />
              <button class="btn btn-primary btn-block" disabled={phase === 'joining' || !name.trim()}>
                {phase === 'joining' ? 'Joining…' : 'Join room'}
              </button>
              {error && <div class="caption warn-text">{error}</div>}
            </form>
            <div class="caption muted">
              This link is yours alone. It locks to this device the first time you join, so please don’t forward it.
            </div>
          </>
        )}
        {phase === 'waiting' && (
          <>
            <div class="spinner" />
            <h1 class="h24">Waiting for the host</h1>
            <div class="dim">
              {second
                ? 'This invite was already used on another device, so the host needs to confirm it’s you.'
                : 'The host will let you in in a moment. Keep this page open.'}
            </div>
          </>
        )}
        {phase === 'denied' && (
          <>
            <h1 class="h24">Not admitted</h1>
            <div class="dim">The host didn’t let this device in. If that’s a mistake, message them.</div>
          </>
        )}
        {phase === 'error' && (
          <>
            <h1 class="h24">Can’t open this invite</h1>
            <div class="dim">{error}</div>
          </>
        )}
      </div>
    </div>
  )
}
