export function fmtTime(sec: number): string {
  if (!isFinite(sec) || sec < 0) sec = 0
  sec = Math.floor(sec)
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  const mm = h ? String(m).padStart(2, '0') : String(m)
  return (h ? `${h}:` : '') + `${mm}:${String(s).padStart(2, '0')}`
}

/** Human "1:24" style gap, used for "1:24 behind". */
export function fmtGap(sec: number): string {
  sec = Math.abs(sec)
  if (sec < 60) return `${Math.round(sec)}s`
  return fmtTime(sec)
}

const palette = ['#D99A1E', '#2ED573', '#FF7A59', '#5B9DFF', '#C77DFF', '#00C2B8', '#FF5C8A', '#A7AEC0']

export function colorFor(id: string): string {
  let h = 0
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) | 0
  return palette[Math.abs(h) % palette.length]
}

export function initial(name: string): string {
  return [...name.trim()][0] || '?'
}

export function pingClass(ms: number): string {
  if (ms <= 0) return ''
  if (ms < 120) return 'good'
  if (ms < 300) return 'ok'
  return 'bad'
}

export function fmtBytes(n: number): string {
  if (n > 1e9) return `${(n / 1e9).toFixed(1)} GB`
  if (n > 1e6) return `${(n / 1e6).toFixed(0)} MB`
  return `${Math.round(n / 1e3)} KB`
}

export async function copy(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  }
}

export function navigate(path: string) {
  history.pushState(null, '', path)
  dispatchEvent(new PopStateEvent('popstate'))
}

export const REACTIONS = ['🔥', '😂', '😭', '👏', '❤️', '🍿']
export const MORE_REACTIONS = ['🤯', '😮', '👀', '💀', '🙌', '😍']
