#!/usr/bin/env python3
"""Call the Codex app-server (the API Codex Desktop uses) for e2e checks.

    CODEX_HOME=... scripts/codex-appserver.py '[["thread/list", {"limit": 50}]]'

Prints one JSON result per call."""
import json, os, subprocess, sys

calls = json.loads(sys.argv[1])
p = subprocess.Popen(["codex", "app-server"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                     stderr=subprocess.DEVNULL, text=True, cwd="/tmp")

def call(i, method, params):
    p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": i, "method": method, "params": params}) + "\n")
    p.stdin.flush()
    while True:
        line = p.stdout.readline()
        if not line:
            sys.exit("app-server exited")
        m = json.loads(line)
        if m.get("id") == i:
            return m

call(0, "initialize", {"clientInfo": {"name": "hearth-e2e", "version": "0"}})
p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "initialized"}) + "\n")
for i, (method, params) in enumerate(calls, 1):
    print(json.dumps(call(i, method, params), ensure_ascii=False))
p.stdin.close()
p.wait(timeout=10)
