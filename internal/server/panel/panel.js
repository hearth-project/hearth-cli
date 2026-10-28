"use strict";
// Hearth console: plain DOM, no dependencies. Everything shown comes from
// /v1/me and /v1/admin and is inserted as text (never as HTML).
var LANG = localStorage.getItem("hearth-lang") || ((navigator.language || "en").toLowerCase().indexOf("zh") === 0 ? "zh" : "en");
function t(k) { var d = window.I18N[LANG] || {}; return d[k] || k; }

function h(tag, attrs) {
  var el = document.createElement(tag);
  attrs = attrs || {};
  Object.keys(attrs).forEach(function (k) {
    var v = attrs[k];
    if (v === undefined || v === null || v === false) return;
    if (k.slice(0, 2) === "on") el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else if (k === "style") el.style.cssText = v; // CSSOM: allowed by the CSP, unlike style attributes
    else el.setAttribute(k, v === true ? "" : v);
  });
  for (var i = 2; i < arguments.length; i++) add(el, arguments[i]);
  return el;
}
function add(el, c) {
  if (c === undefined || c === null || c === false) return;
  if (Array.isArray(c)) { c.forEach(function (x) { add(el, x); }); return; }
  el.appendChild(typeof c === "object" ? c : document.createTextNode(String(c)));
}
// svg builds an icon from path data (24x24, stroked).
var ICONS = {
  home: "M3 11l9-8 9 8M5 10v10h14V10", machine: "M3 5h18v11H3zM8 20h8M12 16v4", lib: "M4 4h6v16H4zM14 4l6 2-4 14-6-2z",
  team: "M16 19v-1a4 4 0 0 0-8 0v1M12 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6M20 19v-1a3 3 0 0 0-3-3M4 19v-1a3 3 0 0 1 3-3",
  chart: "M4 20V10M10 20V4M16 20v-7M22 20H2", users: "M17 20v-1a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v1M10 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M21 20v-1a4 4 0 0 0-3-3.9",
  check: "M5 12l4 4L19 6", mail: "M3 6h18v12H3zM3 7l9 7 9-7", gear: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z",
  tool: "M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18v3h3l6.3-6.3a4 4 0 0 0 5.4-5.4l-2.5 2.5-2.4-.6-.6-2.4z", log: "M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01",
  db: "M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
  flame: "M12 22c4 0 7-2.7 7-6.8 0-3.4-2.4-5.6-4-7.7-.6 1.9-1.6 3-3 3.6.4-3.3-1-6.4-4-8.1.3 3.5-1.3 5.6-2.7 7.5C4 12 3 13.6 3 15.4 3 19.4 6.6 22 12 22z",
  inbox: "M22 12h-6l-2 3h-4l-2-3H2M5.5 5h13L22 12v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-6z",
  chat: "M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z", bolt: "M13 2L3 14h9l-1 8 10-12h-9z", agent: "M12 8V4M8 4h8M5 8h14v10H5zM9 13h.01M15 13h.01"
};
function icon(name) {
  var NS = "http://www.w3.org/2000/svg", s = document.createElementNS(NS, "svg"), p = document.createElementNS(NS, "path");
  s.setAttribute("viewBox", "0 0 24 24"); s.setAttribute("fill", "none"); s.setAttribute("stroke", "currentColor");
  s.setAttribute("stroke-width", "1.8"); s.setAttribute("stroke-linecap", "round"); s.setAttribute("stroke-linejoin", "round");
  p.setAttribute("d", ICONS[name] || ICONS.home); s.appendChild(p);
  return s;
}
function bytes(n) {
  if (!n) return "0 B";
  var u = ["B", "KiB", "MiB", "GiB", "TiB"], i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i ? n.toFixed(1) : n) + " " + u[i];
}
function parseBytes(s) {
  s = String(s || "").trim();
  if (!s || s === "0" || /^unlimited$/i.test(s)) return 0;
  var m = /^([\d.]+)\s*([kmgt]?)(i?b)?$/i.exec(s);
  if (!m) return NaN;
  var p = { "": 0, k: 1, m: 2, g: 3, t: 4 }[m[2].toLowerCase()];
  var base = m[3] && m[3].toLowerCase() === "b" && m[2] ? 1000 : 1024;
  return Math.round(parseFloat(m[1]) * Math.pow(base, p));
}
function when(ts) {
  if (!ts) return t("never");
  var d = typeof ts === "number" ? new Date(ts * 1000) : new Date(ts);
  if (d.getFullYear() < 1980) return t("never");
  return d.toLocaleString(LANG === "zh" ? "zh-CN" : undefined, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
function ago(ts) {
  var d = typeof ts === "number" ? ts * 1000 : Date.parse(ts);
  if (!d || d < 315532800000) return t("never");
  var s = (Date.now() - d) / 1000, zh = LANG === "zh";
  if (s < 60) return zh ? "刚刚" : "just now";
  var units = [[86400, zh ? "天前" : "d ago"], [3600, zh ? "小时前" : "h ago"], [60, zh ? "分钟前" : "min ago"]];
  for (var i = 0; i < units.length; i++) if (s >= units[i][0]) return Math.floor(s / units[i][0]) + (zh ? "" : " ") + units[i][1];
}

function toast(msg, err) {
  var d = h("div", { class: err ? "err" : "" }, msg);
  document.getElementById("toast").appendChild(d);
  setTimeout(function () { d.remove(); }, err ? 6000 : 2500);
}

// call: JSON request to the server with the session cookie. Answers
// without a body (201, 204) are fine.
function call(method, path, body) {
  var opt = { method: method, credentials: "same-origin", headers: { "X-Hearth-Admin": "1" } };
  if (body !== undefined) { opt.headers["Content-Type"] = "application/json"; opt.body = JSON.stringify(body); }
  return fetch(path, opt).then(function (r) {
    if (r.status === 401) { showLogin(); throw new Error("unauthorized"); }
    return r.text().then(function (txt) {
      var j = null;
      if (txt) { try { j = JSON.parse(txt); } catch (e) { j = null; } }
      if (!r.ok) throw new Error(t((j && j.error) || r.statusText || ("HTTP " + r.status)));
      return j;
    });
  });
}
function api(method, path, body) { return call(method, "/v1/admin" + path, body); } // admin pages
function me(method, path, body) { return call(method, "/v1/me" + path, body); }   // the user's own pages

// act runs a change, reports it and redraws the view.
function act(promise, ok) {
  return promise.then(function (r) { toast(ok || t("Done")); render(); return r; },
    function (e) { if (e.message !== "unauthorized") toast(e.message, true); });
}

function kpi(label, value, ic, cls) {
  return h("div", { class: "card kpi" }, h("div", { class: "ic" }, icon(ic || "chart")),
    h("div", {}, h("div", { class: "v " + (cls || "") }, value), h("div", { class: "l" }, label)));
}
function badge(text, cls) { return h("span", { class: "badge " + (cls || "") }, text); }
function statusBadge(s) { return badge(t(s), { active: "ok", disabled: "bad", pending: "warn" }[s] || ""); }
function avatar(email) { return h("span", { class: "avatar" }, (email || "?").slice(0, 1).toUpperCase()); }
function card(title, body, extra) { return h("div", { class: "card" }, h("div", { class: "head" }, h("h3", {}, title), extra), body); }
function empty(text, ic) { return h("div", { class: "empty" }, h("div", { class: "big" }, icon(ICONS[ic] ? ic : "inbox")), text); }
function wrap(table) { return h("div", { class: "tablewrap" }, table); }

// chart: bars per day with a light grid and the max value on the left.
function chart(values, labels, fmt) {
  var NS = "http://www.w3.org/2000/svg", W = 640, H = 170, top = 12, bot = 20;
  function el(n, a) { var e = document.createElementNS(NS, n); for (var k in a) e.setAttribute(k, a[k]); return e; }
  var svg = el("svg", { viewBox: "0 0 " + W + " " + H, class: "chart", preserveAspectRatio: "none" });
  var max = Math.max.apply(null, values.concat([1])), bw = W / values.length;
  for (var g = 0; g <= 3; g++) svg.appendChild(el("line", { x1: 0, x2: W, y1: top + (H - top - bot) * g / 3, y2: top + (H - top - bot) * g / 3 }));
  values.forEach(function (v, i) {
    var bh = (H - top - bot) * v / max;
    var r = el("rect", { class: "b", x: i * bw + bw * 0.18, y: H - bot - bh, width: Math.max(bw * 0.64, 1), height: Math.max(bh, v ? 2 : 0), rx: 2 });
    var tt = document.createElementNS(NS, "title");
    tt.textContent = labels[i] + ": " + (fmt ? fmt(v) : v);
    r.appendChild(tt); svg.appendChild(r);
  });
  var lt = el("text", { x: 0, y: H - 5 }); lt.textContent = labels[0]; svg.appendChild(lt);
  var rt = el("text", { x: W, y: H - 5, "text-anchor": "end" }); rt.textContent = labels[labels.length - 1]; svg.appendChild(rt);
  var mt = el("text", { x: 2, y: top - 2 }); mt.textContent = fmt ? fmt(max) : max; svg.appendChild(mt);
  return svg;
}

// tip shows content() in a floating box while the pointer is over el.
var tipBox = null;
function tip(el, content) {
  el.classList.add("hastip");
  el.addEventListener("mouseenter", function () {
    if (!tipBox) { tipBox = h("div", { class: "tip" }); document.body.appendChild(tipBox); }
    tipBox.textContent = "";
    add(tipBox, content());
    tipBox.style.display = "block";
  });
  el.addEventListener("mousemove", function (ev) {
    if (!tipBox) return;
    var w = tipBox.offsetWidth, hh = tipBox.offsetHeight, x = ev.clientX + 14, y = ev.clientY + 14;
    if (x + w > window.innerWidth - 8) x = ev.clientX - w - 14;
    if (y + hh > window.innerHeight - 8) y = Math.max(8, ev.clientY - hh - 14);
    tipBox.style.left = x + "px"; tipBox.style.top = y + "px";
  });
  el.addEventListener("mouseleave", function () { if (tipBox) tipBox.style.display = "none"; });
  return el;
}

// bars draws one labelled bar per item; label(x) names it, tipOf(x)
// (optional) fills the hover box.
function bars(items, fmt, tipOf, label) {
  if (!items || !items.length) return empty(t("No usage yet"), "📊");
  var max = Math.max.apply(null, items.map(function (x) { return x.count; }));
  return items.map(function (x) {
    var name = label ? label(x) : x.name;
    var row = h("div", { class: "bar" }, h("span", { class: "n", title: tipOf ? null : name }, name),
      h("span", { class: "t" }, h("i", { style: "width:" + (100 * x.count / max).toFixed(1) + "%" })),
      h("span", { class: "c" }, fmt ? fmt(x) : x.count));
    return tipOf ? tip(row, function () { return tipOf(x); }) : row;
  });
}
