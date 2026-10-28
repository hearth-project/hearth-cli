"use strict";
// Approvals, invites, teams.

view("approvals", "Approvals", function () {
  return Promise.all([api("GET", "/users"), api("GET", "/settings")]).then(function (r) {
    var pend = r[0].filter(function (u) { return u.status === "pending"; });
    pendingCount = pend.length;
    drawTabs();
    var body = [h("h2", {}, t("Approvals"))];
    if (r[1].registration !== "approval") body.push(h("p", { class: "muted" }, t("Approval mode is off: new accounts work at once. Turn it on in Settings (registration = approval).")));
    if (!pend.length) body.push(h("p", {}, t("No accounts waiting for approval.")));
    else {
      body.push(h("div", { class: "row" }, h("button", {
        class: "pri", onclick: function () {
          act(Promise.all(pend.map(function (u) { return api("POST", "/users/" + encodeURIComponent(u.email) + "/status", { status: "active" }); })));
        }
      }, t("Approve") + " (" + pend.length + ")")));
      body.push(h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, t("Email")), h("th", {}, t("Created")), h("th", {}, t("Actions")))),
        h("tbody", {}, pend.map(function (u) { return h("tr", {}, h("td", {}, u.email), h("td", {}, when(u.created_at)), h("td", { class: "acts" }, userActions(u))); }))));
    }
    return h("div", { class: "card" }, body);
  });
}, "admin", "check");

view("invites", "Invites", function () {
  return api("GET", "/invites").then(function (list) {
    var days = h("input", { type: "number", min: 1, max: 365, value: 7, style: "width:80px" });
    var out = h("div", {});
    var create = h("button", {
      class: "pri", onclick: function () {
        api("POST", "/invites", { days: parseInt(days.value, 10) || 7 }).then(function (r) {
          out.textContent = "";
          out.appendChild(h("p", {}, t("Invite code (shown once):")));
          out.appendChild(h("p", { class: "code mono" }, r.code));
          out.appendChild(h("p", { class: "muted small" }, "hearth setup --invite " + r.code));
          api("GET", "/invites").then(function (l) { tbl.replaceWith(tbl = table(l)); });
        }, function (e) { toast(e.message, true); });
      }
    }, t("Create"));
    function table(l) {
      return h("table", {}, h("thead", {}, h("tr", {}, ["ID", "Created", "Expires", "Used by", "Actions"].map(function (k) { return h("th", {}, t(k)); }))),
        h("tbody", {}, l.map(function (i) {
          var expired = Date.parse(i.expires_at) < Date.now();
          return h("tr", {}, h("td", { class: "mono" }, i.id), h("td", {}, when(i.created_at)),
            h("td", {}, when(i.expires_at), expired ? " " : null, expired ? badge(t("Expired"), "bad") : null),
            h("td", {}, i.used_by || h("span", { class: "muted" }, t("unused"))),
            h("td", {}, i.used_by ? null : h("button", { class: "bad", onclick: function () { act(api("DELETE", "/invites/" + i.id)); } }, t("Revoke"))));
        })));
    }
    var tbl = table(list);
    return [h("div", { class: "card" }, h("h2", {}, t("New invite")), h("div", { class: "row" }, t("Valid for days"), days, create), out),
      h("div", { class: "card" }, h("h2", {}, t("Invites")), tbl)];
  });
}, "admin", "mail");

var ROLES = ["member", "maintainer", "owner"];
function roleSelect(cur, onchange) {
  var sel = h("select", {}, ROLES.map(function (r) { return h("option", { value: r, selected: r === cur }, t(r)); }));
  if (onchange) sel.addEventListener("change", function () { onchange(sel.value); });
  return sel;
}
// userDatalist offers the registered emails in email inputs.
function userDatalist(users) {
  return h("datalist", { id: "user-emails" }, users.map(function (u) { return h("option", { value: u.email }); }));
}

function teamDetail(name, tr, refresh) {
  var next = tr.nextSibling;
  if (next && next.className === "sub") {
    next.remove();
    if (!refresh) return;
  }
  var path = "/teams/" + encodeURIComponent(name);
  var reload = function () { teamDetail(name, tr, true); };
  var change = function (p) { p.then(function () { toast(t("Done")); reload(); }, function (e) { toast(e.message, true); }); };
  api("GET", path).then(function (d) {
    var email = h("input", { type: "email", list: "user-emails", placeholder: "user@example.com" });
    var role = roleSelect("maintainer");
    var members = h("div", {}, h("h3", {}, t("Members") + " (" + d.members.length + ")"),
      h("table", {}, h("tbody", {}, d.members.map(function (m) {
        var me = encodeURIComponent(m.email);
        return h("tr", {}, h("td", {}, m.email),
          h("td", {}, roleSelect(m.role, function (r) { change(api("PUT", path + "/members/" + me, { role: r })); })),
          h("td", {}, h("button", { class: "bad", onclick: function () { if (confirm(t("Remove from team?") + "\n" + m.email)) change(api("DELETE", path + "/members/" + me)); } }, t("Remove"))));
      }))),
      h("div", { class: "row", style: "margin-top:10px" }, email, role,
        h("button", { class: "pri", onclick: function () {
          var e = email.value.trim();
          if (!e) { email.focus(); return; }
          change(api("PUT", path + "/members/" + encodeURIComponent(e), { role: role.value }));
        } }, t("Add member"))));
    var sk = d.packages.filter(function (p) { return p.type === "skill"; }), ag = d.packages.filter(function (p) { return p.type === "agent"; });
    var box = h("div", {}, members,
      h("div", { class: "cols", style: "margin-top:16px" },
        h("div", {}, h("div", { class: "colh" }, icon("lib"), t("Skills"), h("span", { class: "muted" }, sk.length)), rankList(sk, "skill")),
        h("div", {}, h("div", { class: "colh" }, icon("agent"), t("Agents"), h("span", { class: "muted" }, ag.length)), rankList(ag, "agent"))),
      h("p", { class: "muted small" }, t("Shared with") + ": " + ((d.shared_with || []).join(", ") || "-") + " · " + t("Shared from") + ": " + ((d.shared_from || []).join(", ") || "-")));
    tr.parentNode.insertBefore(h("tr", { class: "sub" }, h("td", { colspan: 8 }, box)), tr.nextSibling);
  }, function (e) { toast(e.message, true); });
}

view("teams", "Team management", function () {
  return Promise.all([api("GET", "/teams"), api("GET", "/users")]).then(function (r) {
    var ts = r[0], users = r[1];
    var name = h("input", { placeholder: t("Team name"), title: t("Lowercase letters, digits, . _ -"), pattern: "[a-z0-9][a-z0-9._-]*" });
    var owner = h("input", { type: "email", list: "user-emails", value: ME, placeholder: t("Owner") });
    var create = h("div", { class: "card" }, h("h2", {}, t("New team")), userDatalist(users),
      h("div", { class: "row" }, name, owner, h("button", { class: "pri", onclick: function () {
        var n = name.value.trim();
        if (!n) { name.focus(); return; }
        act(api("POST", "/teams", { name: n, owner: owner.value.trim() }));
      } }, t("Create"))),
      h("p", { class: "muted small" }, t("Lowercase letters, digits, . _ -")));
    var list = h("div", { class: "card" }, h("h2", {}, t("Teams") + " (" + ts.length + ")"),
      h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, t("Name")), h("th", {}, t("Owners")), h("th", { class: "num" }, t("Members")),
        h("th", { class: "num" }, t("Skills")), h("th", { class: "num" }, t("Agents")), h("th", { class: "num" }, t("Storage")), h("th", {}, t("Shared with")), h("th", {}, t("Actions")))),
        h("tbody", {}, ts.map(function (tm) {
          var tr = h("tr", {},
            h("td", {}, h("a", { href: "#teams", onclick: function (ev) { ev.preventDefault(); teamDetail(tm.name, tr); } }, tm.name), h("div", { class: "muted small" }, when(tm.created_at))),
            h("td", { class: "small" }, tm.owners.join(", ")), h("td", { class: "num" }, tm.members), h("td", { class: "num" }, tm.skills),
            h("td", { class: "num" }, tm.agents), h("td", { class: "num" }, bytes(tm.bytes)), h("td", { class: "small" }, (tm.shared_with || []).join(", ") || "-"),
            h("td", {}, h("button", { onclick: function () { teamDetail(tm.name, tr); } }, t("Members")),
              h("button", { class: "bad", onclick: function () { if (confirm(t("Delete this team and everything it shares?") + "\n" + tm.name)) act(api("DELETE", "/teams/" + encodeURIComponent(tm.name))); } }, t("Delete team"))));
          return tr;
        }))),
      h("p", { class: "muted small" }, t("Click a team to manage its members.")));
    return [create, list];
  });
}, "admin", "team");
