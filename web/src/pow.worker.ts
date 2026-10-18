import { search } from './sha256'

self.onmessage = (e: MessageEvent<{ salt: string; bits: number }>) => {
  ;(self as unknown as Worker).postMessage(search(e.data.salt, e.data.bits))
}
