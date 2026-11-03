"use strict";
// Sign-in screen, navigation and view switching.

// A console opened by the menu bar app carries this device's vault key in
// the URL fragment (#hearth-vk=..., never sent to the server): taken here,
// removed from the address at once, then kept like a password sign-in's.
var HANDED_VK = null;
(function () {
  var m = /(?:^#|&)hearth-vk=([A-Za-z0-9_-]+)/.exec(location.hash);
  if (!m) return;
  try { var k = HC.b64dec(m[1].replace(/-/g, "+").replace(/_/g, "/") + "===".slice((m[1].length + 3) % 4)); if (k.length === 32) HANDED_VK = k; } catch (e) { HANDED_VK = null; }
  history.replaceState(null, "", location.pathname + location.search);
})();
var ME = "", IS_ADMIN = false, VIEWS = {}, ORDER = [], current = location.hash.slice(1) || "home", pendingCount = 0;
// view registers a page: group "me" (everyone) or "admin".
function view(id, title, fn, group, ic) { VIEWS[id] = { title: title, fn: fn, group: group || "admin", icon: ic || "chart" }; ORDER.push(id); }

function showShell(on) {
  document.getElementById("shell").classList.toggle("hidden", !on);
  var a = document.getElementById("auth");
  if (a) a.classList.toggle("hidden", on);
}

function showLogin() {
  forgetVault();
  showShell(false);
  var auth = document.getElementById("auth");
  if (auth) auth.remove();
  var email = h("input", { type: "email", autocomplete: "username", placeholder: "you@example.com", required: true });
  var pw = h("input", { type: "password", autocomplete: "current-password", placeholder: t("Master password"), required: true });
  var bar = h("i", {}), prog = h("div", { class: "progress hidden" }, h("span", { class: "t" }, bar), h("span", { class: "muted small" }, t("Checking the password in this browser…")));
  var err = h("p", { class: "err" });
  var btn = h("button", { class: "pri", type: "submit" }, t("Sign in"));
  var remember = h("input", { type: "checkbox", checked: true });
  var form = h("form", {
    class: "loginform", onsubmit: function (ev) {
      ev.preventDefault();
      err.textContent = ""; btn.disabled = true;
      prog.classList.remove("hidden"); bar.style.width = "0%";
      passwordLogin(email.value.trim(), pw.value, remember.checked, function (f) { bar.style.width = (100 * f).toFixed(0) + "%"; }).then(function () {
        pw.value = "";
        location.hash = "home";
        location.reload();
      }, function (e) {
        err.textContent = e.message; btn.disabled = false; prog.classList.add("hidden");
      });
    }
  }, h("label", {}, t("Email"), email), h("label", {}, t("Master password"), pw),
    h("label", { class: "remember" }, remember, h("span", {}, t("Remember this computer for 7 days"))), prog, err, btn);
  document.body.appendChild(h("div", { id: "auth", class: "auth" }, h("div", { class: "box" },
    h("div", { class: "brand" }, h("span", { class: "logo" }, icon("flame")), "Hearth"),
    h("h2", {}, t("Sign in")),
    h("p", { class: "lead" }, t("Your machines, skills, agents and teams. The password is checked in this browser; only a derived key is sent.")),
    form,
    h("details", { class: "other" }, h("summary", {}, t("Other ways to sign in")),
      h("p", {}, t("On a machine where you are logged in:")), h("p", { class: "code" }, "hearth panel"),
      h("p", {}, t("or on the server:")), h("p", { class: "code" }, "hearth server admin link <email> --url <public-url> --data <dir>")))));
  email.focus();
}

// deriveKeys runs the client's key derivation (Argon2id + HKDF) in a
// worker: the login key for the server and the key that unwraps the vault
// key, which never leaves this page.
function deriveKeys(email, password, onProgress) {
  return fetch("/v1/auth/prelogin", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: email }) })
    .then(function (r) { return r.json().then(function (j) { if (!r.ok) throw new Error(t(j.error || r.statusText)); return j; }); })
    .then(function (pre) {
      return new Promise(function (resolve, reject) {
        var w = new Worker("/app/kdf.js");
        w.onmessage = function (ev) {
          var d = ev.data;
          if (d.progress !== undefined) { if (onProgress) onProgress(d.progress); return; }
          w.terminate();
          if (d.error) reject(new Error(d.error)); else resolve(d);
        };
        w.onerror = function (e) { w.terminate(); reject(new Error(e.message || "worker failed")); };
        w.postMessage({ password: password, salt: pre.salt, kdf: pre.kdf });
      });
    });
}

// VAULT_KEY: the account's vault key, unwrapped in this page from the
// password typed into it (sign-in, or the install-command prompt). Only
// used to make one-time install commands. The page keeps it for the whole
// session: sealed with the session's wrap key (derived by the server from
// the HttpOnly session cookie, GET /v1/me/vault-wrap) in localStorage. The
// sealed copy is useless without the session, and the server never sees it.
// Signing out (or an expired session) removes it.
var VAULT_KEY = null, VAULT_AAD = "hearth/console-vault/v1";
try { sessionStorage.removeItem("hearth-vk"); } catch (e) { /* older pages */ }

function keepVault(vk, wrapKey) {
  VAULT_KEY = vk;
  try { localStorage.setItem("hearth-vk", HC.b64enc(HC.seal(HC.b64dec(wrapKey), vk, HC.utf8(VAULT_AAD)))); } catch (e) { /* asked again after a reload */ }
}
function forgetVault() {
  VAULT_KEY = null;
  try { localStorage.removeItem("hearth-vk"); } catch (e) { /* nothing kept */ }
}
// loadVault resolves to the vault key kept for this session, or null.
function loadVault() {
  if (VAULT_KEY) return Promise.resolve(VAULT_KEY);
  var sealed = null;
  try { sealed = localStorage.getItem("hearth-vk"); } catch (e) { sealed = null; }
  if (!sealed) return Promise.resolve(null);
  return me("GET", "/vault-wrap").then(function (r) {
    try { VAULT_KEY = HC.open(HC.b64dec(r.wrap_key), HC.b64dec(sealed), HC.utf8(VAULT_AAD)); } catch (e) { forgetVault(); }
    return VAULT_KEY;
  }, function () { return null; });
}

function unwrapVault(kek, encVaultKey) {
  return HC.open(HC.b64dec(kek), HC.b64dec(encVaultKey), HC.utf8("hearth/vault-key/kek/v1"));
}

// passwordLogin exchanges the derived login key for the session cookie.
function passwordLogin(email, password, remember, onProgress) {
  var keys;
  return deriveKeys(email, password, onProgress)
    .then(function (k) {
      keys = k;
      return fetch("/v1/web/login", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: email, auth_key: k.authKey, remember: remember }) });
    })
    .then(function (r) {
      return r.text().then(function (txt) {
        var j = {};
        try { j = txt ? JSON.parse(txt) : {}; } catch (e) { j = {}; }
        if (!r.ok) throw new Error(t(j.error || r.statusText));
        if (j.enc_vault_key && j.wrap_key) {
          try { keepVault(unwrapVault(keys.kek, j.enc_vault_key), j.wrap_key); } catch (e) { /* install commands ask for the password */ }
        }
      });
    });
}

function drawTabs() {
  var nav = document.getElementById("tabs");
  nav.textContent = "";
  [["me", "My account"], ["admin", "Administration"]].forEach(function (g) {
    if (g[0] === "admin" && !IS_ADMIN) return;
    nav.appendChild(h("div", { class: "navgroup" }, t(g[1])));
    ORDER.forEach(function (id) {
      var v = VIEWS[id];
      if (v.group !== g[0]) return;
      nav.appendChild(h("a", { class: "t-" + v.icon + (id === current ? " on" : ""), href: "#" + id, onclick: function () { document.getElementById("side").classList.remove("open"); } },
        icon(v.icon), h("span", {}, t(v.title)), id === "approvals" && pendingCount ? badge(pendingCount, "warn") : null));
    });
  });
}

function render() {
  if (!VIEWS[current] || (VIEWS[current].group === "admin" && !IS_ADMIN)) current = "home";
  drawTabs();
  document.getElementById("title").textContent = t(VIEWS[current].title);
  document.title = t(VIEWS[current].title) + " · Hearth";
  var main = document.getElementById("main");
  Promise.resolve(VIEWS[current].fn()).then(function (node) {
    main.textContent = "";
    add(main, node);
  }, function (e) { if (e.message !== "unauthorized") { main.textContent = ""; main.appendChild(empty(e.message, "⚠️")); } });
}
