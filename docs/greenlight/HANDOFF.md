# Greenlight backend for Alby Hub: handoff

**Start here.** This file is the single entry point for anyone picking up this work. It says why it exists, where it stands, what was built, where the code is, and the exact ordered steps left to call it done.

## 1. Why (what we are solving)
Today an Alby Hub user must choose: **run a server, or trust a custodian.** This backend adds a third option:
- **Your keys.** The signer (VLS, via `glcli signer run`) runs on the user's machine. Blockstream hosts the node but cannot spend.
- **No server.** Blockstream's Greenlight hosts the always-on Core Lightning node.
- **Your apps still connect.** Everything in Alby Hub keeps working, including Nostr Wallet Connect (NWC).
- **A way out.** Leave for your own Core Lightning node at any time (`scripts/greenlight/migrate-out.sh`; Greenlight's `scheduler export` is still untested).
- **AI agents and apps get budgeted, revocable access.** Each NWC connection has a hub-enforced budget the app or agent cannot raise, can be revoked on its own, can't double-spend on an unclear payment (it stays pending with the budget held), and bad invoices are stopped before they reach the node.

Honest trade-offs: Blockstream can see payment metadata (channels, amounts, destinations, timing; oblivious send never shipped), and users depend on Blockstream's uptime. See [ONE-PAGER.md](ONE-PAGER.md).

## 2. Current status (2026-10-17, UTC)
- **Code complete.** All 33 LNClient methods; hold invoices, `signmessage` and `ResetRouter` are unsupported by design.
- **Tested:** 51/51 regtest checks on Blockstream's open-source test stack, 19 adversarial tests, fuzzing (no crashes), `go test -race ./...`, mutation checks. See [AUDIT-SUMMARY.md](AUDIT-SUMMARY.md).
- **Blocked on Blockstream for a live hosted payment:**
  - Signet: nodes can't be scheduled (TLS alert 225), [Blockstream/greenlight#750](https://github.com/Blockstream/greenlight/issues/750).
  - Testnet3: pay fails with "no response from lightningd", [#751](https://github.com/Blockstream/greenlight/issues/751).
- **Signer freeze** (a request rejected every time can wedge the hosted node): reported as hardening in [#752](https://github.com/Blockstream/greenlight/issues/752); background in [#739](https://github.com/Blockstream/greenlight/issues/739).
- **`scheduler export` untested** (one-way; no public decrypt tool). `convert-backup` exit works on regtest.
- **Not mainnet-ready for real users** until the capped mainnet trial below passes. No replies yet from Blockstream or Alby on #739/#750–#752.

## 3. What was built
- `lnclient/greenlight/`: the backend (payments with local invoice validation, pending/reconcile, preimage checks; incoming pump with a persisted cursor; keysend stream; LSPS2 JIT invoices with opening-fee checks; health/readiness; creds parsing in Go; output sanitizer). Docs: `README.md`, `ARCHITECTURE.md` there.
- `service/gl_signer.go`: signer supervisor (respawn, orphan reaping, glcli compatibility check, seed file only while unlocked, sanitized logs).
- Frontend: Greenlight setup with acknowledgements, recover-from-mnemonic, status alerts, sign-message hidden.
- `scripts/greenlight/migrate-out.sh` + `seed`: exit to self-hosted CLN with the same node id.
- Upstream-wide: authenticated (AES-GCM) backups and private workdir permissions.
- Mainnet safety (trial branch): fail closed on unknown network, JIT fee cap, refuse JIT after an LSP fee violation.
- Docs: `docs/GREENLIGHT.md`, `docs/RUNBOOK.md`, and this folder.

## 4. Branch map (welliv/alby-hub)
| Branch | Commit | What it is |
|---|---|---|
| `feat/greenlight-backend` | this branch | `trial/mainnet` + `docs/greenlight/` (this handoff). Code outside `docs/greenlight/` is identical to `trial/mainnet`, apart from a 3-line pointer at the top of the root `AGENTS.md` (a doc file). |
| `trial/mainnet` | `15b9ef0b` | `release/greenlight` + 3 mainnet-safety fixes. **Build this for the trial.** |
| `release/greenlight` | `960a47de` | The integrated, fully tested release branch (backend + UI + JIT + AEAD backups + perms). |
| `pr/1-greenlight-backend` | `048c976f` | PR 1: backend without JIT (base getAlby/hub `master` ce1cd798). |
| `pr/2-setup-recover-ui` | `06b98ebf` | PR 2: setup, recover and status UI. Stacked on PR 1. |
| `pr/3-lsp-jit` | `ad0abab3` | PR 3: LSPS2 JIT invoices with fee verification. Stacked on PR 1. |
| `pr/4-backup-aead` | `25ddd28d` | PR 4: AES-GCM backups + workdir perms. Independent (base ce1cd798). |

The `pr/*` branches leave `.github/workflows/` identical to upstream ce1cd798; the fork-only CI guards (Docker build limited to `master`/`v*` tags, macOS signing only for `getAlby`) live only on `feat/greenlight-backend`, `trial/mainnet` and `release/greenlight`. The fork's "Multiplatform Docker build & push" workflow is disabled, because the fork cannot push images to ghcr.io.

The PR descriptions are in [PR-DESCRIPTIONS.md](PR-DESCRIPTIONS.md). Note: the trial kit sent earlier as file parts referred to `27fb8c90` / `48d36024`; those are the same trees as `15b9ef0b` / `960a47de` (only commit timestamps' zone offsets were normalised to UTC before publishing). Check with `git rev-parse 15b9ef0b^{tree}`.

## 5. What remains, in order (definition of done)
0. **If a coding agent helps:** it reads [AGENTS.md](AGENTS.md) first (binding rules: it never sees the phrase, cert key or NWC URLs, and never moves money without your GO); paste [START-PROMPT.md](START-PROMPT.md) as its first prompt; set up isolation with [TOOL-CONFIGS.md](TOOL-CONFIGS.md). What "done" means at each level is in [SUCCESS-CRITERIA.md](SUCCESS-CRITERIA.md).
1. **Run the capped mainnet trial.** Follow [mainnet-trial/README.md](mainnet-trial/README.md), [PLAN.md](mainnet-trial/PLAN.md) and [CHECKLIST.md](mainnet-trial/CHECKLIST.md): fresh seed, ≤ 25k sats planned, 50k hard cap, stop at the first problem. Build from this repo; run on a machine you own; never paste a seed, certificate key or NWC secret anywhere.
   - Includes step 7A: an agent buys L402 pay-per-call API calls through a pay-only NWC connection with a hub-enforced budget (public L402 services, or the `tools/l402-mock.mjs` fallback).
   - NWC connection URLs are spending secrets: delivered as a file only the agent's Linux user can read, never in chat, the repo, logs or evidence; revoked at the end of each step and of the trial; created new on the restored hub.
   - Done when: every step is PASS / N/A / STOPPED with a reason, and all funds are withdrawn and reconciled (pass/fail detail: [SUCCESS-CRITERIA.md](SUCCESS-CRITERIA.md) level A).
2. **Publish the evidence.** Fill [EVIDENCE-TEMPLATE.md](mainnet-trial/EVIDENCE-TEMPLATE.md) (UTC only, logs through `tools/redact.py`, read before publishing). Add it to the public gist and comment on #751 (and #750/#752 if relevant) with the results. Optional: a 2-minute demo video of a real mainnet payment and an agent hitting its budget.
3. **Post the Alby discussion** in getAlby/hub, tagging **only @rolznz**: the why, the evidence link, and the ask for a yes to Greenlight as an opt-in, experimental backend.
4. **Open the 4 PRs** to getAlby/hub after that yes: PR 1 first; PR 2 and PR 3 stacked on PR 1; PR 4 independent. Use [PR-DESCRIPTIONS.md](PR-DESCRIPTIONS.md); update the "Not verified" sections with the trial results.
5. **Independent review.** Ask the Alby and Blockstream engineers (or another reviewer) to review the payment-safety paths first: `lnclient/greenlight/payments.go`, `greenlight.go` (validation, pending/reconcile), `jit*.go`, `service/gl_signer.go`, and the AEAD backup change. Fix what they find.
6. **Blockstream maintenance handover.** Agree with Blockstream (@cdecker) on who maintains the backend long-term, plus pricing for hosted nodes, a fix or answer on #750/#751/#752, and testing `scheduler export`.

## 6. Roles
- **welliv: the bridge, not the long-term maintainer.** welliv proves the use case end to end and connects both teams. Once it is proven, Blockstream maintains the backend and Alby ships it as an opt-in backend.
- **Blockstream:** hosted nodes, fixes for #750/#751/#752, long-term maintenance.
- **Alby (@rolznz):** review and ship as opt-in, experimental.

## 7. Files in this folder
| File | Purpose |
|---|---|
| HANDOFF.md | This file |
| AGENTS.md | Binding rules for a coding agent working on this (Claude Code reads it through `CLAUDE.md`) |
| CLAUDE.md | One-line import of AGENTS.md for Claude Code |
| START-PROMPT.md | The first prompt to paste into a local coding agent |
| TOOL-CONFIGS.md | Separate OS user, NWC secret delivery, log bridge, per-tool settings (Claude Code, Codex, Cursor, Gemini, Aider) |
| SUCCESS-CRITERIA.md | Pass/fail definition of done: trial, ship, mission; kill and pause criteria |
| ONE-PAGER.md | The pitch: why, the agent/app angle, what Blockstream can and can't see, status, asks |
| AUDIT-SUMMARY.md | Findings, fixes and tests; the 9 corrections; known limits |
| PR-DESCRIPTIONS.md | Drafts for the 4 upstream PRs (none opened) |
| mainnet-trial/ | The trial kit: plan (including step 7A, L402), checklist, risks, evidence template, setup/build scripts, tools (`nwc.mjs`, `l402.mjs`, `l402-mock.mjs`, `redact.py`) |

## 8. Quick verification from a fresh clone
```bash
git clone https://github.com/welliv/alby-hub && cd alby-hub && git checkout feat/greenlight-backend
git diff --quiet 15b9ef0b HEAD -- . ':(exclude)docs/greenlight' ':(exclude)AGENTS.md' && echo same-code-as-trial
go test ./lnclient/greenlight/...    # backend unit, adversarial and fuzz-seed tests
```
