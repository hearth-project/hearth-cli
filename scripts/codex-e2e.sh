#!/usr/bin/env bash
# End-to-end checks against a real Codex CLI. Uses temporary homes; the only
# things copied from this machine are ~/.codex/config.toml and auth.json
# (model provider settings). Makes 5 model calls.
#
#   scripts/codex-e2e.sh [path/to/hearth]
set -uo pipefail
B=${1:-$(cd "$(dirname "$0")/.." && pwd)/bin/hearth}
SRC_CODEX=${SRC_CODEX:-$HOME/.codex}
command -v codex >/dev/null || { echo "codex not installed"; exit 2; }
T=$(mktemp -d /tmp/hearth-codex-e2e.XXXX)
PORT=${PORT:-18899}
pass=0; fail=0
ok()  { echo "  PASS $*"; pass=$((pass+1)); }
bad() { echo "  FAIL $*"; fail=$((fail+1)); }
cleanup() { [ -n "${SP:-}" ] && kill "$SP" 2>/dev/null; wait 2>/dev/null; rm -r "$T"; }
trap cleanup EXIT
export HEARTH_PASSWORD=e2e-password-123

machine() { # isolated hearth + codex homes for one simulated machine
  # Each machine keeps its work somewhere else: transcripts are path-translated.
  export HEARTH_WORKSPACE=$T/$1/work-$1
  mkdir -p "$HEARTH_WORKSPACE"
  export HEARTH_HOME=$T/$1/as CODEX_HOME=$T/$1/codex CLAUDE_CONFIG_DIR=$T/$1/claude HEARTH_AGENTS=codex
  mkdir -p "$CODEX_HOME"
  cp "$SRC_CODEX/config.toml" "$SRC_CODEX/auth.json" "$CODEX_HOME/" 2>/dev/null
  chmod 600 "$CODEX_HOME"/* 2>/dev/null
}
cx() { (cd "$HEARTH_WORKSPACE" && timeout 240 codex exec --skip-git-repo-check -s read-only "$@" 2>&1); }

"$B" server --data "$T/srv" --addr 127.0.0.1:$PORT --registration open >"$T/server.log" 2>&1 &
SP=$!
sleep 1
URL=http://127.0.0.1:$PORT

echo "== machine A: library + a real Codex session"
machine a
"$B" register --server $URL --email e2e@example.com >/dev/null &&
  "$B" login --server $URL --email e2e@example.com --device a >/dev/null || { echo "login failed"; exit 1; }
L=$HEARTH_HOME/library
mkdir -p "$L/skills/zebra-probe" "$L/agents" "$L/instructions"
printf -- '---\nname: zebra-probe\ndescription: ZEBRA_SKILL_MARKER probe skill for hearth e2e\n---\nThe zebra word is STRIPES.\n' >"$L/skills/zebra-probe/SKILL.md"
printf -- '---\nname: zebra-agent\ndescription: ZEBRA_AGENT_MARKER probe agent\n---\nYou are a probe.\n' >"$L/agents/zebra-agent.md"
printf 'ZEBRA_INSTRUCTIONS_MARKER: keep answers short.\n' >"$L/instructions/AGENTS.md"
"$B" lib sync >/dev/null
out=$(cx "Remember the code word PELICAN-42. Reply with exactly: noted")
echo "$out" | grep -qi noted && ok "codex session on A" || bad "codex session on A: $(echo "$out" | tail -2)"
F=$(find "$CODEX_HOME/sessions" -name '*.jsonl' | head -1)
grep -q ZEBRA_SKILL_MARKER "$F" && ok "skill symlinked into CODEX_HOME is loaded" || bad "skill not in the prompt"
grep -q ZEBRA_INSTRUCTIONS_MARKER "$F" && ok "AGENTS.md symlink is loaded" || bad "instructions not in the prompt"
SID=$(basename "$F" .jsonl | grep -oE '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
# Rename and pin the thread the way Codex Desktop does (app-server API).
AS=$(dirname "$0")/codex-appserver.py
PINNED=01984de2-8f74-7c91-a3b2-5c5e937cf318
"$AS" '[["thread/name/set",{"threadId":"'$SID'","name":"ZEBRA_THREAD_NAME"}],["thread/section/move",{"threadId":"'$SID'","sectionId":"'$PINNED'"}]]' >/dev/null
UPD_A=$("$AS" '[["thread/list",{"limit":50,"sourceKinds":["exec","cli","vscode"]}]]' | python3 -c 'import json,sys; print([t["updatedAt"] for t in json.loads(sys.stdin.read())["result"]["data"] if t["id"]=="'$SID'"][0])')
"$B" push >/dev/null && "$B" lib push >/dev/null && ok "pushed session and library" || bad "push"

echo "== machine B: setup from nothing, then resume A's session"
machine b
"$B" setup --server $URL --email e2e@example.com --device b --no-daemon >"$T/setup.log" 2>&1 && ok "setup" || bad "setup: $(tail -3 "$T/setup.log")"
[ -L "$CODEX_HOME/skills/zebra-probe" ] && [ -f "$CODEX_HOME/agents/zebra-agent.toml" ] && ok "library rendered on B" || bad "library not rendered on B"
out=$(cx "Do not run tools. If you can spawn sub-agents, list the agent type names you accept, comma separated; else reply NONE.")
echo "$out" | grep -q zebra-agent && ok "agent rendered into CODEX_HOME/agents is offered" || bad "agent not offered: $(echo "$out" | tail -1)"
T_B=$("$AS" '[["thread/list",{"limit":50,"sourceKinds":["exec","cli","vscode"]}]]' | python3 -c 'import json,sys
for t in json.loads(sys.stdin.read())["result"]["data"]:
    if t["id"]=="'$SID'": print(t.get("name"), (t.get("section") or {}).get("id"), t["updatedAt"], t.get("cwd"))')
[ "$T_B" = "ZEBRA_THREAD_NAME $PINNED $UPD_A $HEARTH_WORKSPACE" ] && ok "Codex thread list on B keeps name, Pinned section and recency; cwd is B's workspace" ||
  bad "thread list on B: '$T_B' (want name, $PINNED, $UPD_A, $HEARTH_WORKSPACE)"
grep -q "$T/a/work-a" "$(find "$CODEX_HOME/sessions" -name '*.jsonl' | head -1)" && bad "A's workspace path left in B's rollout" || ok "rollout on B uses B's paths"
# What the Desktop sidebar sees: Codex's state database only, filtered by the open project.
N=$("$AS" '[["thread/list",{"limit":50,"useStateDbOnly":true,"cwd":"'$HEARTH_WORKSPACE'","sourceKinds":["exec","cli","vscode"]}]]' | python3 -c 'import json,sys; print(sum(t["id"]=="'$SID'" for t in json.loads(sys.stdin.read())["result"]["data"]))')
[ "$N" = 1 ] && ok "restored thread is in Codex's state database under B's workspace (Desktop sidebar)" || bad "thread not indexed for the Desktop sidebar"
out=$(cd "$HEARTH_WORKSPACE" && timeout 240 codex exec --skip-git-repo-check -s read-only resume "$SID" "Do not run tools. What code word did I ask you to remember? Reply with the word only." 2>&1)
echo "$out" | grep -q 'PELICAN-42' && ok "codex resume of the restored session keeps context" || bad "resume: $(echo "$out" | tail -2)"
# B's new turns go to the server in A's form: a third machine sees its own paths.
"$B" push >/dev/null 2>&1 && "$B" daemon stop >/dev/null 2>&1
machine c
"$B" login --server $URL --email e2e@example.com --device c >/dev/null && "$B" restore >/dev/null 2>&1
FC=$(find "$CODEX_HOME/sessions" -name "*$SID*.jsonl" | head -1)
[ -n "$FC" ] && [ "$(grep -c "$T/c/work-c" "$FC")" -ge 2 ] && ! grep -q "$T/[ab]/work-" "$FC" &&
  ok "B's resumed turns reach machine C with C's paths" || bad "machine C rollout: $(grep -o "$T/[abc]/work-[abc]" "$FC" | sort | uniq -c | tr '\n' ' ')"
machine b

echo "== MCP: skill search from Codex"
cat >"$HEARTH_HOME/library/mcp.toml" <<TOML
[servers.hearth-skills]
command = "$B"
args = ["mcp"]
env = { HEARTH_HOME = "$HEARTH_HOME", HEARTH_AGENTS = "codex" }
TOML
"$B" mcp-config sync >/dev/null && codex mcp list 2>/dev/null | grep -q hearth-skills && ok "MCP server registered in codex" || bad "MCP registration"
out=$(cx "Use the hearth-skills search_skills tool to find the skill about zebra, load it with get_skill, then reply with the zebra word only.")
echo "$out" | grep -q 'hearth-skills/search_skills (completed)' && echo "$out" | grep -q 'STRIPES' && ok "search_skills + get_skill through MCP" || bad "MCP skill search: $(echo "$out" | grep -E 'mcp:|STRIPES' | tail -3)"

echo "== MCP: Codex records a setup step for the user's next machine"
# Interactive Codex asks the user to approve this write; exec runs with approval "never", so pre-approve it.
out=$(cx -c 'mcp_servers.hearth-skills.tools.propose_setup_step.approval_mode="approve"' "I just installed the tool 'zebractl' on this machine with the command: npm i -g zebractl@1.2.3 . Every new machine of mine needs it. Record it with the hearth-skills propose_setup_step tool as a run step named zebractl (reason: zebra tooling), then reply done.")
P=$(ls "$HEARTH_HOME/library/recipes/proposals/"*.toml 2>/dev/null | head -1)
[ -n "$P" ] && grep -q 'zebractl' "$P" && ok "propose_setup_step wrote a proposal" || bad "proposal: $(echo "$out" | grep -E 'mcp:|propose' | tail -3)"
if [ -n "$P" ]; then
  "$B" recipe proposals accept "$(basename "$P" .toml)" >/dev/null && grep -q 'npm i -g zebractl' "$HEARTH_HOME/library/recipes/learned.toml" &&
    ok "accepted proposal is in the learned recipe" || bad "accept proposal"
fi

echo "== skill hearth: Codex finishes setting up a logged-in machine"
machine d
export HOME_SAVED=$HOME
D_HOME=$T/d/home; mkdir -p "$D_HOME/.ssh" "$D_HOME/.config/gh"
printf -- '-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n' >"$D_HOME/.ssh/id_e2e"
printf 'github.com:\n  oauth_token: gho_fake\n' >"$D_HOME/.config/gh/hosts.yml"
"$B" login --server $URL --email e2e@example.com --device d >/dev/null   # step 4, done by the user
PATH=$(dirname "$B"):$PATH HOME=$D_HOME "$B" onboard install-skill >/dev/null
[ -f "$CODEX_HOME/skills/hearth/SKILL.md" ] && ok "hearth skill installed for Codex" || bad "skill not installed"
out=$(cd "$HEARTH_WORKSPACE" && PATH=$(dirname "$B"):$PATH HOME=$D_HOME timeout 600 codex exec --skip-git-repo-check -s danger-full-access \
  "Set up hearth on this machine using the hearth skill. I already logged in. My decisions: keep every credential and setup item for all machines, no other steps, do not approve any recipe commands and do not start the daemon. Do not ask me questions; finish and give a short summary." 2>&1)
SEC=$(PATH=$(dirname "$B"):$PATH HOME=$D_HOME "$B" secret list 2>/dev/null)
echo "$SEC" | grep -q id_e2e && echo "$SEC" | grep -q hosts && ok "skill: Codex learned the SSH key and gh login as secrets" || bad "skill learn: $(echo "$out" | tail -5)"
grep -q 'ssh-key' "$HEARTH_HOME/library/recipes/learned.toml" 2>/dev/null && ok "skill: learned recipe written" || bad "no learned recipe"
codex mcp get hearth >/dev/null 2>&1 && ok "skill: hearth MCP registered with Codex" || bad "skill: MCP not registered"

echo
echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
