// Proof-of-work solver. Runs in a Web Worker so typing stays smooth.
// The puzzle: find a nonce so sha256(salt + ":" + nonce) starts with `bits`
// zero bits. ~65k hashes at the base difficulty, a fraction of a second.

export function solve(salt: string, bits: number): Promise<string> {
  return new Promise((resolve, reject) => {
    let worker: Worker
    try {
      worker = new Worker(new URL('./pow.worker.ts', import.meta.url), { type: 'module' })
    } catch {
      // Very old browsers: solve on the main thread.
      import('./sha256').then(({ search }) => resolve(search(salt, bits))).catch(reject)
      return
    }
    worker.onmessage = (e: MessageEvent<string>) => {
      resolve(e.data)
      worker.terminate()
    }
    worker.onerror = (e) => {
      worker.terminate()
      reject(new Error(e.message || 'browser check failed'))
    }
    worker.postMessage({ salt, bits })
  })
}
