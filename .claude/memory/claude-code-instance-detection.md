---
name: claude-code-instance-detection
description: How to detect running Claude Code instances + their busy/idle status on this Windows machine
metadata: 
  node_type: memory
  type: reference
  originSessionId: 31997c1a-341a-4ead-b8d0-fef4e6ee481d
---

To enumerate **running Claude Code instances and their status** on this machine:

1. **Status source of truth**: `~/.claude/sessions/<pid>.json` — one file per live interactive instance. Fields: `pid`, `status` (**busy**/**idle**), `cwd`, `version`, `startedAt` (epoch ms), `updatedAt`, `sessionId`. The filename is the PID. Stale files (pid dead) linger after exit, so verify PID liveness.

1b. **Model / tokens / context source**: `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl` — cwd encoded with `:`, `/`, `\` all → `-` (e.g. `E:\test` → `E--test`), filename == the session's `sessionId`. Scan from the end for the last line with `"type":"assistant"`: `message.model` is the model (this box shows `glm-5.1` — Claude Code is routed through a GLM backend); `message.usage` has `input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `output_tokens`. Current context size ≈ input + cache_creation + cache_read.

2. **Process name trap**: `claude.exe` is used by **TWO different products** — must distinguish by exe path:
   - **Claude Code CLI** (what you usually want): path contains `@anthropic-ai/claude-code` (npm global install). These write session files.
   - **Claude Desktop** (Electron): `C:\Program Files\Claude Desktop\claude.exe` — one main process + many `--type=gpu-process/renderer/utility/crashpad` children. **No session files.** A naive "count claude.exe" hugely overcounts (8+ extra).
   Rule: count a `claude.exe` as a Claude Code instance **only if it has a matching session file** (`~/.claude/sessions/<pid>.json`, cross-check PID start time vs `startedAt` within ~15s to defeat PID reuse). Do NOT accept on exe path alone — see startup-flicker gotcha below. (Earlier the code accepted path-only; that was the bug.)

3. **Startup-flicker gotcha**: when Claude Code launches, `claude.exe` (a 240MB Node SEA, JS embedded) briefly spawns several short-lived helper children (version/update probe etc.) that **share the exact same exe + path** as the main process but write **no session file**. Accepting on path alone made the monitor flash several rows on startup that vanished after ~1-2s, leaving one. Gating on session-file presence hides these helpers (and Claude Desktop, which also has none) cleanly; the real interactive instance writes its session file within ~1s, so it still appears promptly. Confirmed 2026/06/09: claude-code-path procs and session-file PIDs matched exactly, 16 Claude Desktop procs all correctly hidden.

Process enumeration in pure Go (no cgo): `golang.org/x/sys/windows` `CreateToolhelp32Snapshot`/`Process32First|Next` for PID+name; `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` + `QueryFullProcessImageName` for path, `GetProcessTimes` for start time.

Reference impl lives at `E:\test\build\monitor\` (detector.go + main.go, Walk GUI), builds to `E:\test\claude-monitor.exe`.
