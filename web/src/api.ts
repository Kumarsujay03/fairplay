import { solve } from './pow'

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function call<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    credentials: 'same-origin',
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string }).error || `request failed (${res.status})`)
  return data as T
}

export const get = <T>(url: string) => call<T>('GET', url)
export const post = <T>(url: string, body: unknown = {}) => call<T>('POST', url, body)
export const del = <T>(url: string) => call<T>('DELETE', url)

/** Fetch and solve a proof-of-work challenge from the bot shield. */
export async function proof(): Promise<{ id: string; nonce: string }> {
  const c = await get<{ id: string; salt: string; bits: number }>('/api/challenge')
  const nonce = await solve(c.salt, c.bits)
  return { id: c.id, nonce }
}

export interface Me {
  status: 'none' | 'pending' | 'admitted' | 'denied'
  role?: 'host' | 'guest'
  name?: string
  id?: string
  roomName?: string
}

export const me = () => get<Me>('/api/me')
