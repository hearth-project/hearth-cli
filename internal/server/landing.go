package server

import (
	"html/template"
	"net/http"
	"strings"
)

// The landing page (GET /) lists this deployment's install command and
// download links, in Chinese and English, so an admin can just share the
// server URL.
var landingTmpl = template.Must(template.New("landing").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Hearth · {{.URL}}</title>
<style>
body{font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,"PingFang SC","Microsoft YaHei",sans-serif;max-width:860px;margin:2rem auto;padding:0 1rem;color:#1f2328}
h1{margin-bottom:.2rem}code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:13px}
pre{background:#f6f8fa;padding:12px;border-radius:6px;overflow-x:auto;white-space:pre-wrap;word-break:break-all}
table{border-collapse:collapse;width:100%}td,th{border:1px solid #d0d7de;padding:6px 8px;text-align:left}th{background:#f6f8fa}
.muted{color:#59636e}.lang{float:right}a{color:#0969da}
.btn{display:inline-block;background:#ea580c;color:#fff;padding:8px 18px;border-radius:8px;font-weight:600;text-decoration:none}
</style></head><body>
<div class="lang">{{if eq .Lang "zh"}}<b>简体中文</b> | <a href="/?lang=en">English</a>{{else}}<a href="/?lang=zh">简体中文</a> | <b>English</b>{{end}}</div>
<h1>Hearth</h1>
<p class="muted">{{.T.Tagline}}</p>
<p><a class="btn" href="/app">{{.T.Console}}</a></p>

<h2>{{.T.Install}}</h2>
<p>{{.T.InstallHint}}</p>
<pre>{{.Cmd}}</pre>
<p>{{.T.NewUser}}</p>
<pre>{{.CmdNew}}</pre>
<p>{{.T.InstallOnly}}</p>
<pre>{{.CmdOnly}}</pre>
<p class="muted">{{.T.Registration}}: <code>{{.Registration}}</code>{{if .Pin}} · {{.T.Pin}}{{end}}</p>

<h2>{{.T.Downloads}}</h2>
<table><tr><th>{{.T.Platform}}</th><th>{{.T.File}}</th><th>SHA-256</th></tr>
{{range .Bins}}<tr><td>{{.Label}}</td><td><a href="/download/{{.Name}}">{{.Name}}</a></td><td><code>{{.SHA}}</code></td></tr>
{{end}}</table>
<p class="muted">{{.T.Manual}}</p>
<pre>{{.ManualCmd}}</pre>

<h2>{{.T.More}}</h2>
<ul>
<li><a href="/install.sh">/install.sh</a> — {{.T.Script}}</li>
<li><a href="/v1/dist">/v1/dist</a> — {{.T.Manifest}}</li>
<li><a href="/onboard/SKILL.md">/onboard/SKILL.md</a> — {{.T.Skill}}</li>
<li><a href="/v1/info">/v1/info</a> — {{.T.Info}}</li>
{{if .Pin}}<li><a href="/ca.pem">/ca.pem</a> — {{.T.CA}}</li>{{end}}
</ul>
</body></html>
`))

type landingText struct {
	Tagline, Install, InstallHint, NewUser, InstallOnly, Registration, Pin string
	Downloads, Platform, File, Manual, More, Script, Manifest, Skill, Info string
	CA, Console                                                            string
}

var landingTexts = map[string]landingText{
	"zh": {
		Tagline:      "跨机器备份与恢复 Coding Agent 的会话、技能、凭证和环境（端到端加密）。",
		Install:      "一键安装（Linux / macOS）",
		InstallHint:  "在终端执行。脚本按系统和架构选择安装包、校验 SHA-256、安装到 ~/.local/bin、写入 PATH、给本机的 Codex / Claude Code / Comate 装上 hearth skill，然后登录并恢复环境：",
		NewUser:      "第一次使用（创建账号）：",
		InstallOnly:  "只安装，之后在 agent 里说「用 hearth」由它引导登录：",
		Registration: "注册方式",
		Pin:          "服务器为自签证书：-k 只跳过 CA 校验，--pinnedpubkey 仍会核对服务器公钥",
		Downloads:    "下载",
		Platform:     "平台",
		File:         "文件",
		Manual:       "手动安装（不用安装脚本）：",
		More:         "其他地址",
		Script:       "安装脚本",
		Manifest:     "安装包与校验和清单（JSON）",
		Skill:        "hearth agent skill（未安装 hearth 的机器也可直接让 agent 读取）",
		Info:         "服务器信息（注册方式）",
		CA:           "服务器自签证书（手动安装时保存为 ~/.hearth/ca.pem）",
		Console:      "登录控制台：查看我的机器、skills、agents 和团队",
	},
	"en": {
		Tagline:      "Back up and restore coding-agent sessions, skills, credentials and setup across machines (end-to-end encrypted).",
		Install:      "One-line install (Linux / macOS)",
		InstallHint:  "Run in a terminal. The script picks the build for your OS and CPU, checks its SHA-256, installs it to ~/.local/bin, adds it to PATH, installs the hearth skill for Codex / Claude Code / Comate on this machine, then logs in and restores your environment:",
		NewUser:      "First time (creates the account):",
		InstallOnly:  "Install only; then ask your agent \"use hearth\" and it guides you through login:",
		Registration: "Registration",
		Pin:          "self-signed server: -k only skips CA validation, --pinnedpubkey still checks the server's key",
		Downloads:    "Downloads",
		Platform:     "Platform",
		File:         "File",
		Manual:       "Manual install (without the script):",
		More:         "Other endpoints",
		Script:       "install script",
		Manifest:     "builds and checksums (JSON)",
		Skill:        "the hearth agent skill (an agent can read it on a machine without hearth)",
		Info:         "server info (registration mode)",
		CA:           "the server's self-signed certificate (save as ~/.hearth/ca.pem for a manual install)",
		Console:      "Sign in to the console: your machines, skills, agents and teams",
	},
}

var platformLabel = map[string]string{
	"hearth-linux-amd64":  "Linux x86_64",
	"hearth-linux-arm64":  "Linux ARM64 (aarch64)",
	"hearth-darwin-arm64": "macOS Apple Silicon",
	"hearth-darwin-amd64": "macOS Intel",
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	d := s.dist
	if d == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("hearth server\n"))
		return
	}
	lang := r.URL.Query().Get("lang")
	if lang != "zh" && lang != "en" {
		lang = "en"
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "zh") {
			lang = "zh"
		}
	}
	cmd := s.InstallCommand()
	base := strings.TrimSuffix(cmd, " | sh")
	type bin struct{ Name, Label, SHA string }
	var bins []bin
	for _, n := range d.names() {
		label := platformLabel[n]
		if label == "" {
			label = n
		}
		bins = append(bins, bin{n, label, d.bins[n].sha256})
	}
	curl := "curl -fsSL"
	if d.pin != "" {
		curl = "curl -fsSL -k"
		if d.PinInstall {
			curl += " --pinnedpubkey '" + d.pin + "'"
		}
	}
	manual := "mkdir -p ~/.local/bin ~/.hearth\n" +
		"BIN=hearth-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')\n" +
		curl + " -o ~/.local/bin/hearth " + d.PublicURL + "/download/$BIN && chmod +x ~/.local/bin/hearth\n"
	if d.pin != "" {
		manual += curl + " -o ~/.hearth/ca.pem " + d.PublicURL + "/ca.pem   # self-signed server certificate\n"
	}
	manual += "export PATH=\"$HOME/.local/bin:$PATH\"\n" +
		"hearth setup --server " + d.PublicURL
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	landingTmpl.Execute(w, map[string]any{
		"Lang": lang, "T": landingTexts[lang], "URL": d.PublicURL, "Pin": d.pin != "",
		"Cmd": cmd, "CmdNew": base + " | sh -s -- --new", "CmdOnly": base + " | sh -s -- --install-only",
		"Registration": s.registration(), "Bins": bins, "ManualCmd": manual,
	})
}

// handleCA serves the self-signed server certificate (for manual installs;
// the install script embeds it).
func (s *Server) handleCA(w http.ResponseWriter, _ *http.Request) {
	if s.dist == nil || len(s.dist.CertPEM) == 0 {
		writeErr(w, http.StatusNotFound, "no self-signed certificate")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Write(s.dist.CertPEM)
}
