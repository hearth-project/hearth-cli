"use strict";
// Crypto shared by the console page and kdf.js, matching the hearth client
// (internal/crypto): SHA-256, HMAC-SHA256, HKDF-SHA256 (empty salt, 32
// bytes) and XChaCha20-Poly1305 sealed as nonce || ciphertext || tag.
// Plain JavaScript: the console may be served over plain HTTP, where
// WebCrypto is not available. Only short keys pass through here.
var HC = (function () {
  var K = new Uint32Array([0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
    0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb,
    0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f,
    0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2]);

  function cat(parts) {
    var n = 0, i;
    for (i = 0; i < parts.length; i++) n += parts[i].length;
    var out = new Uint8Array(n), o = 0;
    for (i = 0; i < parts.length; i++) { out.set(parts[i], o); o += parts[i].length; }
    return out;
  }
  function utf8(s) { return new TextEncoder().encode(s); }

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
  // hkdf: HKDF-SHA256 with an empty salt, 32 bytes (one block), like
  // crypto.HKDF in the client.
  function hkdf(secret, info) {
    var prk = hmac(new Uint8Array(32), secret);
    return hmac(prk, cat([utf8(info), new Uint8Array([1])]));
  }

  // ChaCha20 (RFC 8439) and HChaCha20.
  function u32(b, o) { return (b[o] | (b[o + 1] << 8) | (b[o + 2] << 16) | (b[o + 3] << 24)) >>> 0; }
  function rotl(v, n) { return (v << n) | (v >>> (32 - n)); }
  function qr(x, a, b, c, d) {
    x[a] += x[b]; x[d] = rotl(x[d] ^ x[a], 16); x[c] += x[d]; x[b] = rotl(x[b] ^ x[c], 12);
    x[a] += x[b]; x[d] = rotl(x[d] ^ x[a], 8); x[c] += x[d]; x[b] = rotl(x[b] ^ x[c], 7);
  }
  function rounds(s) {
    var x = new Uint32Array(s);
    for (var i = 0; i < 10; i++) {
      qr(x, 0, 4, 8, 12); qr(x, 1, 5, 9, 13); qr(x, 2, 6, 10, 14); qr(x, 3, 7, 11, 15);
      qr(x, 0, 5, 10, 15); qr(x, 1, 6, 11, 12); qr(x, 2, 7, 8, 13); qr(x, 3, 4, 9, 14);
    }
    return x;
  }
  function state(key, words) {
    var s = new Uint32Array(16);
    s[0] = 0x61707865; s[1] = 0x3320646e; s[2] = 0x79622d32; s[3] = 0x6b206574;
    for (var i = 0; i < 8; i++) s[4 + i] = u32(key, 4 * i);
    for (i = 0; i < 4; i++) s[12 + i] = words[i];
    return s;
  }
  function block(key, counter, nonce) {
    var s = state(key, [counter, u32(nonce, 0), u32(nonce, 4), u32(nonce, 8)]), x = rounds(s), out = new Uint8Array(64);
    for (var i = 0; i < 16; i++) {
      var v = (x[i] + s[i]) >>> 0;
      out[4 * i] = v; out[4 * i + 1] = v >>> 8; out[4 * i + 2] = v >>> 16; out[4 * i + 3] = v >>> 24;
    }
    return out;
  }
  function stream(key, nonce, counter, data) {
    var out = new Uint8Array(data.length);
    for (var o = 0; o < data.length; o += 64) {
      var ks = block(key, counter++, nonce);
      for (var j = 0; j < 64 && o + j < data.length; j++) out[o + j] = data[o + j] ^ ks[j];
    }
    return out;
  }
  function hchacha(key, n16) {
    var x = rounds(state(key, [u32(n16, 0), u32(n16, 4), u32(n16, 8), u32(n16, 12)])), out = new Uint8Array(32);
    [0, 1, 2, 3, 12, 13, 14, 15].forEach(function (w, i) {
      out[4 * i] = x[w]; out[4 * i + 1] = x[w] >>> 8; out[4 * i + 2] = x[w] >>> 16; out[4 * i + 3] = x[w] >>> 24;
    });
    return out;
  }

  // Poly1305 with BigInt (messages here are a few dozen bytes).
  function le(b) { var n = BigInt(0); for (var i = b.length - 1; i >= 0; i--) n = (n << BigInt(8)) | BigInt(b[i]); return n; }
  function poly1305(key, msg) {
    var r = le(key.subarray(0, 16)) & BigInt("0x0ffffffc0ffffffc0ffffffc0fffffff"), s = le(key.subarray(16, 32));
    var p = (BigInt(1) << BigInt(130)) - BigInt(5), a = BigInt(0);
    for (var o = 0; o < msg.length; o += 16) {
      var c = msg.subarray(o, Math.min(o + 16, msg.length));
      a = ((a + le(c) + (BigInt(1) << BigInt(8 * c.length))) * r) % p;
    }
    a = (a + s) & ((BigInt(1) << BigInt(128)) - BigInt(1));
    var out = new Uint8Array(16);
    for (var i = 0; i < 16; i++) { out[i] = Number(a & BigInt(255)); a >>= BigInt(8); }
    return out;
  }
  function macData(aad, ct) {
    var pad = function (n) { return new Uint8Array((16 - n % 16) % 16); }, lens = new Uint8Array(16);
    [aad.length, ct.length].forEach(function (n, i) { for (var j = 0; j < 4; j++) lens[8 * i + j] = n >>> (8 * j); });
    return cat([aad, pad(aad.length), ct, pad(ct.length), lens]);
  }
  // xchacha derives the subkey and the 12-byte nonce of XChaCha20.
  function xchacha(key, nonce) {
    return { key: hchacha(key, nonce.subarray(0, 16)), nonce: cat([new Uint8Array(4), nonce.subarray(16, 24)]) };
  }
  function random(n) { var b = new Uint8Array(n); crypto.getRandomValues(b); return b; }

  // seal encrypts like crypto.Seal: XChaCha20-Poly1305, random nonce first.
  function seal(key, plain, aad) {
    var nonce = random(24), x = xchacha(key, nonce);
    var ct = stream(x.key, x.nonce, 1, plain);
    return cat([nonce, ct, poly1305(block(x.key, 0, x.nonce).subarray(0, 32), macData(aad, ct))]);
  }
  // open reverses seal and throws when the key, AAD or data do not match.
  function open(key, data, aad) {
    if (data.length < 40) throw new Error("decryption failed");
    var nonce = data.subarray(0, 24), ct = data.subarray(24, data.length - 16), tag = data.subarray(data.length - 16);
    var x = xchacha(key, nonce), want = poly1305(block(x.key, 0, x.nonce).subarray(0, 32), macData(aad, ct)), d = 0;
    for (var i = 0; i < 16; i++) d |= want[i] ^ tag[i];
    if (d) throw new Error("decryption failed");
    return stream(x.key, x.nonce, 1, ct);
  }

  function b64enc(u) { var s = ""; for (var i = 0; i < u.length; i++) s += String.fromCharCode(u[i]); return btoa(s); }
  function b64dec(s) { var b = atob(s), out = new Uint8Array(b.length); for (var i = 0; i < b.length; i++) out[i] = b.charCodeAt(i); return out; }
  function b64url(u) { return b64enc(u).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""); }

  return { cat: cat, utf8: utf8, sha256: sha256, hmac: hmac, hkdf: hkdf, seal: seal, open: open, random: random,
    b64enc: b64enc, b64dec: b64dec, b64url: b64url };
})();
if (typeof module !== "undefined") module.exports = HC;
