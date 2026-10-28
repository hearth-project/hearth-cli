"use strict";
// The user's teams: members, what each team shares, invite codes.

// rankList shows a team's skills or agents, hottest first: rank, name,
// version, description, a heat bar and the uses / people count.
function rankList(items, kind, team) {
  if (!items.length) return empty(t(kind === "skill" ? "No skills shared yet." : "No agents shared yet."));
  var max = Math.max.apply(null, items.map(function (p) { return p.uses; }).concat([1]));
  return h("ol", { class: "rank" }, items.map(function (p, i) {
    var row = h("li", { class: team ? "clickable" : "" },
      h("span", { class: "no" + (i < 3 && p.uses ? " top" + (i + 1) : "") }, i + 1),
      h("div", { class: "rk" },
        h("div", { class: "rt" }, h("span", { class: "nm" }, p.name), h("span", { class: "ver" }, "v" + p.version)),
        h("div", { class: "ds" }, p.description || "—"),
        h("div", { class: "heat" }, h("span", { class: "t" }, h("i", { style: "width:" + (100 * p.uses / max).toFixed(1) + "%" })),
          h("span", { class: "c" }, p.uses ? p.uses + " " + t("uses") + " · " + p.users + " " + t("people") : t("not used yet")))));
    if (team) row.addEventListener("click", function () { showTeamItem(team, p); });
    return tip(row, function () {
      return [h("h4", {}, p.name + " v" + p.version), h("p", {}, p.description || "—"),
        h("p", { class: "muted" }, t("Published by") + " " + p.published_by + " · " + ago(p.published_at)), team ? h("p", { class: "muted" }, t("Click for details")) : null];
    });
  }));
}

function roleName(r) { return t({ owner: "role-owner", maintainer: "role-maintainer", member: "role-member", reader: "role-reader" }[r] || r); }

function teamBlock(tm) {
  var pk = tm.packages;
  var skills = pk.filter(function (p) { return p.type === "skill"; }), agents = pk.filter(function (p) { return p.type === "agent"; });
  var roleBadge = badge(roleName(tm.role), tm.role === "owner" ? "acc" : tm.role === "reader" ? "" : "ok");
  var actions = [];
  var inviteOut = h("div", {});
  if (tm.role === "maintainer" || tm.role === "owner") {
    actions.push(h("button", { class: "sm", onclick: function () {
      me("POST", "/teams/" + encodeURIComponent(tm.name) + "/invite").then(function (r) {
        inviteOut.textContent = "";
        add(inviteOut, h("div", { class: "invite" }, h("span", { class: "small muted" }, t("Invite code (valid 7 days); colleagues run:")), h("span", { class: "code" }, "hearth team join " + r.code)));
      }, function (e) { toast(e.message, true); });
    } }, t("Invite")));
  }
  if (tm.role !== "reader") {
    actions.push(h("button", { class: "sm bad", onclick: function () {
      if (confirm(t("Leave this team?") + "\n" + tm.name)) act(me("DELETE", "/teams/" + encodeURIComponent(tm.name)));
    } }, t("Leave")));
  }
  var share = [];
  if ((tm.shared_with || []).length) share.push(t("Shared with") + ": " + tm.shared_with.join(", "));
  if ((tm.shared_from || []).length) share.push(t("Shared from") + ": " + tm.shared_from.join(", "));
  return h("div", { class: "card team" },
    h("div", { class: "th" }, h("div", { class: "tic" }, icon("team")),
      h("div", { class: "tt" }, h("div", {}, h("h3", {}, tm.name), " ", roleBadge),
        h("div", { class: "muted small" }, tm.members.length + " " + t("members") + " · " + skills.length + " " + t("skills") + " · " + agents.length + " " + t("agents") +
          (tm.via ? " · " + t("via") + " " + tm.via : "") + (share.length ? " · " + share.join(" · ") : ""))),
      h("div", { class: "acts" }, actions)),
    inviteOut,
    tm.members.length ? h("div", { class: "members" }, tm.members.map(function (m) {
      return h("span", { class: "chip", title: m.email + " · " + roleName(m.role) }, avatar(m.email), h("span", {}, m.email.split("@")[0]),
        m.role !== "member" ? h("span", { class: "role" }, roleName(m.role)) : null);
    })) : null,
    h("div", { class: "cols" },
      h("div", {}, h("div", { class: "colh" }, icon("lib"), t("Skills"), h("span", { class: "muted" }, skills.length)), rankList(skills, "skill", tm.name)),
      h("div", {}, h("div", { class: "colh" }, icon("agent"), t("Agents"), h("span", { class: "muted" }, agents.length)), rankList(agents, "agent", tm.name))));
}

view("myteams", "My teams", function () {
  return me("GET", "/teams").then(function (ts) {
    if (!ts.length) {
      return card(t("My teams"), [empty(t("You are not in a team yet."), "👥"),
        h("p", { class: "muted small" }, t("Create one: hearth team create <name> · join one: hearth team join <invite code>"))]);
    }
    return [h("p", { class: "muted small", style: "margin:0 0 16px" }, t("Ranked by how much the team's members used each item in the last 30 days. Your 5 most used skills and agents are shared with teams where you can publish.")),
      ts.map(teamBlock)];
  });
}, "me", "team");

// The user's own settings: resident skills, automatic sharing, links, and
// changes waiting for their machines.
view("mysettings", "My settings", function () {
  return Promise.all([me("GET", "/settings"), me("GET", "/library"), me("GET", "/intents")]).then(function (r) {
    var st = r[0], lib = r[1], pend = r[2];
    var skills = lib.items.filter(function (x) { return x.type === "skill"; });
    var res = st.resident || [], all = !res.length;
    var boxes = skills.map(function (x) {
      var cb = h("input", { type: "checkbox", checked: all || res.indexOf(x.name) >= 0 });
      return { name: x.name, cb: cb, el: h("label", { class: "check" }, cb, h("span", { class: "nm" }, x.name), x.uses ? h("span", { class: "uses" }, icon("bolt"), x.uses) : null) };
    });
    var top = h("input", { type: "number", min: 0, max: 50, value: st.share_top, style: "width:90px" });
    var parts = [];
    if (pend.length) {
      parts.push(card(t("Waiting for your machines") + " (" + pend.length + ")", [h("p", { class: "muted small" }, t("A machine with hearth running applies these within about a minute, then all your machines get them.")),
        pend.map(function (it) {
          return h("div", { class: "item" }, h("div", { style: "flex:1" }, h("div", { class: "nm" }, it.kind), h("div", { class: "ds mono" }, JSON.stringify(it.data))),
            h("button", { class: "sm", onclick: function () { act(me("DELETE", "/intents/" + it.id)); } }, t("Cancel")));
        })]));
    }
    parts.push(card(t("Resident skills"), [h("p", { class: "muted small" }, t("Resident skills are loaded into every agent; the others are found on demand by skill search (fewer tokens, fewer wrong picks).")),
      h("div", { class: "row" }, h("button", { class: "sm", onclick: function () { boxes.forEach(function (b) { b.cb.checked = true; }); } }, t("All")),
        h("button", { class: "sm", onclick: function () { boxes.forEach(function (b) { b.cb.checked = false; }); } }, t("None")),
        h("button", { class: "sm", onclick: function () { boxes.forEach(function (b, i) { b.cb.checked = i < 5; }); } }, t("Top 5 by use"))),
      h("div", { class: "checks" }, boxes.map(function (b) { return b.el; })),
      h("div", { class: "row", style: "margin-top:14px" }, h("button", { class: "pri", onclick: function () {
        var names = boxes.filter(function (b) { return b.cb.checked; }).map(function (b) { return b.name; });
        if (names.length === boxes.length) names = []; // all
        queueChange("resident", { names: names }).then(function () { render(); });
      } }, t("Save")))]));
    parts.push(card(t("Automatic sharing with teams"), [h("p", { class: "muted small" }, t("Your most used skills and agents (this many of each) are shared with teams where you can publish; 0 = off.")),
      h("div", { class: "row" }, top, h("button", { class: "pri", onclick: function () {
        var n = parseInt(top.value, 10);
        if (isNaN(n) || n < 0) { toast("?", true); return; }
        queueChange("share-top", { n: n }).then(function () { render(); });
      } }, t("Save")))]));
    var links = st.links || [];
    parts.push(card(t("Kept from teams") + " (" + links.length + ")", links.length ? links.map(function (l) {
      return h("div", { class: "item" }, h("div", { class: "ic " + l.type }, icon(l.type === "agent" ? "agent" : "lib")),
        h("div", { style: "flex:1" }, h("div", { class: "nm" }, l.name, " ", badge("v" + l.version), l.auto ? badge(t("team top"), "acc") : null), h("div", { class: "ds" }, t("from") + " " + l.source)),
        h("button", { class: "sm", onclick: function () { queueChange("unlink", { type: l.type, name: l.name }).then(function () { render(); }); } }, t("Stop keeping it")));
    }).concat([h("p", { class: "muted small" }, t("Each team's 20 most used skills and agents are kept automatically; the others are found by skill search."))])
      : empty(t("Open a team's skill or agent and choose “Keep in all my agents”."))));
    return parts;
  });
}, "me", "gear");
