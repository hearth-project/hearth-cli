"use strict";
// Settings, maintenance, audit log; then start.

view("settings", "Settings", function () {
  return api("GET", "/settings").then(function (st) {
    var reg = h("select", {}, ["invite", "open", "approval", "closed"].map(function (m) {
      var label = { invite: "invite: an invite code is needed", open: "open: anyone who can reach the server", approval: "approval: anyone may sign up, an admin approves", closed: "closed: no new accounts" }[m];
      return h("option", { value: m, selected: st.registration === m }, t(label));
    }));
    var quota = h("input", { value: st.default_quota ? bytes(st.default_quota) : "", placeholder: t("unlimited") });
    var auto = h("input", { type: "checkbox", checked: st.client_auto_update });
    function field(label, input, help) { return h("div", { class: "field" }, h("div", {}, h("div", {}, label), h("div", { class: "h" }, help || "")), h("div", {}, input)); }
    return h("div", { class: "card" }, h("h2", {}, t("Settings")),
      field(t("Registration"), reg),
      field(t("Default quota per account"), quota, t("Size like 10GiB or 500MB; 0 or empty = unlimited.")),
      field(t("Client auto-update"), auto, t("Daemons on client machines install new builds from this server.")),
      h("div", { class: "row", style: "margin-top:16px" }, h("button", {
        class: "pri", onclick: function () {
          var q = parseBytes(quota.value);
          if (isNaN(q)) { toast("?", true); return; }
          act(api("PUT", "/settings", { registration: reg.value, default_quota: q, client_auto_update: auto.checked }), t("Saved"));
        }
      }, t("Save"))));
  });
}, "admin", "gear");

view("maintenance", "Maintenance", function () {
  var dry = h("input", { type: "checkbox", checked: true });
  var out = h("div", {});
  return h("div", { class: "card" }, h("h2", {}, t("Run expiry and clean storage")),
    h("div", { class: "row" }, h("label", {}, dry, " ", t("Dry run (only count orphaned blobs)")),
      h("button", {
        class: "pri", onclick: function () {
          api("POST", "/gc", { dry_run: dry.checked }).then(function (r) {
            out.textContent = "";
            out.appendChild(h("table", {}, h("tbody", {},
              h("tr", {}, h("td", {}, t("Expired then deleted")), h("td", { class: "num" }, r.expired + " / " + r.deleted)),
              h("tr", {}, h("td", {}, t("Orphaned blobs")), h("td", { class: "num" }, r.orphans + " (" + bytes(r.orphan_bytes) + "), " + r.orphans_deleted + " deleted")))));
          }, function (e) { toast(e.message, true); });
        }
      }, t("Run"))), out);
}, "admin", "tool");

view("audit", "Audit log", function () {
  return api("GET", "/audit?limit=300").then(function (rows) {
    return h("div", { class: "card" }, h("h2", {}, t("Audit log")),
      h("table", {}, h("thead", {}, h("tr", {}, ["Time", "Who", "Action", "Target", "Detail"].map(function (k) { return h("th", {}, t(k)); }))),
        h("tbody", {}, rows.map(function (a) {
          return h("tr", {}, h("td", {}, when(a.at)), h("td", {}, a.actor), h("td", {}, badge(a.action)), h("td", {}, a.target), h("td", { class: "small mono" }, a.detail));
        }))));
  });
}, "admin", "log");

(function start() {
  document.getElementById("logo").appendChild(icon("flame"));
  var lang = document.getElementById("lang");
  lang.textContent = LANG === "zh" ? "English" : "中文";
  lang.addEventListener("click", function () { localStorage.setItem("hearth-lang", LANG === "zh" ? "en" : "zh"); location.reload(); });
  var out = document.getElementById("logout");
  out.textContent = t("Sign out");
  out.addEventListener("click", function () {
    fetch("/v1/web/logout", { method: "POST", credentials: "same-origin", headers: { "X-Hearth-Admin": "1" } }).then(showLogin);
  });
  document.getElementById("menu").addEventListener("click", function () { document.getElementById("side").classList.toggle("open"); });
  window.addEventListener("hashchange", function () { current = location.hash.slice(1) || "home"; render(); });
  me("GET", "").then(function (m) {
    ME = m.email; IS_ADMIN = m.admin;
    document.getElementById("me").textContent = m.email;
    var av = document.getElementById("avatar"); av.textContent = (m.email || "?").slice(0, 1).toUpperCase();
    showShell(true);
    render();
    if (IS_ADMIN) api("GET", "/overview").then(function (o) { pendingCount = o.pending_users; drawTabs(); }, function () {});
  }, function () {});
})();
