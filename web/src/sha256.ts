// Minimal single-block SHA-256 for the proof-of-work search. Messages are
// always shorter than 56 bytes (salt + ":" + decimal nonce), so one 64-byte
// block is enough and no allocation happens inside the loop.

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])

const W = new Uint32Array(64)
const block = new Uint8Array(64)

/** Returns the first 32-bit word of sha256(msg) for msg.length < 56. */
function firstWord(msg: Uint8Array, len: number): number {
  block.fill(0)
  block.set(msg.subarray(0, len))
  block[len] = 0x80
  const bitLen = len * 8
  block[62] = (bitLen >>> 8) & 0xff
  block[63] = bitLen & 0xff
  for (let i = 0; i < 16; i++) {
    W[i] = (block[i * 4] << 24) | (block[i * 4 + 1] << 16) | (block[i * 4 + 2] << 8) | block[i * 4 + 3]
  }
  for (let i = 16; i < 64; i++) {
    const x = W[i - 15], y = W[i - 2]
    const s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3)
    const s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10)
    W[i] = (W[i - 16] + s0 + W[i - 7] + s1) | 0
  }
  let a = 0x6a09e667, b = 0xbb67ae85, c = 0x3c6ef372, d = 0xa54ff53a
  let e = 0x510e527f, f = 0x9b05688c, g = 0x1f83d9ab, h = 0x5be0cd19
  for (let i = 0; i < 64; i++) {
    const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7))
    const ch = (e & f) ^ (~e & g)
    const t1 = (h + S1 + ch + K[i] + W[i]) | 0
    const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10))
    const maj = (a & b) ^ (a & c) ^ (b & c)
    const t2 = (S0 + maj) | 0
    h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = b; b = a; a = (t1 + t2) | 0
  }
  return (a + 0x6a09e667) >>> 0
}

export function search(salt: string, bits: number): string {
  const prefix = new TextEncoder().encode(salt + ':')
  const buf = new Uint8Array(64)
  buf.set(prefix)
  const need = Math.min(bits, 32)
  const mask = need === 32 ? 0xffffffff : ~(0xffffffff >>> need) >>> 0
  for (let n = 0; n < 0x7fffffff; n++) {
    const s = n.toString()
    let len = prefix.length
    for (let i = 0; i < s.length; i++) buf[len++] = s.charCodeAt(i)
    if ((firstWord(buf, len) & mask) === 0) return s
  }
  throw new Error('no solution')
}
