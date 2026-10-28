"use strict";
// Web Worker: derives the login key from the master password exactly like
// the hearth client (Argon2id -> HKDF-SHA256 "hearth-auth"), so the
// password never leaves the browser; only the derived key is sent.
importScripts("/app/blake2b.js", "/app/argon2.js");

var K = new Uint32Array([0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
  0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb,
  0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f,
  0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2]);

function sha256(data) {
  var h = new Uint32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);
  var l = data.length, n = ((l + 9 + 63) >> 6) << 6, buf = new Uint8Array(n);
  buf.set(data); buf[l] = 0x80;
  var bits = l * 8;
  buf[n - 4] = bits >>> 24; buf[n - 3] = bits >>> 16; buf[n - 2] = bits >>> 8; buf[n - 1] = bits;
  buf[n - 5] = Math.floor(bits / 4294967296);
  var w = new Uint32Array(64);
  for (var o = 0; o < n; o += 64) {
    var i;
    for (i = 0; i < 16; i++) w[i] = (buf[o + i * 4] << 24) | (buf[o + i * 4 + 1] << 16) | (buf[o + i * 4 + 2] << 8) | buf[o + i * 4 + 3];
    for (i = 16; i < 64; i++) {
      var a = w[i - 15], b = w[i - 2];
      var s0 = ((a >>> 7) | (a << 25)) ^ ((a >>> 18) | (a << 14)) ^ (a >>> 3);
      var s1 = ((b >>> 17) | (b << 15)) ^ ((b >>> 19) | (b << 13)) ^ (b >>> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    var A = h[0], B = h[1], C = h[2], D = h[3], E = h[4], F = h[5], G = h[6], H = h[7];
    for (i = 0; i < 64; i++) {
      var S1 = ((E >>> 6) | (E << 26)) ^ ((E >>> 11) | (E << 21)) ^ ((E >>> 25) | (E << 7));
      var t1 = (H + S1 + ((E & F) ^ (~E & G)) + K[i] + w[i]) | 0;
      var S0 = ((A >>> 2) | (A << 30)) ^ ((A >>> 13) | (A << 19)) ^ ((A >>> 22) | (A << 10));
      var t2 = (S0 + ((A & B) ^ (A & C) ^ (B & C))) | 0;
      H = G; G = F; F = E; E = (D + t1) | 0; D = C; C = B; B = A; A = (t1 + t2) | 0;
    }
    h[0] += A; h[1] += B; h[2] += C; h[3] += D; h[4] += E; h[5] += F; h[6] += G; h[7] += H;
  }
  var out = new Uint8Array(32);
  for (var j = 0; j < 32; j++) out[j] = h[j >> 2] >>> (24 - 8 * (j & 3));
  return out;
}
function hmac(key, msg) {
  if (key.length > 64) key = sha256(key);
  var k = new Uint8Array(64); k.set(key);
  var ip = new Uint8Array(64), op = new Uint8Array(64);
  for (var i = 0; i < 64; i++) { ip[i] = k[i] ^ 0x36; op[i] = k[i] ^ 0x5c; }
  return sha256(cat([op, sha256(cat([ip, msg]))]));
}
// hkdf32: HKDF-SHA256 with an empty salt, 32 bytes (one block).
function hkdf32(secret, info) {
  var prk = hmac(new Uint8Array(32), secret);
  return hmac(prk, cat([new TextEncoder().encode(info), new Uint8Array([1])]));
}

function b64dec(s) { var b = atob(s), out = new Uint8Array(b.length); for (var i = 0; i < b.length; i++) out[i] = b.charCodeAt(i); return out; }
function b64enc(u) { var s = ""; for (var i = 0; i < u.length; i++) s += String.fromCharCode(u[i]); return btoa(s); }

self.onmessage = function (ev) {
  var d = ev.data, kdf = d.kdf;
  try {
    if (kdf.time < 1 || kdf.memory_kib < 19 * 1024 || kdf.threads < 1 || kdf.memory_kib > 4 * 1024 * 1024 || kdf.time > 100)
      throw new Error("unexpected key derivation parameters from the server");
    var last = 0;
    var master = argon2id(new TextEncoder().encode(d.password), b64dec(d.salt), kdf.time, kdf.memory_kib, kdf.threads, 32, function (f) {
      if (f - last >= 0.02) { last = f; self.postMessage({ progress: f }); }
    });
    self.postMessage({ authKey: b64enc(hkdf32(master, "hearth-auth")) });
  } catch (e) {
    self.postMessage({ error: String(e.message || e) });
  }
};
