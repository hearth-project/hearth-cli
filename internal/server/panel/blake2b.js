"use strict";
// BLAKE2b (RFC 7693), unkeyed, any output length 1..64, 64-bit words as
// (lo, hi) pairs of 32-bit integers. Used by the Argon2id worker.
var BLAKE2B_IV = new Uint32Array([0xf3bcc908, 0x6a09e667, 0x84caa73b, 0xbb67ae85, 0xfe94f82b, 0x3c6ef372, 0x5f1d36f1, 0xa54ff53a,
  0xade682d1, 0x510e527f, 0x2b3e6c1f, 0x9b05688c, 0xfb41bd6b, 0x1f83d9ab, 0x137e2179, 0x5be0cd19]);
var BLAKE2B_SIGMA = [
  0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3,
  11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4, 7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8,
  9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13, 2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9,
  12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11, 13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10,
  6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5, 10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0,
  0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3];

function b2bAdd(v, a, b) { // v[a] += v[b] (64-bit)
  var lo = v[a] + v[b], hi = v[a + 1] + v[b + 1];
  if (lo >= 0x100000000) hi++;
  v[a] = lo; v[a + 1] = hi;
}
function b2bAddM(v, a, m, x) { // v[a] += m[x]
  var lo = v[a] + m[x], hi = v[a + 1] + m[x + 1];
  if (lo >= 0x100000000) hi++;
  v[a] = lo; v[a + 1] = hi;
}
function b2bG(v, m, a, b, c, d, x, y) {
  b2bAdd(v, a, b); b2bAddM(v, a, m, x);
  var xl = v[d] ^ v[a], xh = v[d + 1] ^ v[a + 1]; v[d] = xh; v[d + 1] = xl;            // rotr 32
  b2bAdd(v, c, d);
  xl = v[b] ^ v[c]; xh = v[b + 1] ^ v[c + 1];
  v[b] = (xl >>> 24) | (xh << 8); v[b + 1] = (xh >>> 24) | (xl << 8);                  // rotr 24
  b2bAdd(v, a, b); b2bAddM(v, a, m, y);
  xl = v[d] ^ v[a]; xh = v[d + 1] ^ v[a + 1];
  v[d] = (xl >>> 16) | (xh << 16); v[d + 1] = (xh >>> 16) | (xl << 16);                // rotr 16
  b2bAdd(v, c, d);
  xl = v[b] ^ v[c]; xh = v[b + 1] ^ v[c + 1];
  v[b] = (xh >>> 31) | (xl << 1); v[b + 1] = (xl >>> 31) | (xh << 1);                  // rotr 63
}

function blake2bCompress(h, block, t, last) {
  var v = new Uint32Array(32), m = new Uint32Array(32), i;
  for (i = 0; i < 16; i++) { v[i] = h[i]; v[i + 16] = BLAKE2B_IV[i]; }
  v[24] ^= t; v[25] ^= (t / 0x100000000) >>> 0;
  if (last) { v[28] = ~v[28]; v[29] = ~v[29]; }
  for (i = 0; i < 32; i++) m[i] = block[i * 4] | (block[i * 4 + 1] << 8) | (block[i * 4 + 2] << 16) | (block[i * 4 + 3] << 24);
  for (i = 0; i < 12; i++) {
    var s = BLAKE2B_SIGMA.slice(i * 16, i * 16 + 16);
    b2bG(v, m, 0, 8, 16, 24, s[0] * 2, s[1] * 2); b2bG(v, m, 2, 10, 18, 26, s[2] * 2, s[3] * 2);
    b2bG(v, m, 4, 12, 20, 28, s[4] * 2, s[5] * 2); b2bG(v, m, 6, 14, 22, 30, s[6] * 2, s[7] * 2);
    b2bG(v, m, 0, 10, 20, 30, s[8] * 2, s[9] * 2); b2bG(v, m, 2, 12, 22, 24, s[10] * 2, s[11] * 2);
    b2bG(v, m, 4, 14, 16, 26, s[12] * 2, s[13] * 2); b2bG(v, m, 6, 8, 18, 28, s[14] * 2, s[15] * 2);
  }
  for (i = 0; i < 16; i++) h[i] ^= v[i] ^ v[i + 16];
}

// blake2b returns the outLen-byte BLAKE2b digest of data (Uint8Array).
function blake2b(outLen, data) {
  var h = new Uint32Array(BLAKE2B_IV);
  h[0] ^= 0x01010000 ^ outLen;
  var block = new Uint8Array(128), t = 0, n = data.length, off = 0;
  while (n - off > 128) {
    block.set(data.subarray(off, off + 128));
    t += 128;
    blake2bCompress(h, block, t, false);
    off += 128;
  }
  block.fill(0);
  block.set(data.subarray(off));
  t += n - off;
  blake2bCompress(h, block, t, true);
  var out = new Uint8Array(outLen);
  for (var i = 0; i < outLen; i++) out[i] = h[i >> 2] >>> (8 * (i & 3));
  return out;
}
