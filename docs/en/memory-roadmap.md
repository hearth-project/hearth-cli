# Memory roadmap

[中文](../zh/memory-roadmap.md) · [Back to README](../../README.en.md)

> Status: **M1 shipped, M2 in progress** (see "Unreleased" in [CHANGELOG](../../CHANGELOG.md)). Available now: memory cards, the local FTS5 index, `hearth recall`, `hearth memory remember|forget|list`, MCP `recall` / `remember` / `forget`, the session-start and prompt hooks of Codex, Claude Code and Comate, the 6 KB key memory and `hearth memory tidy`. Still open: all of M3; schedule in the [development roadmap](https://github.com/hearth-project/hearth-cli/issues/1).
>
> Hearth is a **portable workspace** for coding agents (conversations, skills, credentials and setup follow you). This document describes how it gains **long-term memory** on top of that.

## Goals

1. **Used automatically.** Agents use their memory during normal work, without the user asking them to look something up.
2. **Across agents and machines.** What you did in Codex, Claude Code knows; what you learned on machine A is there on machine B.
3. **Fast.** Recall sits on the agent's critical path: local search p95 < 20 ms, a hook including process start < 100 ms, no network on the search path.
4. **Still end-to-end encrypted.** The server keeps storing ciphertext only; all search happens on the client.
5. **Honest retention.** What is kept long term is the small memory; conversation content still expires under the current policy.

## What memory is: three kinds

| Kind | Content | Source | Size | Kept |
| --- | --- | --- | --- | --- |
| **Session cards** | one per conversation: title, goal, outcome, files changed, commands run, errors and their fixes, machine, time | extracted by the background sync | ~1–2 KB each | permanently (small) |
| **Facts and habits** | "runs `make lint` before committing", "this repo's CI runs on H800", "answer in Chinese" | proposed by the agent and confirmed by the user, or "remember …" | tens to hundreds of bytes | permanently, deletable |
| **Conversation content** | the full transcripts | today's sync | large | current expiry policy |

Search covers the first two kinds by default (small, fast, dense); details come from the content, by the session id a card carries.

## Architecture

```text
              agent (Codex / Claude Code / Comate)
   ┌────────────────┬───────────────────┬────────────────────────┐
   │ ① resident     │ ② injected before  │ ③ searched on demand   │
   │   profile      │   each prompt      │   (MCP tools)          │
   │ managed block  │ top-3, by a hook   │ recall / remember      │
   └───────┬────────┴─────────┬─────────┴───────────┬────────────┘
           └──────────────────┼──────────────────────┘
                  local memory index (SQLite FTS5, milliseconds)
                              ▲ updated incrementally by the daemon
           cards and facts (encrypted) ◀── sync ──▶ Hearth server (ciphertext only)
```

### 1. The local index: where speed comes from

- **Every machine downloads all cards and facts and indexes them locally.** Cards of 10,000 conversations are about 10–20 MB, so all of them fit; search never calls the server.
- **SQLite FTS5** (SQLite is already a dependency): BM25 ranking; trigram tokens for Chinese and Japanese, words for English. Field weights: title, outcome, errors > commands, files > the rest.
- **Maintained by the daemon**: new cards are indexed as they arrive; searches only read and never wait for sync.
- **Ranking**: BM25 times a recency decay and a context boost (same repository, same kind of machine).
- **Targets**: p95 < 20 ms over 100,000 entries; `hearth recall` is a Go binary that starts in about 5 ms.
- **Later**: optional vector search (a small local model, or an embedding service the user configures) mixed with BM25; off by default, so speed and privacy do not change.

### 2. Where cards come from

- **Deterministic extraction (default: free, fast)**: the daemon reads synced conversations and takes the title, the first request, the closing outcome, changed file paths, commands run, error lines and the fix that followed. No model calls.
- **Agent-written summaries (optional, better)**: at the end of a conversation the agent writes a summary with the MCP tool `remember_session`, replacing the card's outcome.
- **Scanned before upload**: cards and facts go through the existing credential scan; matches are left out.

### 3. Deep integration: three paths together

| Path | How | Automatic? | Where |
| --- | --- | --- | --- |
| **① Resident profile** | the most important facts and habits (≤ 1 KB) in a managed block of each agent's instruction file (`AGENTS.md`, `CLAUDE.md`, Comate rules), in context from the first message | fully, zero latency | every agent |
| **② Injected before each prompt** | the agent's pre-prompt hook runs `hearth recall --hook` and adds the 3 memories most relevant to this prompt as extra context | fully, independent of the model's judgement | agents with such a hook (e.g. Claude Code's `UserPromptSubmit`); to be checked agent by agent |
| **③ Searched on demand** | MCP `recall(query)` returns ranked excerpts (session id, time, machine); `remember(fact)` / `forget(id)` keep the facts. The profile says "recall first when you hit an error, an unfamiliar repo, or something done before" | the model decides | every agent with MCP |

All three use the same local index. ① keeps habits always in force, ② gives true automatic recall where the agent supports it, ③ covers the rest and the cases that need detail.

**Cost control**: injection has a relevance threshold (nothing below it), at most 3 entries and ~600 characters per prompt; the profile stays under 1 KB.

### 4. Key memory: small and always loaded

Key memory is the set of remembered facts: one per user, end-to-end encrypted, shared by every machine and agent (Codex, Claude Code, Comate) and given whole at the start of every session. Because every session carries it, it stays small:

- **Limit**: 6 KB in all, at most 1000 bytes an entry. When it is full `remember` refuses until entries are merged or forgotten. Conversations and the agents' own notes are not limited by it; `recall` searches them as before.
- **What goes in**: the user's decisions and preferences, verified rules, traps and how to avoid them. One line each, the conclusion and where it holds (everywhere, a repository); details stay in the conversation, pointed to with `hearth recall --show s:…`. Not what the repository or the machine already tells, not the progress of the current task.
- **Who writes**: when the user says "remember …"; and an agent may add an entry by itself when something is clearly worth keeping, telling the user in one sentence.
- **Rewrite and merge**: `remember` with `replaces` (`--replace f:…` on the command line) stores the new entry and forgets the ones it supersedes. Every injected entry carries its id, so an agent can rewrite it.
- **Tidy**: `hearth memory tidy` lists the entries with size and origin and flags the long ones. The agent checks each against the conversations it came from and the current state, proposes keep / rewrite / merge / forget, and changes only what the user agrees to. Hearth calls no model and the server sees no content. Above 85 % the session-start note suggests tidying.

### 5. Team memory (third stage)

- Facts can be shared with a team ("on this cluster NCCL needs `NCCL_IB_HCA=mlx5`"), encrypted with a team key and indexed by every member.
- Session cards stay personal by default.

## Privacy and control

- Cards, facts and the local index are synced encrypted; the index file is mode 0600.
- `hearth memory forget <id>`, dropping a session's card, `[memory] enabled = false` to turn it all off; the upload selection (`config.toml` `[backup]`) applies to cards too.
- Injected memories name their source (session id, date) in the hook output, so the user sees why the agent knows something.

## Milestones

| Stage | Content | Done when |
| --- | --- | --- |
| **M1 Recall** | deterministic session cards and their sync; local FTS5 index; `hearth recall`; MCP `recall` | p95 < 20 ms over 10,000 conversations; finds conversations of other machines and other agents |
| **M2 Automatic** | resident profile block; `remember` / `forget` / `hearth memory review`; automatic injection through Claude Code's pre-prompt hook; other agents' hooks one by one | without being told, an agent uses an outcome from the last machine; injection < 100 ms |
| **M3 Quality and teams** | agent-written summaries; optional hybrid vector search; shared team facts; viewing and editing memory in the console | hit rate and citation rate visible in the console |

## Open questions

- Whether Codex and Comate offer a pre-prompt hook; without one, the profile and the MCP tool have to carry it, which needs measuring.
- How trigram tokens handle mixed Chinese / English code and error text; to be evaluated on real conversations, with a custom tokenizer if needed.
- Card extraction rules need iterating on real conversations to keep noise (long logs, large code blocks) out.
