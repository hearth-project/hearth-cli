"use strict";
// The signed-in user's own pages.

function deviceRows(ds, mine) {
  if (!ds.length) return empty(t("No machines yet. Install Hearth on a machine and run hearth setup."), "💻");
  var cols = ["Machine", "IP", "Status", "Environment", "Version", "Conversations", "Last seen", "Created", ""];
  var body = h("tbody", {});
  ds.forEach(function (d) {
    var st = d.status !== "active" ? badge(t(d.status), "bad") : d.online ? h("span", { class: "dev" }, h("span", { class: "dot" }), t("online")) : h("span", { class: "dev muted" }, h("span", { class: "dot off" }), t("offline"));
    var detail = null;
    var toggle = function () {
      if (detail) { detail.remove(); detail = null; return; }
      detail = h("tr", { class: "sub" }, h("td", { colspan: cols.length }, envDetail(d)));
      row.after(detail);
    };
    var row = h("tr", {}, h("td", {}, h("b", {}, d.name), h("div", { class: "muted mono" }, d.id), d.facts ? h("div", { class: "muted facts" }, factsLine(d.facts)) : null),
      h("td", { class: "mono" }, d.ip || "-"), h("td", {}, st),
      h("td", {}, d.status === "active" ? envPill(d, toggle) : h("span", { class: "muted" }, "-")),
      h("td", { class: "mono" }, d.version || (d.status === "active" && d.online ? h("span", { class: "badge warn", title: t(OLD_CLIENT_HINT) }, t("old version: run hearth self-update there")) : h("span", { class: "muted" }, "-"))),
      h("td", {}, d.sessions ? h("span", {}, d.sessions, h("div", { class: "muted small" }, t("newest") + " " + ago(d.last_active))) : h("span", { class: "muted" }, "0")),
      h("td", {}, ago(d.last_seen)), h("td", {}, when(d.created_at)),
      h("td", {}, mine && d.status === "active" ? h("button", { class: "sm bad", onclick: function () {
        if (confirm(t("Log this machine out? It must run hearth setup again.") + "\n" + d.name)) act(me("DELETE", "/devices/" + encodeURIComponent(d.id)));
      } }, t("Log out")) : null));
    add(body, row);
  });
  return wrap(h("table", {}, h("thead", {}, h("tr", {}, cols.map(function (k) { return h("th", {}, t(k)); }))), body));
}

view("home", "Overview", function () {
  return Promise.all([me("GET", ""), me("GET", "/devices"), me("GET", "/library"), me("GET", "/teams")]).then(function (r) {
    var o = r[0], ds = r[1], lib = r[2], teams = r[3], a = o.account;
    var online = ds.filter(function (d) { return d.online; });
    var skills = lib.items.filter(function (x) { return x.type === "skill"; }), agents = lib.items.filter(function (x) { return x.type === "agent"; });
    var days = o.series.map(function (x) { return x.day.slice(5); });
    var notice = o.pending_changes ? h("div", { class: "notice" }, icon("bolt"), t("Changes waiting for your machines") + ": " + o.pending_changes + " · " + t("a machine with hearth running applies them within about a minute")) : null;
    var addMachine = !ds.length && o.install ? card(t("Add a machine"), [h("p", { class: "muted small" }, t("Run on the machine, in a terminal:")), cmdLine(o.install)]) : null;
    return [notice, addMachine,
      h("div", { class: "grid" },
        kpi(t("Machines online"), online.length + " / " + ds.filter(function (d) { return d.status === "active"; }).length, "machine", online.length ? "ok" : ""),
        kpi(t("Conversations"), a.sessions, "chat"),
        kpi(t("Skills"), skills.length, "lib"),
        kpi(t("Agents"), agents.length, "agent"),
        kpi(t("Teams"), teams.length, "team"),
        kpi(t("Storage used"), bytes(o.used_bytes) + (a.quota_bytes ? " / " + bytes(a.quota_bytes) : ""), "db")),
      h("div", { class: "two" },
        card(t("Uploads per day"), chart(o.series.map(function (x) { return x.bytes; }), days, bytes)),
        card(t("Machines online"), online.length ? online.map(function (d) {
          return h("div", { class: "item" }, h("div", { class: "ic" }, icon("machine")), h("div", {},
            h("div", { class: "nm" }, d.name), h("div", { class: "ds" }, (d.ip || "-") + " · " + (d.version || t("not reported (older client)")) + " · " + ago(d.last_seen))));
        }) : empty(t("No machine is online right now."), "💤"), h("a", { href: "#machines" }, t("All machines")))),
      h("div", { class: "two" },
        card(t("My top skills (30 days)"), bars(o.top_skills)),
        card(t("My top agents (30 days)"), bars(o.top_agents))),
      card(t("Team updates (7 days)"), o.feed.length ? h("div", { class: "feed" }, o.feed.map(function (f) {
        var row = h("div", { class: "item clickable" }, h("div", { class: "ic " + f.type }, icon(f.type === "agent" ? "agent" : "lib")),
          h("div", { style: "min-width:0;flex:1" }, h("div", { class: "nm" }, f.name, " ", badge("v" + f.version), " ", h("span", { class: "muted small" }, t("in") + " " + f.team)),
            h("div", { class: "ds" }, (f.version > 1 ? t("updated by") : t("shared by")) + " " + f.published_by + " · " + ago(f.published_at) + (f.description ? " · " + f.description : ""))));
        row.addEventListener("click", function () { showTeamItem(f.team, f); });
        return row;
      })) : empty(t("Nothing new in your teams this week."))),
      h("p", { class: "muted small" }, t("Conversations and credentials are end-to-end encrypted: this page cannot read their titles or contents."))
    ];
  });
}, "me", "home");

view("machines", "Machines", function () {
  return Promise.all([me("GET", "/devices"), me("GET", "")]).then(function (r) {
    var ds = r[0], o = r[1];
    return [card(t("Machines") + " (" + ds.length + ")", deviceRows(ds, true),
      h("span", { class: "muted small" }, t("Online = background sync reported in the last 15 minutes."))),
      o.install ? card(t("Add a machine"), [h("p", { class: "muted small" }, t("Run on the new machine, in a terminal. It installs Hearth, signs in and restores your setup:")), cmdLine(o.install),
        h("p", { class: "muted small" }, t("Then, on any other machine with Hearth, tell its agent the sentence the installer prints; it sets up the new machine's software over hearth ssh:")),
        cmdLine(t("restore the Codex environment of <machine>"))]) : null];
  });
}, "me", "machine");

// sharedIn maps "type/name" to the teams that have it (and its version).
function sharedIn(teams) {
  var m = {};
  teams.forEach(function (tm) {
    tm.packages.forEach(function (p) { (m[p.type + "/" + p.name] = m[p.type + "/" + p.name] || []).push({ team: tm.name, version: p.version, by: p.published_by }); });
  });
  return m;
}

function libItem(x, shared, publishable, settings, allSkills) {
  var where = shared[x.type + "/" + x.name] || [];
  var shareBtn = publishable.length ? h("button", { class: "sm", onclick: function () { shareDialog(x, publishable, where); } }, icon("team"), " ", t(where.length ? "Share again" : "Share")) : null;
  var el = h("div", { class: "item clickable", title: t("Click for details") }, h("div", { class: "ic " + x.type }, x.type === "agent" ? icon("agent") : icon("lib")),
    h("div", { style: "min-width:0;flex:1" }, h("div", { class: "nm" }, x.name, " ", x.type === "skill" ? (x.resident ? badge(t("resident"), "acc") : badge(t("on demand"))) : null,
        x.uses ? h("span", { class: "uses", title: t("Uses in the last 30 days") }, icon("bolt"), x.uses) : null),
      h("div", { class: "ds", title: x.description }, x.description || "—"),
      where.length ? h("div", { class: "sharedin" }, where.map(function (w) { return badge(w.team + " v" + w.version, "ok"); })) : null),
    shareBtn);
  el.addEventListener("click", function (ev) { if (!ev.target.closest("button")) showMyItem(x, settings, allSkills); });
  return el;
}

// shareDialog lets the user pick teams and publishes the item to them.
function shareDialog(x, publishable, where) {
  var has = {};
  where.forEach(function (w) { has[w.team] = true; });
  var boxes = publishable.map(function (tm) {
    var cb = h("input", { type: "checkbox", checked: !has[tm] });
    return { team: tm, cb: cb, row: h("label", { class: "check" }, cb, h("span", {}, tm), has[tm] ? badge(t("already shared"), "ok") : null) };
  });
  var out = h("div", {});
  var go = h("button", { class: "pri" }, t("Share"));
  var close = function () { ov.remove(); };
  var ov = h("div", { class: "overlay", onclick: function (ev) { if (ev.target === ov) close(); } }, h("div", { class: "dialog" },
    h("h3", {}, t("Share with teams"), ": ", x.name),
    h("p", { class: "muted small" }, t("Team members can use it at once; changes are published again when you share again (or automatically for your most used items).")),
    boxes.map(function (b) { return b.row; }), out,
    h("div", { class: "row", style: "justify-content:flex-end;margin:16px 0 0" }, h("button", { onclick: close }, t("Close")), go)));
  go.addEventListener("click", function () {
    var teams = boxes.filter(function (b) { return b.cb.checked; }).map(function (b) { return b.team; });
    if (!teams.length) { toast(t("Pick at least one team"), true); return; }
    go.disabled = true;
    me("POST", "/share", { type: x.type, name: x.name, teams: teams }).then(function (res) {
      out.textContent = "";
      res.forEach(function (r) {
        add(out, h("div", { class: "small", style: "margin-top:6px" }, r.error ? badge(r.team, "bad") : badge(r.team, "ok"), " ",
          r.error ? r.error : (r.created ? t("shared as") + " v" + r.version : t("unchanged") + " (v" + r.version + ")")));
      });
      go.disabled = false;
      if (res.every(function (r) { return !r.error; })) { toast(t("Done")); setTimeout(function () { close(); render(); }, 900); }
    }, function (e) { go.disabled = false; out.textContent = ""; add(out, h("p", { class: "err" }, e.message)); });
  });
  document.body.appendChild(ov);
}

view("library", "Skills & agents", function () {
  return Promise.all([me("GET", "/library"), me("GET", "/teams"), me("GET", "/settings")]).then(function (r) {
    var lib = r[0], teams = r[1], settings = r[2], kind = "skill", q = "";
    var allSkills = lib.items.filter(function (x) { return x.type === "skill"; }).map(function (x) { return x.name; });
    var shared = sharedIn(teams);
    var publishable = teams.filter(function (tm) { return tm.role === "maintainer" || tm.role === "owner"; }).map(function (tm) { return tm.name; });
    var box = h("div", {});
    var count = function (k) { return lib.items.filter(function (x) { return x.type === k; }).length; };
    var segBtns = {};
    function draw() {
      box.textContent = "";
      var list = lib.items.filter(function (x) { return x.type === kind && (!q || (x.name + " " + x.description).toLowerCase().indexOf(q) >= 0); });
      add(box, list.length ? h("div", { class: "items" }, list.map(function (x) { return libItem(x, shared, publishable, settings, allSkills); })) : empty(t("Nothing here yet.")));
      Object.keys(segBtns).forEach(function (k) { segBtns[k].className = k === kind ? "on" : ""; });
    }
    var seg = h("div", { class: "seg" }, ["skill", "agent"].map(function (k) {
      return segBtns[k] = h("button", { onclick: function () { kind = k; draw(); } }, t(k === "skill" ? "Skills" : "Agents") + " " + count(k));
    }));
    var search = h("input", { type: "search", placeholder: t("Search") });
    search.addEventListener("input", function () { q = search.value.toLowerCase(); draw(); });
    draw();
    return h("div", { class: "card" }, h("div", { class: "row" }, seg, search, h("span", { class: "spacer" }),
      h("span", { class: "muted small" }, t("Library version") + " v" + lib.version)), box,
      h("p", { class: "muted small" }, t("Most used first (all your machines, last 30 days). Resident skills are loaded into every agent; the rest are found on demand by skill search. Change: hearth config --edit")),
      publishable.length ? null : h("p", { class: "muted small" }, t("To share, be maintainer or owner of a team (My teams).")));
  });
}, "me", "lib");
