// 客户端口令哈希：与服务端 `models.StaticHash` 必须逐字节一致 ——
// sha256(明文 + "-" + 固定盐) 的小写十六进制。算错不会被拒，而是**静默写坏账号**
// （服务端存成 bcrypt(明文)，那个账号从此永远登不进去），所以盐与格式只能是这一份。
//
// 管理后台（app.js）与浏览应用（home.js）共用本文件：一份实现、两处引用，
// 换盐时不可能只改一半。

// SALT 与服务端 models.StaticHashSalt 保持一致，改这里会让老客户端登不进来。
export const SALT = 'https://github.com/alist-org/alist';

// sha256Hex 纯 JS 实现，**只在浏览器不提供 crypto.subtle 时兜底**。
//
// 为什么必须有它：浏览器只在「安全上下文」（HTTPS，或 localhost/127.0.0.1）
// 才提供 crypto.subtle。自建媒体服务器基本都是从局域网里用
// http://192.168.x.x:8000 这种地址打开的 —— 那是非安全上下文，crypto.subtle
// 为 undefined，调用它直接抛 TypeError，**登录按钮会毫无反应**。
// 用法与 WebCrypto 等价（同样是小写十六进制、同样对 UTF-8 字节做摘要）。
export function sha256Hex(str) {
  const K = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];
  const H = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
    0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ]);
  const rr = (x, n) => (x >>> n) | (x << (32 - n));
  const bytes = new TextEncoder().encode(str);
  const len = bytes.length;
  // 末尾补 0x80、补零到 64 字节整数倍，最后 8 字节放比特长度（大端）
  const total = (((len + 8) >> 6) + 1) << 6;
  const buf = new Uint8Array(total);
  buf.set(bytes);
  buf[len] = 0x80;
  const dv = new DataView(buf.buffer);
  dv.setUint32(total - 8, Math.floor(len / 536870912)); // 高 32 位 = len*8 / 2^32
  dv.setUint32(total - 4, (len << 3) >>> 0);
  const w = new Uint32Array(64);
  for (let off = 0; off < total; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = dv.getUint32(off + i * 4);
    for (let i = 16; i < 64; i++) {
      const s0 = rr(w[i - 15], 7) ^ rr(w[i - 15], 18) ^ (w[i - 15] >>> 3);
      const s1 = rr(w[i - 2], 17) ^ rr(w[i - 2], 19) ^ (w[i - 2] >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
    }
    let [a, b, c, d, e, f, g, h] = H;
    for (let i = 0; i < 64; i++) {
      const S1 = rr(e, 6) ^ rr(e, 11) ^ rr(e, 25);
      const ch = (e & f) ^ (~e & g);
      const t1 = (h + S1 + ch + K[i] + w[i]) >>> 0;
      const S0 = rr(a, 2) ^ rr(a, 13) ^ rr(a, 22);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + maj) >>> 0;
      h = g; g = f; f = e; e = (d + t1) >>> 0;
      d = c; c = b; b = a; a = (t1 + t2) >>> 0;
    }
    const hh = [a, b, c, d, e, f, g, h];
    for (let i = 0; i < 8; i++) H[i] = (H[i] + hh[i]) >>> 0;
  }
  return [...H].map(x => x.toString(16).padStart(8, '0')).join('');
}

/**
 * staticHash 计算要发给服务端的口令哈希（登录、改密、目录密码都用它）。
 * 安全上下文走原生实现，否则回退到内置 SHA-256。
 */
export async function staticHash(pwd) {
  const msg = pwd + '-' + SALT;
  if (globalThis.crypto?.subtle) {
    const buf = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(msg));
    return [...new Uint8Array(buf)].map(b => b.toString(16).padStart(2, '0')).join('');
  }
  console.warn('当前不是安全上下文（HTTPS/localhost），crypto.subtle 不可用，已改用内置 SHA-256');
  return sha256Hex(msg);
}
