import { Logo } from '../components/Icons'

export function Landing() {
  return (
    <div class="center-page">
      <div class="join-card">
        <Logo />
        <h1 class="h24">This is a private room</h1>
        <div class="dim">You need a personal invite link from the host to join.</div>
      </div>
    </div>
  )
}
