import * as S from '../store'
import { socket } from '../socket'
import { Icon } from './Icons'

export function LobbyEntry({ p }: { p: S.Pending }) {
  const nameDiffers = p.name.toLowerCase() !== p.label.toLowerCase()
  return (
    <div class={`lobby-entry ${p.secondDevice ? 'warn' : ''}`}>
      <div class="grow col tight">
        <div class="row wrap">
          <strong class="ellipsis">{p.name}</strong>
          {nameDiffers && <span class="caption dim">via “{p.label}” invite</span>}
        </div>
        <div class="caption muted">
          {p.device} · {p.ip}
        </div>
        {p.secondDevice && (
          <div class="caption warn-text">
            <Icon name="shield" size={12} /> {p.label}’s link opened on a second device. Admitting moves the seat here.
          </div>
        )}
      </div>
      <div class="row">
        <button class="btn btn-sm btn-ghost" onClick={() => socket.send({ t: 'host', op: 'deny', sid: p.id })}>
          Deny
        </button>
        <button class="btn btn-sm btn-primary" onClick={() => socket.send({ t: 'host', op: 'admit', sid: p.id })}>
          Admit
        </button>
      </div>
    </div>
  )
}

/** Join requests shown on top of the player for the host. */
export function LobbyToasts() {
  const list = S.lobby.value
  if (!list.length) return null
  return (
    <div class="lobby-toasts">
      {list.slice(0, 3).map((p) => (
        <LobbyEntry key={p.id} p={p} />
      ))}
      {list.length > 3 && <div class="caption dim">+{list.length - 3} more in the host console</div>}
    </div>
  )
}
