# Setup notes for the operator: files to add, isolation, per-tool config

## 1. Files to add to `feat/greenlight-backend`
| File | Content |
|---|---|
| `docs/greenlight/AGENTS.md` | the agent rules |
| `docs/greenlight/SUCCESS-CRITERIA.md` | the definition of done |
| `docs/greenlight/START-PROMPT.md` | the first prompt you paste into the agent |
| `docs/greenlight/TOOL-CONFIGS.md` | this file |
| `docs/greenlight/CLAUDE.md` | one line, `@AGENTS.md` (an import; Claude Code loads nested `CLAUDE.md` when it works in that folder) |
| root `AGENTS.md` | the block below is prepended. **Do not replace** the file: it is Alby's own guide. Root `CLAUDE.md` is already a symlink to it, so Claude Code gets the pointer too |
| `docs/greenlight/mainnet-trial/LOG.md` | created by the agent on a local branch during the trial |

Block at the top of the root file:
```
> **Greenlight mainnet trial (branch `feat/greenlight-backend` only).** Before doing anything else,
> read and follow `docs/greenlight/AGENTS.md`. It overrides this file for any work involving
> Greenlight, the mainnet trial, real funds, secrets, or GitHub actions.
```
This change lives only on `feat/greenlight-backend`; the four `pr/*` branches stay untouched.

## 2. Isolation (preferred: separate OS user)
Run the agent as its own Linux user. The hub, the workdirs and the cert stay with the operator's user.
On WSL2, run the agent **inside** the Linux distro as that user; a Windows-side agent can reach the
distro's files through `\\wsl$` and bypasses this separation.
```bash
sudo adduser --disabled-password glagent
sudo groupadd gltrial && sudo usermod -aG gltrial glagent && sudo usermod -aG gltrial "$USER"
sudo install -d -m 2750 -o "$USER" -g gltrial /srv/gl-trial-share   # operator writes, agent reads only
chmod 700 "$HOME"                                                    # agent cannot list the operator home
sudo -u glagent ls "$HOME"                                           # must say: Permission denied
```
Log bridge (operator terminal, during the trial; the agent never sees the raw log):
```bash
WD=<workdir>; S=/srv/gl-trial-share
while sleep 30; do (umask 027; python3 "$KIT/tools/redact.py" < "$WD/log/nwc.log" > "$S/.nwc.tmp") \
  && mv "$S/.nwc.tmp" "$S/nwc.redacted.log"; done
```
Giving the agent an NWC connection URL (a spending secret): never in chat, the repo, logs or evidence.
Write it straight into a file only the agent user can read, by pasting into stdin (never on a command line):
```bash
sudo -u glagent -H sh -c 'umask 077; mkdir -p ~/.nwc; cat > ~/.nwc/observer.secret'   # paste, Enter, Ctrl-D
```
Use the same command with `trial-agent.secret` (step 7), `trial-keysend.secret` (step 6) or
`trial-l402.secret` (step 7A) only when you give the GO for that step. When the step ends, delete the
connection in the hub UI, then `sudo -u glagent shred -u ~glagent/.nwc/<name>.secret`. The observer
connection gets get info, get balance, lookup invoice and list transactions only. Before retiring hub A
(step 10.1, while it still runs alone) delete all of its connections; create new ones on hub B. At trial end delete every connection.
Optional hardening: mount `/proc` with `hidepid=invisible` so the agent user cannot see your processes.

## 3. Per-tool configuration (defence in depth; set up in the agent user's checkout)
**Claude Code** (`.claude/settings.json`; Read deny rules cover built-in tools and recognised Bash file
commands, the sandbox enforces at OS level for Bash and its children):
```json
{
  "permissions": {
    "deny": ["Read(//home/OPERATOR/**)", "Read(**/client-key.pem)", "Read(**/*.secret)",
             "Read(**/trial.env)", "Read(**/hsm_secret)", "Read(**/nwc.db)", "Read(**/*.bkp)",
             "Bash(git push *)", "Bash(gh *)"],
    "ask": ["Bash(curl *)", "Bash(git commit *)"]
  },
  "sandbox": { "enabled": true,
    "filesystem": { "denyRead": ["/home/OPERATOR"] } }
}
```
Use Manual mode (or the sandbox auto-allow mode), not bypass/auto. Note: the `*.secret` deny would also block
the `nwc.mjs < ~/.nwc/...` redirections; in the separate-user layout drop that rule (the agent's own
`~/.nwc/` files are meant for stdin use). In the same-user fallback keep secrets in `~/.config/gl-trial-nwc/`
(0700, outside the repo) and let the operator run the NWC steps.
**Codex CLI** (`~/.codex/config.toml`): `sandbox_mode = "workspace-write"`, `approval_policy = "on-request"`,
`approvals_reviewer = "user"`. Network stays off by default in workspace-write; allow it only for
builds and NWC relay traffic. Codex's sandbox limits writes, not reads, so rely on the separate OS user.
**Cursor** (Agent/CLI): reads root and nested `AGENTS.md` natively. Add `.cursor/sandbox.json` with
`"readBoundary": "workspace"` (Cursor 3.23+), and a `.cursorignore` for `*.pem`, `*.secret`, `*.bkp`,
`trial.env`. `.cursorignore` does not cover terminal or MCP tools.
**Gemini CLI**: `.gemini/settings.json` → `{ "context": { "fileName": ["AGENTS.md"] } }`.
**Aider**: not auto-discovered; `.aider.conf.yml` → `read: [AGENTS.md, docs/greenlight/AGENTS.md, docs/greenlight/SUCCESS-CRITERIA.md]`.
Aider is an edit-focused pair programmer and is a poor fit for supervising a 48-hour trial; prefer an agentic CLI.
**All tools:** no GitHub write token in the session during funded steps (removes the "external
communication" leg of the lethal trifecta while the agent reads untrusted issues, logs and invoices).
