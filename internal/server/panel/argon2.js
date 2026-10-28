"use strict";
// Argon2id v1.3 (RFC 9106) in plain JS, matching golang.org/x/crypto/argon2
// IDKey (no secret, no associated data). Memory: one Uint32Array, each
// 1 KiB block = 128 words of (lo, hi). Needs blake2b.js.

function le32(n) { return new Uint8Array([n & 255, (n >>> 8) & 255, (n >>> 16) & 255, (n >>> 24) & 255]); }
function cat(parts) {
  var n = 0, i, o = 0;
  for (i = 0; i < parts.length; i++) n += parts[i].length;
  var out = new Uint8Array(n);
  for (i = 0; i < parts.length; i++) { out.set(parts[i], o); o += parts[i].length; }
  return out;
}
// hPrime is Argon2's variable-length hash H'.
function hPrime(outLen, input) {
  var x = cat([le32(outLen), input]);
  if (outLen <= 64) return blake2b(outLen, x);
  var out = new Uint8Array(outLen), v = blake2b(64, x), o = 0;
  out.set(v.subarray(0, 32), 0); o = 32;
  while (outLen - o > 64) { v = blake2b(64, v); out.set(v.subarray(0, 32), o); o += 32; }
  out.set(blake2b(outLen - o, v), o);
  return out;
}

// 64-bit helpers on (lo, hi) pairs at even indexes of a Uint32Array.
function fBlaMka(v, a, b) { // v[a] = v[a] + v[b] + 2 * lo(v[a]) * lo(v[b])
  var al = v[a], bl = v[b];
  var a0 = al & 0xffff, a1 = al >>> 16, b0 = bl & 0xffff, b1 = bl >>> 16;
  var mid = a0 * b1 + a1 * b0;
  var plo = a0 * b0 + (mid % 65536) * 65536;
  var phi = a1 * b1 + Math.floor(mid / 65536) + Math.floor(plo / 4294967296);
  plo = plo >>> 0;
  phi = ((phi << 1) | (plo >>> 31)) >>> 0;
  plo = (plo << 1) >>> 0;
  var lo = al + bl + plo;
  var hi = v[a + 1] + v[b + 1] + phi + Math.floor(lo / 4294967296);
  v[a] = lo; v[a + 1] = hi;
}
function gb(v, a, b, c, d) {
  var xl, xh;
  fBlaMka(v, a, b);
  xl = v[d] ^ v[a]; xh = v[d + 1] ^ v[a + 1]; v[d] = xh; v[d + 1] = xl;
  fBlaMka(v, c, d);
  xl = v[b] ^ v[c]; xh = v[b + 1] ^ v[c + 1];
  v[b] = (xl >>> 24) | (xh << 8); v[b + 1] = (xh >>> 24) | (xl << 8);
  fBlaMka(v, a, b);
  xl = v[d] ^ v[a]; xh = v[d + 1] ^ v[a + 1];
  v[d] = (xl >>> 16) | (xh << 16); v[d + 1] = (xh >>> 16) | (xl << 16);
  fBlaMka(v, c, d);
  xl = v[b] ^ v[c]; xh = v[b + 1] ^ v[c + 1];
  v[b] = (xh >>> 31) | (xl << 1); v[b + 1] = (xl >>> 31) | (xh << 1);
}
// blamka permutes 16 words given by their word indexes w[0..15].
function blamka(v, w) {
  gb(v, w[0], w[4], w[8], w[12]); gb(v, w[1], w[5], w[9], w[13]);
  gb(v, w[2], w[6], w[10], w[14]); gb(v, w[3], w[7], w[11], w[15]);
  gb(v, w[0], w[5], w[10], w[15]); gb(v, w[1], w[6], w[11], w[12]);
  gb(v, w[2], w[7], w[8], w[13]); gb(v, w[3], w[4], w[9], w[14]);
}
var ROWS = [], COLS = [];
(function () {
  for (var i = 0; i < 128; i += 16) { var r = []; for (var j = 0; j < 16; j++) r.push((i + j) * 2); ROWS.push(r); }
  for (i = 0; i < 16; i += 2) {
    var c = [];
    for (j = 0; j < 8; j++) { c.push((i + 16 * j) * 2, (i + 16 * j + 1) * 2); }
    COLS.push(c);
  }
})();
var T = new Uint32Array(256), R = new Uint32Array(256);
// compressXOR: out ^= G(in1, in2), blocks given as word offsets into mem.
function compressXOR(mem, out, in1, in2, outArr) {
  var i, dst = outArr || mem;
  for (i = 0; i < 256; i++) { R[i] = mem[in1 + i] ^ (in2 < 0 ? 0 : mem[in2 + i]); T[i] = R[i]; }
  for (i = 0; i < 8; i++) blamka(T, ROWS[i]);
  for (i = 0; i < 8; i++) blamka(T, COLS[i]);
  for (i = 0; i < 256; i++) dst[out + i] ^= R[i] ^ T[i];
}

// argon2id returns keyLen bytes. progress(fraction) is called now and then.
function argon2id(password, salt, time, memoryKiB, threads, keyLen, progress) {
  var sync = 4;
  var memory = Math.floor(memoryKiB / (sync * threads)) * (sync * threads);
  if (memory < 2 * sync * threads) memory = 2 * sync * threads;
  var lanes = memory / threads, segments = lanes / sync;
  var h0 = blake2b(64, cat([le32(threads), le32(keyLen), le32(memoryKiB), le32(time), le32(0x13), le32(2),
    le32(password.length), password, le32(salt.length), salt, le32(0), le32(0)]));
  var mem = new Uint32Array(memory * 256), i, k;
  for (var lane = 0; lane < threads; lane++) {
    for (k = 0; k < 2; k++) {
      var b = hPrime(1024, cat([h0, le32(k), le32(lane)]));
      var base = (lane * lanes + k) * 256;
      for (i = 0; i < 256; i++) mem[base + i] = b[i * 4] | (b[i * 4 + 1] << 8) | (b[i * 4 + 2] << 16) | (b[i * 4 + 3] << 24);
    }
  }
  var addr = new Uint32Array(256), input = new Uint32Array(256), zero = new Uint32Array(256);
  var scratch = new Uint32Array(512); // [0,256): input, [256,512): address block
  function nextAddresses() {
    input[12]++; // in[6] lo
    scratch.set(input, 0); scratch.fill(0, 256);
    compressXOR(scratch, 256, 0, -1);           // addresses = G(zero, in)
    scratch.copyWithin(0, 256, 512); scratch.fill(0, 256);
    compressXOR(scratch, 256, 0, -1);           // addresses = G(zero, addresses)
    addr.set(scratch.subarray(256, 512));
  }
  var done = 0, total = time * sync * threads;
  for (var n = 0; n < time; n++) {
    for (var slice = 0; slice < sync; slice++) {
      for (lane = 0; lane < threads; lane++) {
        var indep = n === 0 && slice < sync / 2;
        if (indep) {
          input.fill(0);
          input[0] = n; input[2] = lane; input[4] = slice; input[6] = memory; input[8] = time; input[10] = 2;
        }
        var index = 0;
        if (n === 0 && slice === 0) {
          index = 2;
          if (indep) nextAddresses();
        }
        var offset = lane * lanes + slice * segments + index;
        for (; index < segments; index++, offset++) {
          var prev = offset - 1;
          if (index === 0 && slice === 0) prev += lanes;
          var rlo, rhi;
          if (indep) {
            if (index % 128 === 0) nextAddresses();
            rlo = addr[(index % 128) * 2]; rhi = addr[(index % 128) * 2 + 1];
          } else {
            rlo = mem[prev * 256]; rhi = mem[prev * 256 + 1];
          }
          var refLane = rhi % threads;
          if (n === 0 && slice === 0) refLane = lane;
          var m = 3 * segments, s = ((slice + 1) % sync) * segments;
          if (lane === refLane) m += index;
          if (n === 0) {
            m = slice * segments; s = 0;
            if (slice === 0 || lane === refLane) m += index;
          }
          if (index === 0 || lane === refLane) m--;
          // phi: p = rlo^2 >> 32; p = p*m >> 32
          var x = rlo, x0 = x & 0xffff, x1 = x >>> 16;
          var mid = 2 * x0 * x1;
          var sqHi = x1 * x1 + Math.floor((x0 * x0 + (mid % 65536) * 65536) / 4294967296) + Math.floor(mid / 65536);
          sqHi = sqHi >>> 0;
          var p = Math.floor((sqHi * m) / 4294967296);
          var ref = refLane * lanes + ((s + m - (p + 1)) % lanes);
          compressXOR(mem, offset * 256, prev * 256, ref * 256);
        }
        done++;
        if (progress) progress(done / total);
      }
    }
  }
  var fin = new Uint32Array(256);
  for (lane = 0; lane < threads; lane++) {
    var last = (lane * lanes + lanes - 1) * 256;
    for (i = 0; i < 256; i++) fin[i] ^= mem[last + i];
  }
  var bytes = new Uint8Array(1024);
  for (i = 0; i < 256; i++) { bytes[i * 4] = fin[i]; bytes[i * 4 + 1] = fin[i] >>> 8; bytes[i * 4 + 2] = fin[i] >>> 16; bytes[i * 4 + 3] = fin[i] >>> 24; }
  return hPrime(keyLen, bytes);
}
