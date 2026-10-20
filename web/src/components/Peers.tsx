import * as S from '../store'
import { ask, askText } from './Dialog'
import { socket } from '../socket'
import { Icon } from './Icons'
import { colorFor, fmtGap, initial, pingClass } from '../util'

export function Avatar({ id, name, size = 28 }: { id: string; name: string; size?: number }) {
  return (
    <span class="avatar" style={{ background: colorFor(id), width: size, height: size }} title={name}>
      {initial(name)}
    </span>
  )
}

export function ViewerStack({ onClick }: { onClick: () => void }) {
  const list = S.peers.value
  const worst = list.reduce((w, p) => Math.max(w, p.rtt), 0)
  return (
    <button class="stack" onClick={onClick} title="Who is watching">
      <span class="avatars">
        {list.slice(0, 4).map((p) => (
          <Avatar key={p.id} id={p.id} name={p.name} />
        ))}
      </span>
      <span class="label">{list.length} watching</span>
      <span class={`ping ${pingClass(S.ping.value)}`}>{S.ping.value} ms</span>
      {worst > 300 && <span class="tag warn">slow link</span>}
    </button>
  )
}

export function peerStatus(p: S.Peer, roomTarget?: number): { text: string; cls: string } {
  if (p.buf) return { text: 'buffering', cls: 'warn' }
  if (p.mode === 'solo') {
    const gap = roomTarget !== undefined ? roomTarget - p.pos : p.drift / -1000
    return { text: Math.abs(gap) > 1 ? `solo, ${fmtGap(gap)} ${gap > 0 ? 'behind' : 'ahead'}` : 'solo', cls: 'warn' }
  }
  if (Math.abs(p.drift) > 400) return { text: 'syncing', cls: 'neutral' }
  return { text: 'in sync', cls: 'ok' }
}

export function Peers({ onClose }: { onClose: () => void }) {
  const me = S.you.value
  const host = me?.role === 'host'
  return (
    <div class="peers-panel" role="dialog" aria-label="Viewers">
      <div class="card-head">
        <div class="h18">Watching · {S.peers.value.length}</div>
        <button class="icon-btn sm plain" onClick={onClose} aria-label="Close">
          <Icon name="x" size={18} />
        </button>
      </div>
      <div class="divider-list">
        {S.peers.value.map((p) => {
          const s = peerStatus(p)
          return (
            <div class="peer-row" key={p.id}>
              <Avatar id={p.id} name={p.name} />
              <div class="grow col tight peer-info">
                <div class="row">
                  <span class="ellipsis">{p.name}</span>
                  {p.id === me?.id && (
                    <button
                      class="caption link-btn"
                      onClick={async () => {
                        const n = await askText({ title: 'Your name in the room', label: 'Display name', value: p.name, maxLength: 24, confirm: 'Rename' })
                        if (n) socket.send({ t: 'rename', name: n })
                      }}
                    >
                      (you · rename)
                    </button>
                  )}
                  {p.role === 'host' && <span class="tag neutral">Host</span>}
                  {p.cohost && <span class="tag warn">Co-host</span>}
                  {p.chatOff && <span class="tag neutral">chat hidden</span>}
                </div>
                <div class="row caption">
                  <span class={`tag ${s.cls}`}>{s.text}</span>
                  <span class={`ping ${pingClass(p.rtt)}`}>{p.rtt} ms</span>
                  {p.q && <span class="muted">{p.q}</span>}
                </div>
              </div>
              {host && p.role !== 'host' && (
                <div class="row peer-actions">
                  <button
                    class="icon-btn sm plain"
                    title={p.chatOff ? 'Show chat to them again' : 'Hide chat from them'}
                    onClick={() => socket.send({ t: 'host', op: 'chat_hide', sid: p.id, on: !p.chatOff })}
                  >
                    <Icon name={p.chatOff ? 'chat' : 'chatOff'} size={16} />
                  </button>
                  <button
                    class={`btn btn-sm ${p.cohost ? 'btn-primary' : 'btn-ghost'}`}
                    title="Let them control playback for everyone"
                    onClick={() => socket.send({ t: 'host', op: 'cohost', sid: p.id, on: !p.cohost })}
                  >
                    {p.cohost ? 'Co-host' : 'Make co-host'}
                  </button>
                  <button
                    class="icon-btn sm plain danger"
                    title="Remove from room"
                    onClick={async () =>
                      (await ask({ title: `Remove ${p.name}?`, message: 'They are disconnected from the room. Their invite still works unless you revoke it.', confirm: 'Remove', danger: true })) &&
                      socket.send({ t: 'host', op: 'kick', sid: p.id })
                    }
                  >
                    <Icon name="x" size={16} />
                  </button>
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
