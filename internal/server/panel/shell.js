"use strict";
// Sign-in screen, navigation and view switching.

var ME = "", IS_ADMIN = false, VIEWS = {}, ORDER = [], current = location.hash.slice(1) || "home", pendingCount = 0;
// view registers a page: group "me" (everyone) or "admin".
function view(id, title, fn, group, ic) { VIEWS[id] = { title: title, fn: fn, group: group || "admin", icon: ic || "chart" }; ORDER.push(id); }

function showShell(on) {
  document.getElementById("shell").classList.toggle("hidden", !on);
  var a = document.getElementById("auth");
  if (a) a.classList.toggle("hidden", on);
}

function showLogin() {
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

// passwordLogin derives the login key in a worker (Argon2id + HKDF, the
// client's scheme) and exchanges it for the session cookie.
function passwordLogin(email, password, remember, onProgress) {
  return fetch("/v1/auth/prelogin", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: email }) })
    .then(function (r) { return r.json().then(function (j) { if (!r.ok) throw new Error(t(j.error || r.statusText)); return j; }); })
    .then(function (pre) {
      return new Promise(function (resolve, reject) {
        var w = new Worker("/app/kdf.js");
        w.onmessage = function (ev) {
          var d = ev.data;
          if (d.progress !== undefined) { onProgress(d.progress); return; }
          w.terminate();
          if (d.error) reject(new Error(d.error)); else resolve(d.authKey);
        };
        w.onerror = function (e) { w.terminate(); reject(new Error(e.message || "worker failed")); };
        w.postMessage({ password: password, salt: pre.salt, kdf: pre.kdf });
      });
    })
    .then(function (authKey) {
      return fetch("/v1/web/login", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: email, auth_key: authKey, remember: remember }) });
    })
    .then(function (r) {
      if (r.status === 204) return;
      return r.json().then(function (j) { throw new Error(t(j.error || r.statusText)); });
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
      nav.appendChild(h("a", { class: id === current ? "on" : "", href: "#" + id, onclick: function () { document.getElementById("side").classList.remove("open"); } },
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
