"use strict";
// Web Worker: derives the login key from the master password exactly like
// the hearth client (Argon2id -> HKDF-SHA256 "hearth-auth"), so the
// password never leaves the browser; only the derived key is sent. It also
// returns the key that unwraps the vault key ("hearth-kek"), which stays in
// the browser (one-time install commands).
importScripts("/app/blake2b.js", "/app/argon2.js", "/app/seal.js");

self.onmessage = function (ev) {
  var d = ev.data, kdf = d.kdf;
  try {
    if (kdf.time < 1 || kdf.memory_kib < 19 * 1024 || kdf.threads < 1 || kdf.memory_kib > 4 * 1024 * 1024 || kdf.time > 100)
      throw new Error("unexpected key derivation parameters from the server");
    var last = 0;
    var master = argon2id(new TextEncoder().encode(d.password), HC.b64dec(d.salt), kdf.time, kdf.memory_kib, kdf.threads, 32, function (f) {
      if (f - last >= 0.02) { last = f; self.postMessage({ progress: f }); }
    });
    self.postMessage({ authKey: HC.b64enc(HC.hkdf(master, "hearth-auth")), kek: HC.b64enc(HC.hkdf(master, "hearth-kek")) });
  } catch (e) {
    self.postMessage({ error: String(e.message || e) });
  }
};
