import { useEffect, useState } from 'preact/hooks'
import { me } from '../api'
import { socket } from '../socket'
import * as S from '../store'
import { Player } from '../components/Player'
import { Logo } from '../components/Icons'
import { Landing } from './Landing'

export function Room() {
  const [ok, setOk] = useState<boolean | null>(null)
  useEffect(() => {
    me()
      .then((who) => {
        if (who.status === 'admitted') {
          socket.connect()
          setOk(true)
        } else setOk(false)
      })
      .catch(() => setOk(false))
  }, [])

  if (S.kicked.value) {
    return (
      <div class="center-page">
        <div class="join-card">
          <Logo />
          <h1 class="h24">You’ve left the room</h1>
          <div class="dim">{S.kicked.value}</div>
        </div>
      </div>
    )
  }
  if (ok === null) return <div class="center-page"><div class="spinner" /></div>
  if (!ok) return <Landing />
  if (!S.you.value) {
    return (
      <div class="center-page">
        <div class="col center-col">
          <div class="spinner" />
          <div class="dim">Connecting to the room…</div>
        </div>
      </div>
    )
  }
  return <Player />
}
