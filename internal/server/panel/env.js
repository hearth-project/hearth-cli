"use strict";
// Machine environment check: a status pill per machine (green ok, amber
// things to do, red problems), a hover card listing the problems, and a
// detail row with the tools table and how to fix it.

var TOOL_NAMES = { codex: "Codex CLI", claude: "Claude Code", "codex-mixin": "codex-mixin", node: "Node.js", hearth: "Hearth" };
function toolName(x) { return TOOL_NAMES[x] || x; }

function zh() { return LANG === "zh"; }

// envText words one problem in the console's language.
function envText(p) {
  var n = toolName(p.tool), z = zh(), cnt = (p.more ? "≥" : "") + p.behind;
  switch (p.code) {
    case "missing": return z ? n + " 未安装" : n + " is not installed";
    case "outdated": return z ? n + " 落后 " + cnt + " 个版本（" + p.have + " → " + p.latest + "）" : n + " is " + cnt + " release(s) behind (" + p.have + " → " + p.latest + ")";
    case "doctor": return z ? n + " 检查未通过：未登录或网关异常" : n + " check failed: not logged in or the gateway is down";
    case "integration": return z ? n + " 未接入 Hearth（skill 检索未注册）" : n + " is not connected to Hearth (skill search not registered)";
    case "node": return z ? "Node.js 版本过低（" + (p.have || "未安装") + "，Codex 需要 ≥ 22）" : "Node.js too old (" + (p.have || "missing") + ", Codex needs ≥ 22)";
    case "steps": return z ? "恢复流程还有 " + p.behind + " 步未完成" : p.behind + " restore step(s) left";
    case "bypass": return z ? "Codex 没有走 codex-mixin 网关，用的是别处的配置：" + p.detail : "Codex does not go through codex-mixin; it uses another config: " + p.detail;
    case "shared_home": return z ? "你的 codex-mixin 登录放在共用的 " + p.detail + "，这个账号上没设 CODEX_HOME 的人都会用到它" : "Your codex-mixin login is in the shared " + p.detail + "; everyone on this account without their own CODEX_HOME uses it";
    case "unreachable": return z ? "Codex 连不上它的模型服务 " + p.have + "（" + p.detail + "）" : "Codex cannot reach its model endpoint " + p.have + " (" + p.detail + ")";
    case "hearth": return z ? "Hearth 客户端有新版本（" + p.have + " → " + p.latest + "）" : "Newer Hearth client available (" + p.have + " → " + p.latest + ")";
  }
  return p.code;
}

function envFix(p, d) {
  var z = zh();
  switch (p.code) {
    case "hearth": return "hearth self-update";
    case "integration": return "hearth lib mcp-install";
    case "doctor": return "codex-mixin doctor --no-tui";
    case "steps": return "hearth recipe plan";
    case "bypass": return "hearth ssh <machine>   # Hearth's sshd marks your sessions; the end of the rc file sets your CODEX_HOME";
    case "unreachable": return "hearth machine check";
    case "shared_home": return "hearth agents isolate codex   # moves your Codex config and conversations into your workspace";
  }
  return null;
}

function envCounts(env) {
  var e = 0, w = 0;
  (env.problems || []).forEach(function (p) { if (p.level === "error") e++; else w++; });
  return { e: e, w: w };
}

function factsLine(f) {
  if (!f) return null;
  var parts = [f.os, f.arch].filter(Boolean);
  if (f.gpu) parts.push((f.gpu_count > 1 ? f.gpu_count + "× " : "") + (f.gpu_name || f.gpu));
  return parts.join(" · ");
}

// envPill is the table cell: a dot and a short summary, with a hover card.
function envPill(d, onToggle) {
  var env = d.env;
  if (!env) return h("span", { class: "muted" }, "-");
  var c = envCounts(env), cls, label;
  if (env.status === "unknown") { cls = "unk"; label = zh() ? "未上报" : "not reported"; }
  else if (env.status === "ok") { cls = "ok"; label = zh() ? "正常" : "ready"; }
  else if (env.status === "error") { cls = "bad"; label = zh() ? (c.e + c.w) + " 个问题" : (c.e + c.w) + " problem(s)"; }
  else { cls = "warn"; label = zh() ? c.w + " 项待处理" : c.w + " to do"; }
  var pill = h("button", { class: "envpill " + cls + (d.online ? "" : " stale"), type: "button", onclick: onToggle }, h("span", { class: "edot" }), label);
  return tip(pill, function () { return envCard(d); });
}

function envCard(d) {
  var env = d.env, z = zh();
  var head = h("div", { class: "envhead" }, h("b", {}, z ? "环境检查" : "Environment check"),
    h("span", { class: "muted" }, [factsLine(d.facts), env.checked_at ? (z ? "检查于 " : "checked ") + ago(env.checked_at) : null].filter(Boolean).join(" · ")));
  if (env.status === "unknown") return [head, h("p", { class: "muted", style: "margin:6px 0 0" }, z ? "这台机器的客户端还没有上报环境信息：在该机器执行 hearth self-update。" : "This machine has not reported its environment yet: run hearth self-update there.")];
  if (!env.problems.length) return [head, h("div", { class: "envok" }, icon("check"), z ? "与标准环境一致，没有发现问题" : "Matches the standard setup, nothing to fix")];
  return [head, h("ul", { class: "envlist" }, env.problems.map(function (p) {
    return h("li", { class: p.level }, h("span", { class: "edot" }), h("span", {}, envText(p)));
  })), h("div", { class: "muted small", style: "margin-top:8px" }, z ? "点击查看详情和修复方法" : "Click for details and how to fix")];
}

// envDetail is the expanded row: problems with fixes, the tools table, and
// the one sentence to say to an agent on another machine.
function envDetail(d) {
  var env = d.env, z = zh();
  var short = /^\d+(\.\d+){3}$/.test(d.name) ? d.name : d.name.split(".")[0]; // keep IP names whole
  var probs = env.problems.length ? h("ul", { class: "envlist big" }, env.problems.map(function (p) {
    var fix = envFix(p, d);
    return h("li", { class: p.level }, h("span", { class: "edot" }),
      h("div", {}, h("div", {}, envText(p)),
        p.code === "doctor" && p.detail ? h("div", { class: "muted small mono" }, p.detail) : null,
        p.code === "steps" && p.steps ? h("div", { class: "muted small" }, p.steps.join(" · ")) : null,
        fix ? h("code", { class: "fix" }, fix) : null));
  })) : h("div", { class: "envok" }, icon("check"), z ? "与标准环境一致，没有发现问题" : "Matches the standard setup, nothing to fix");
  var tools = (env.tools || []).length ? h("table", { class: "envtools" }, h("thead", {}, h("tr", {}, [z ? "工具" : "Tool", z ? "本机" : "Installed", z ? "最新" : "Latest", ""].map(function (k) { return h("th", {}, k); }))),
    h("tbody", {}, env.tools.map(function (x) {
      var st = !x.installed ? badge(z ? "未安装" : "missing", x.expected ? "bad" : "") : x.latest && x.version && x.version !== x.latest ? badge(z ? "可升级" : "update", "warn") : badge("OK", "ok");
      return h("tr", {}, h("td", {}, toolName(x.name)), h("td", { class: "mono" }, x.version || "-"), h("td", { class: "mono" }, x.latest || "-"), h("td", {}, st));
    }))) : null;
  var say = z ? "恢复 " + short + " 的 Codex 环境" : "restore the Codex environment of " + short;
  var how = env.problems.length ? h("div", { class: "envhow" }, h("div", { class: "muted small" }, z ? "在任意一台已登录 Hearth 的机器上，对 agent 说：" : "On any machine logged in to Hearth, tell your agent:"),
    cmdLine(say), h("div", { class: "muted small" }, z ? "agent 会按这台机器的架构选择恢复流程，通过 hearth ssh 逐步装好并检查。" : "It picks the restore flow for this machine's architecture and sets it up over hearth ssh, checking each step.")) : null;
  return h("div", { class: "envdetail" }, h("div", {}, h("h4", {}, z ? "检查结果" : "Findings"), probs, how), tools ? h("div", {}, h("h4", {}, z ? "工具版本" : "Tools"), tools) : null);
}
