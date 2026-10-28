"use strict";
// Shared console widgets: dialogs, copy buttons, item details.

function copyBtn(text) {
  var b = h("button", { class: "sm", title: t("Copy") }, t("Copy"));
  b.addEventListener("click", function () {
    var done = function () { b.textContent = t("Copied"); setTimeout(function () { b.textContent = t("Copy"); }, 1500); };
    if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(text).then(done, function () { fallback(); });
    else fallback();
    function fallback() {
      var ta = h("textarea", { style: "position:fixed;left:-1000px" }); ta.value = text; document.body.appendChild(ta);
      ta.select(); try { document.execCommand("copy"); done(); } catch (e) { toast(t("Copy failed"), true); } ta.remove();
    }
  });
  return b;
}
function cmdLine(text) { return h("div", { class: "cmd" }, h("code", {}, text), copyBtn(text)); }

// dialog shows a modal; returns a close function.
function dialog(title, body, actions, wide) {
  var ov;
  var close = function () { ov.remove(); document.removeEventListener("keydown", esc); };
  var esc = function (ev) { if (ev.key === "Escape") close(); };
  ov = h("div", { class: "overlay", onclick: function (ev) { if (ev.target === ov) close(); } },
    h("div", { class: "dialog" + (wide ? " wide" : "") }, h("div", { class: "dh" }, h("h3", {}, title), h("button", { class: "ghost", onclick: function () { close(); } }, "✕")),
      h("div", { class: "db" }, body), actions && actions.length ? h("div", { class: "row", style: "justify-content:flex-end;margin:16px 0 0" }, actions) : null));
  document.addEventListener("keydown", esc);
  document.body.appendChild(ov);
  return close;
}

function fileList(files) {
  return h("div", { class: "files" }, files.slice(0, 50).map(function (f) { return h("div", {}, h("span", { class: "mono" }, f.path), h("span", { class: "muted small" }, bytes(f.size))); }),
    files.length > 50 ? h("div", { class: "muted small" }, "+" + (files.length - 50)) : null);
}

// queueChange asks the user's machines to change their library.
function queueChange(kind, data, okMsg) {
  return me("POST", "/intents", { kind: kind, data: data }).then(function () {
    toast(okMsg || t("Saved. Your machines apply it within about a minute."));
  }, function (e) { toast(e.message, true); throw e; });
}

// Detail of one of the user's own skills or agents.
function showMyItem(x, settings, allSkills) {
  me("GET", "/library/" + x.type + "/" + encodeURIComponent(x.name)).then(function (d) {
    var actions = [];
    if (x.type === "skill" && settings) {
      var res = settings.resident || [];
      var isRes = !res.length || res.indexOf(x.name) >= 0;
      actions.push(h("button", { onclick: function () {
        var names = res.length ? res.slice() : allSkills.slice();
        if (isRes) names = names.filter(function (n) { return n !== x.name; }); else names.push(x.name);
        queueChange("resident", { names: names }).then(function () { close(); });
      } }, t(isRes ? "Make on demand" : "Make resident")));
    }
    var close = dialog(x.name, [
      h("p", { class: "muted" }, x.description || ""),
      h("div", { class: "row small muted" }, badge(t(x.type === "skill" ? "Skills" : "Agents")), x.uses ? h("span", {}, x.uses + " " + t("uses") + " · 30d") : null,
        x.type === "skill" ? badge(t(x.resident ? "resident" : "on demand"), x.resident ? "acc" : "") : null),
      h("h4", {}, t("Content")), h("pre", { class: "content" }, d.content || "—"),
      h("h4", {}, t("Files") + " (" + d.files.length + ")"), fileList(d.files)], actions, true);
  }, function (e) { toast(e.message, true); });
}

// Detail of a team's shared skill or agent, with version history and the
// option to keep it in every agent of the user.
function showTeamItem(team, p) {
  me("GET", "/teams/" + encodeURIComponent(team) + "/packages/" + p.type + "/" + encodeURIComponent(p.name)).then(function (d) {
    var ref = team + ":" + p.name;
    var linkCmd = "hearth lib link " + (p.type === "agent" ? "--agent " : "") + ref;
    var actions = [d.linked
      ? h("button", { onclick: function () { queueChange("unlink", { type: p.type, name: p.name }).then(function () { close(); }); } }, t("Stop keeping it"))
      : h("button", { class: "pri", onclick: function () { queueChange("link", { type: p.type, name: p.name, team: team }).then(function () { close(); }); } }, t("Keep in all my agents"))];
    var close = dialog(p.name + " · " + team, [
      h("p", { class: "muted" }, p.description || ""),
      h("div", { class: "row small muted" }, badge("v" + d.latest), p.uses ? h("span", {}, p.uses + " " + t("uses") + " · " + p.users + " " + t("people")) : null,
        d.linked ? badge(t("kept in your agents"), "ok") : h("span", {}, t("found on demand by skill search"))),
      h("h4", {}, t("Content")), h("pre", { class: "content" }, d.item.content || "—"),
      h("div", { class: "two" },
        h("div", {}, h("h4", {}, t("Files") + " (" + d.item.files.length + ")"), fileList(d.item.files)),
        h("div", {}, h("h4", {}, t("Versions")), h("div", { class: "files" }, d.versions.map(function (v) {
          return h("div", {}, h("span", {}, "v" + v.version), h("span", { class: "muted small" }, v.published_by + " · " + ago(v.published_at)));
        })))),
      h("h4", {}, t("From a terminal")), cmdLine(linkCmd)], actions, true);
  }, function (e) { toast(e.message, true); });
}

// menuButton opens a small menu of [label, action, class] entries.
function menuButton(label, entries) {
  var pop = null;
  var wrapEl = h("span", { class: "menu-wrap" });
  var btn = h("button", { class: "sm", title: t("More") }, label);
  var hide = function () { if (pop) { pop.remove(); pop = null; document.removeEventListener("click", outside, true); } };
  var outside = function (ev) { if (!wrapEl.contains(ev.target)) hide(); };
  btn.addEventListener("click", function () {
    if (pop) { hide(); return; }
    pop = h("div", { class: "menu-pop" }, entries.map(function (en) {
      return h("button", { class: en[2] || "", onclick: function () { hide(); en[1](); } }, en[0]);
    }));
    wrapEl.appendChild(pop);
    document.addEventListener("click", outside, true);
  });
  wrapEl.appendChild(btn);
  return wrapEl;
}
