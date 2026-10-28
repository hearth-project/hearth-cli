"use strict";
// Overview and users.

view("overview", "Dashboard", function () {
  return api("GET", "/overview").then(function (o) {
    var s = o.stats;
    pendingCount = o.pending_users;
    drawTabs();
    var days = o.series.map(function (x) { return x.day.slice(5); });
    return [
      h("div", { class: "grid" },
        kpi(t("Accounts"), s.Users, "users"),
        kpi(t("Active (30 days)"), s.ActiveUsers30d, "bolt"),
        kpi(t("Waiting for approval"), o.pending_users, "check", o.pending_users ? "warn" : ""),
        kpi(t("Devices"), s.Devices, "machine"),
        kpi(t("Clients online"), o.online_devices, "machine", o.online_devices ? "ok" : ""),
        kpi(t("Conversations"), s.Sessions, "chat"),
        kpi(t("Expired"), s.ExpiredSessions, "log"),
        kpi(t("Session storage"), bytes(s.SessionBytes), "db"),
        kpi(t("Library storage"), bytes(s.LibraryBytes), "lib"),
        kpi(t("Teams"), s.Teams, "team"),
        kpi(t("Team packages"), s.Packages + " · " + bytes(s.PackageBytes), "lib")),
      h("div", { class: "two" },
        h("div", { class: "card" }, h("h3", {}, t("Active users per day")), chart(o.series.map(function (x) { return x.active_users; }), days)),
        h("div", { class: "card" }, h("h3", {}, t("Uploads per day")), chart(o.series.map(function (x) { return x.bytes; }), days, bytes))),
      h("div", { class: "two" },
        h("div", { class: "card" }, h("h3", {}, t("Top skills (30 days)")),
          bars(o.top_skills, function (x) { return x.count + " · " + x.users + " " + t("people"); }, skillTip),
          h("p", { class: "muted small" }, t("Hover a skill to see who uses it."))),
        h("div", { class: "card" }, h("h3", {}, t("Client versions (active devices)")),
          bars(o.client_versions, null, versionTip, versionName),
          h("p", { class: "muted small" }, t("Served build") + ": " + (o.served_version || "-") + " · " + t("Hover a version to see its machines.")))),
      h("div", { class: "two" },
        h("div", { class: "card" }, h("h3", {}, t("Top agents (30 days)")),
          bars(o.top_agents || [], function (x) { return x.count + " · " + x.users + " " + t("people"); }, skillTip),
          h("p", { class: "muted small" }, t("Subagents started by type (Codex spawn_agent, Claude Code Task); hover to see who."))),
        h("div", { class: "card" }, h("h3", {}, t("Shared in teams")),
          h("table", {}, h("tbody", {},
            h("tr", {}, h("td", {}, t("Skills")), h("td", { class: "num" }, o.team_skills)),
            h("tr", {}, h("td", {}, t("Agents")), h("td", { class: "num" }, o.team_agents)))),
          h("p", { class: "muted small" }, t("Each user's 5 most used skills and agents are shared with their teams automatically.")))),
      h("div", { class: "card" }, h("h3", {}, t("Active devices (30 days)") + " (" + o.devices.length + ")"), devicesTable(o.devices)),
      h("div", { class: "two" },
        h("div", { class: "card" }, h("h3", {}, t("Sign-ups per day")), chart(o.series.map(function (x) { return x.signups; }), days)),
        h("div", { class: "card" }, h("h3", {}, t("Server")),
          h("table", {}, h("tbody", {},
            h("tr", {}, h("td", {}, t("Registration")), h("td", {}, badge(o.settings.registration, "acc"))),
            h("tr", {}, h("td", {}, t("Default quota per account")), h("td", {}, o.settings.default_quota ? bytes(o.settings.default_quota) : t("unlimited"))),
            h("tr", {}, h("td", {}, t("Client auto-update")), h("td", {}, o.settings.client_auto_update ? "on" : "off")),
            h("tr", {}, h("td", {}, t("Storage")), h("td", { class: "mono" }, s.Storage)),
            h("tr", {}, h("td", {}, t("Server started")), h("td", {}, when(o.started_at)))))))
    ];
  });
}, "admin", "chart");

function versionName(x) { return x.name || t("not reported (older client)"); }
var OLD_CLIENT_HINT = "Clients installed before Oct 24 do not update by themselves: run hearth self-update once on that machine.";

function skillTip(x) {
  return [h("h4", {}, x.name + " · " + x.count),
    h("table", {}, h("tbody", {}, (x.by_user || []).map(function (u) {
      return h("tr", {}, h("td", {}, u.email), h("td", { class: "num" }, u.count));
    })))];
}

function versionTip(x) {
  return [h("h4", {}, versionName(x) + " · " + x.count), x.name ? null : h("p", { class: "muted" }, t(OLD_CLIENT_HINT)),
    h("table", {}, h("tbody", {}, (x.devices || []).map(function (d) {
      return h("tr", {}, h("td", {}, d.name), h("td", { class: "mono" }, d.ip || "-"), h("td", {}, d.email), h("td", { class: "muted" }, ago(d.last_seen)));
    })))];
}

function devicesTable(ds) {
  return h("table", {}, h("thead", {}, h("tr", {}, ["Machine", "IP", "User", "Version", "Last seen"].map(function (k) { return h("th", {}, t(k)); }))),
    h("tbody", {}, ds.map(function (d) {
      return h("tr", {}, h("td", {}, d.name, " ", d.online ? badge(t("online"), "ok") : null), h("td", { class: "mono" }, d.ip || "-"), h("td", {}, d.email),
        h("td", { class: "mono" }, d.version || h("span", { class: "muted" }, t("not reported (older client)"))), h("td", {}, ago(d.last_seen)));
    })));
}

// onlineList shows a user's online clients: machine and IP, version on hover.
function onlineList(list) {
  if (!list || !list.length) return h("span", { class: "muted" }, "-");
  return list.map(function (d) {
    return tip(h("div", { class: "dev" }, h("span", { class: "dot" }), d.name, " ", h("span", { class: "mono muted" }, d.ip || "")), function () {
      return h("table", {}, h("tbody", {},
        h("tr", {}, h("td", {}, t("Machine")), h("td", {}, d.name)),
        h("tr", {}, h("td", {}, t("IP")), h("td", { class: "mono" }, d.ip || "-")),
        h("tr", {}, h("td", {}, t("Version")), h("td", { class: "mono" }, d.version || t("not reported (older client)"))),
        h("tr", {}, h("td", {}, t("Last seen")), h("td", {}, ago(d.last_seen)))));
    });
  });
}

function userActions(u) {
  var e = encodeURIComponent(u.email), out = [];
  var setStatus = function (st) { return function () { act(api("POST", "/users/" + e + "/status", { status: st })); }; };
  if (u.status === "pending") {
    out.push(h("button", { class: "pri", onclick: setStatus("active") }, t("Approve")));
    out.push(h("button", { class: "bad", onclick: function () { if (confirm(t("Reject and delete this sign-up?"))) act(api("DELETE", "/users/" + e)); } }, t("Reject")));
    return out;
  }
  out.push(u.status === "active" ? h("button", { onclick: setStatus("disabled") }, t("Disable")) : h("button", { onclick: setStatus("active") }, t("Enable")));
  var more = [
    [t("Set quota"), function () {
      var v = prompt(t("Quota for") + " " + u.email + "\n" + t("Size like 10GiB or 500MB; 0 or empty = unlimited.") + " (default = -1)", u.quota_source === "user" ? (u.quota_bytes ? bytes(u.quota_bytes) : "0") : "default");
      if (v === null) return;
      var b = /^default$|^-1$/i.test(v.trim()) ? -1 : parseBytes(v);
      if (isNaN(b)) { toast("?", true); return; }
      act(api("POST", "/users/" + e + "/quota", { bytes: b }));
    }],
    [t(u.admin ? "Remove admin" : "Make admin"), function () { act(api("POST", "/users/" + e + "/admin", { admin: !u.admin })); }],
    [t("Log out devices"), function () { if (confirm(t("Are you sure?"))) act(api("POST", "/users/" + e + "/revoke-devices")); }],
    [t("Delete"), function () { if (confirm(t("Delete this account and all its data?") + "\n" + u.email)) act(api("DELETE", "/users/" + e)); }, "bad"]
  ];
  out.push(menuButton("⋯", more));
  return out;
}

function devicesRow(u, tr) {
  var next = tr.nextSibling;
  if (next && next.className === "sub") { next.remove(); return; }
  api("GET", "/users/" + encodeURIComponent(u.email) + "/devices").then(function (ds) {
    var tb = h("table", {}, h("thead", {}, h("tr", {}, ["Name", "IP", "Status", "Version", "Last seen", "Created"].map(function (k) { return h("th", {}, t(k)); }))),
      h("tbody", {}, ds.map(function (d) {
        return h("tr", {}, h("td", {}, d.name, " ", h("span", { class: "muted mono" }, d.id)), h("td", { class: "mono" }, d.ip || "-"),
          h("td", {}, d.online ? badge(t("online"), "ok") : d.status),
          h("td", { class: "mono" }, d.version || t("not reported (older client)")),
          h("td", {}, ago(d.last_seen)), h("td", {}, when(d.created_at)));
      })));
    tr.parentNode.insertBefore(h("tr", { class: "sub" }, h("td", { colspan: 9 }, tb)), tr.nextSibling);
  });
}

function usersTable(list) {
  return h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, t("Email")), h("th", {}, t("Status")), h("th", { class: "num" }, t("Used")), h("th", { class: "num" }, t("Quota")),
      h("th", {}, t("Clients online")), h("th", { class: "num" }, t("Devices")), h("th", { class: "num" }, t("Conversations")), h("th", {}, t("Last seen")), h("th", {}, t("Actions")))),
    h("tbody", {}, list.map(function (u) {
      var tr = h("tr", {},
        h("td", {}, h("a", { href: "#users", title: t("Devices"), onclick: function (ev) { ev.preventDefault(); devicesRow(u, tr); } }, u.email), " ", u.admin ? badge(t("Admin"), "acc") : null,
          h("div", { class: "muted small" }, t("Created") + " " + when(u.created_at))),
        h("td", {}, statusBadge(u.status)),
        h("td", { class: "num" }, bytes(u.used_bytes)),
        h("td", { class: "num" }, u.quota_bytes ? bytes(u.quota_bytes) : t("unlimited"), u.quota_source === "default" ? h("div", { class: "muted small" }, t("default")) : null),
        h("td", { class: "online" }, onlineList(u.online)),
        h("td", { class: "num" }, u.devices),
        h("td", { class: "num" }, u.sessions),
        h("td", { class: "num" }, ago(u.last_seen)),
        h("td", { class: "acts" }, userActions(u)));
      return tr;
    })));
}

view("users", "Users", function () {
  return api("GET", "/users").then(function (us) {
    var q = h("input", { placeholder: t("Search"), type: "search" });
    var box = h("div", { class: "tablewrap" }, usersTable(us));
    q.addEventListener("input", function () {
      var v = q.value.toLowerCase();
      box.textContent = "";
      box.appendChild(usersTable(us.filter(function (u) { return u.email.indexOf(v) >= 0; })));
    });
    return h("div", { class: "card" }, h("div", { class: "row" }, h("h2", {}, t("Users") + " (" + us.length + ")"), q,
      h("span", { class: "muted small" }, t("Click an email to see its devices."))), box);
  });
}, "admin", "users");
