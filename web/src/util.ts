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

/** Quality label from a picture size, by its long side: 1920×800 is "1080p". */
export function qualityName(w: number, h: number): string {
  const long = Math.max(w, h)
  if (long >= 1800) return '1080p'
  if (long >= 1200) return '720p'
  if (long >= 800) return '480p'
  if (long >= 600) return '360p'
  return `${Math.min(w, h)}p`
}

/** Friendly name for a quality rung: "1080p HD", "720p SD". */
export function qualityLabel(q: string): string {
  switch (q) {
    case 'auto':
      return 'Auto'
    case '1080p':
      return '1080p HD'
    case '720p':
      return '720p SD'
    case '480p':
      return '480p data saver'
    default:
      return q
  }
}

export function pingClass(ms: number): string {
  if (ms <= 0) return ''
  if (ms < 120) return 'good'
  if (ms < 300) return 'ok'
  return 'bad'
}

export function fmtBytes(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} GB`
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

// Reactions are animated Noto emoji, sent as codepoints like "1f525".
export const DEFAULT_REACTIONS = ['1f525', '1f602', '1f62d', '1f44f', '2764_fe0f', '1f37f']

/** "2764_fe0f" → "❤️" */
export function emojiChar(code: string): string {
  try {
    return String.fromCodePoint(...code.split('_').map((h) => parseInt(h, 16)))
  } catch {
    return '❓'
  }
}

export const emojiURL = (code: string) => `/emoji/${code}.webp`

const RECENT_KEY = 'fp.recentEmoji'
export function recentEmoji(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) || '[]')
    return Array.isArray(v) ? v.filter((x) => typeof x === 'string').slice(0, 24) : []
  } catch {
    return []
  }
}
export function rememberEmoji(code: string) {
  const list = [code, ...recentEmoji().filter((c) => c !== code)].slice(0, 24)
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(list))
  } catch {}
}
