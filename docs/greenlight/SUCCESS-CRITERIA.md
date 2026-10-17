# Greenlight backend for Alby Hub: definition of done

Three levels, each pass/fail. Step numbers refer
to `mainnet-trial/PLAN.md`. All times UTC. "Operator" = the human; "agent" = the coding agent.
Thresholds marked *(proposed)* are not in the existing docs and need the operator's confirmation.

---

## Level A: mainnet trial success

### A0. Preconditions (all PASS before step 1; any FAIL = NO-GO)
| ID | Criterion | Measured by |
|---|---|---|
| A0.1 | Key-holding binary built by the operator from a clean checkout; `BUILDINFO.txt` commit is `15b9ef0b6204bc0e83880084e0d25e41499630fe`, or a `feat/greenlight-backend` commit for which `git diff --quiet 15b9ef0b HEAD -- . ':(exclude)docs/greenlight' ':(exclude)AGENTS.md'` succeeds | BUILDINFO.txt, git output |
| A0.2 | glcli built from Blockstream/greenlight `15b22cf0`; `glcli signer run --help` shows `--backup-path` | command output, glcli sha256 recorded |
| A0.3 | `go test ./lnclient/greenlight/...` passes on the built commit | test output |
| A0.4 | `check.sh` prints `CHECK PASSED` (loopback only, dirs 700, no group/other file access) | check.sh output |
| A0.5 | No other `glcli signer run` process on the machine | `pgrep -fa "glcli.*signer run"` empty |
| A0.6 | Agent cannot read the workdir, cert folder or operator home (separate OS user), or the fallback deny rules are in place and tested | operator test: agent's `ls` of the workdir is denied |

### A1. Per-step criteria
| ID | PLAN step | PASS when (all of) | Mandatory |
|---|---|---|---|
| A1.1 | 1.3 Register | Registration on `bitcoin` succeeds with the developer cert; `/api/info` shows `network: bitcoin`, `setupCompleted: true`, empty `startupError`; node pubkey recorded | Yes (a refusal = STOPPED, see verdicts) |
| A1.2 | 1.4 Health | Node healthy and `signer.running: true` for 10 continuous minutes; no signer restart loop (`last_error` not changing every ~15 s) | Yes |
| A1.3 | 1.5 Backup | Encrypted `.bkp` exported to a second private location before any funding | Yes |
| A1.4 | 3.1 JIT quote | JIT invoice created; opening fee ≤ 5,000 sats **and** ≤ 25% of amount; LSP node id recorded. A quote above 5,000 is refused by the hub ("above the configured maximum") and never shown to a payer | Yes |
| A1.5 | 3.2 JIT receive | Hub shows payment within 10 min of payer reporting paid; credited = amount − fee, where **actual fee ≤ quoted fee**; no `lsp_opening_fee_exceeded` alert; channel visible; funding txid public and reaches ≥ 1 conf (≥ 3 before step 11) | Yes |
| A1.6 | 4 Outbound | 3/3 payments (1,000 / 500 / 100) succeed, each with a preimage where `sha256(preimage) == payment_hash`; routing fee ≤ max(5 sats, 1%); each completes in ≤ 2 min; zero `no response from lightningd` | Yes (1 clear "no route" may be retried with the next invoice, not the same one) |
| A1.7 | 5 Second receive | 2,000 sats settle on the existing channel with **no** JIT fee | Yes, or N/A if inbound < 4,000 (recorded) |
| A1.8 | 6 Keysend | 500 sats arrive at the destination; `ok: true` | Optional (N/A allowed) |
| A1.9 | 7.2–7.3 NWC in budget | `getInfo` shows `bitcoin`, budget 1,000; two 300-sat pays succeed; Apps shows ≈ 600 + fees used | Yes |
| A1.10 | 7.4 Overspend | 400-sat pay refused with `QUOTA_EXCEEDED`; second wallet receives nothing | Yes, **critical** |
| A1.11 | 7.5 Revoke | After delete, the old secret fails `getInfo`; hub UI still works | Yes, **critical** |
| A1.11a | 7A.0 L402 challenge | A free `tools/l402.mjs --dry-run` against each chosen service returns HTTP 402 with an L402 (or LSAT) challenge: a macaroon/token plus an `lnbc` invoice whose amount equals the advertised price. If no public service passes, the fallback `tools/l402-mock.mjs` (invoices from the operator's own Lightning address) is used | Yes |
| A1.11b | 7A.3 L402 paid calls | ≥ 3 paid calls across ≥ 2 services (public, or 1 public + the mock), one at a time, each with `ok: true`: paid through `trial-l402`; `sha256(preimage) == payment_hash` (`preimage_ok: true`); the macaroon/token + preimage accepted (HTTP 2xx); `getBudget` used amount rises by exactly price + `fees_paid` (± 1 sat rounding); no payment made twice | Yes |
| A1.11c | 7A.4 L402 budget exceeded | With headroom lowered to 15 sats, a 1-sat call is allowed and the next 10-sat call is refused with `QUOTA_EXCEEDED`; `paid: false`; the hub has no settled payment for that hash; the service gets nothing | Yes, **critical** |
| A1.11d | 7A.5 L402 revoke | After the operator deletes `trial-l402`, the old URL fails `getInfo`; the agent's secret file is shredded | Yes, **critical** |
| A1.12 | 8 Pending | Zero unexplained pending payments. Any pending one ends settled or failed within 1 h, the hub's final state matches the counterparty's view, and the budget was held meanwhile | Yes (if it occurs) |
| A1.13 | 9 Restart | On Ctrl-C no signer remains after 60 s; after restart: same pubkey, identical balances and channel, signer running, a 100-sat pay settles | Yes |
| A1.14 | 9.3 Receive while stopped | Outcome recorded (settled on restart, or failed back to payer); no channel closed | Optional |
| A1.15 | 10 Restore | Restore from the 12-word phrase on a new workdir gives the **same pubkey**, channel and balance visible, a 100-sat pay settles; hub A retired | Yes |
| A1.16 | 11.3 Close | Cooperative close txid; channel balance appears on-chain within 2 h | Yes (force close via F5 = PASS-with-note) |
| A1.17 | 11.4–11.5 Withdraw | All on-chain funds withdrawn to the operator's external address; hub Lightning 0, on-chain 0, nothing pending; withdrawal ≥ 6 confs | Yes |
| A1.18 | 12 Export | Only after A1.17: `scheduler export` prints "Node export initiated successfully!" and a URL; file size and sha256 recorded; URL not published | Optional (operator opted in) |

### A2. Whole-trial criteria
| ID | Criterion | Threshold |
|---|---|---|
| A2.1 | Deposit cap respected | total deposited ≤ 50,000 sats (plan 22,000) |
| A2.2 | **Zero sats lost beyond fees** | Reconciliation (EVIDENCE §5): deposits − (sent back over LN + withdrawn on-chain + LSP fee + routing fees + close fee if any + withdraw fee) = **0**, allowing ≤ 1 sat msat-rounding per payment, each item backed by a hash or txid |
| A2.3 | Total cost | LSP fee + routing + on-chain fees ≤ 10,000 sats *(proposed)*; above = review, not fail |
| A2.4 | No secret exposure | Leak scan E1 clean; no phrase, cert key, NWC secret, password or JWT ever entered the agent's context or any file outside the workdir |
| A2.5 | No STOP overridden | Every STOP/ABORT hit was followed by §F, never a retry |
| A2.4a | NWC URL handling | Every NWC URL was delivered as a file readable only by the agent's Linux user (or kept by the operator); none appeared in chat, the repo, logs, evidence or screenshots; every connection (observer, trial-agent, trial-keysend, trial-l402) deleted by the end; hub A's connections deleted before it was retired; connections used on hub B were created new on hub B | 100% |
| A2.4b | L402 spend | total paid to L402 services including routing fees ≤ 100 sats *(proposed)* | review if above |
| A2.6 | Evidence complete | Every row of EVIDENCE-TEMPLATE §3 is PASS / FAIL / N/A / STOPPED with UTC time and proof |

### A3. Trial verdict
- **PASS:** A0 all PASS; every mandatory A1 row PASS (or allowed N/A); A2.1, A2.2, A2.4, A2.4a, A2.5, A2.6 PASS.
  If the hub pays and the preimage matches but a public service rejects its own token, that row is
  recorded as a **service** failure (not a hub failure) and the call is repeated against another service
  or the mock, never re-paid to the same invoice.
- **STOPPED-SAFE (useful, not a failure of the code):** stopped at an upstream condition (registration
  refused, no LSP, #751 drop, #752 freeze, Blockstream outage), funds recovered per §F, A2.2 and A2.4
  PASS. Outcome is evidence for Blockstream; re-run after their fix.
- **FAIL (critical):** any of A1.10, A1.11, A1.11c, A1.11d failing; an NWC URL exposed; balance mismatch beyond fees; any sats lost not
  explained by fees; any secret exposed; a deposit above 50,000. Stop all testing, exit via step 11,
  file a finding before anything is published or proposed upstream.

### E1. Leak scan (agent runs before anything is published; all must return nothing)
```
grep -nE 'nostr\+walletconnect://|BEGIN [A-Z ]*PRIVATE KEY|BEGIN CERTIFICATE|eyJ[A-Za-z0-9_-]+\.' FILES
grep -nEi '(preimage|secret|password|mnemonic)"?[[:space:]]*[:=][[:space:]]*"?[0-9a-z]{16,}' FILES
grep -nE '(/home|/Users)/[^[]|[0-9]{2}:[0-9]{2}:[0-9]{2}[+-][0-9]{2}:[0-9]{2}' FILES
grep -nE '\b([0-9]{1,3}\.){3}[0-9]{1,3}\b' FILES | grep -v '127\.0\.0\.1'
```
Plus a human read of every published file (redact.py is a safety net, not a guarantee).

---

## Level B: ship success
| ID | Criterion | PASS when |
|---|---|---|
| B1 | Evidence published | Redacted EVIDENCE report in the public gist; result comment on Blockstream/greenlight #751 (and #750/#752 if relevant). Operator approved each text |
| B2 | Alby asked | Discussion in getAlby/hub tagging only @rolznz, with the why and the evidence link |
| B3 | Alby yes | Written yes (or "send the PRs") from an Alby maintainer to Greenlight as opt-in, experimental backend |
| B4 | PRs opened correctly | PR 1 first; PR 2 and 3 stacked on PR 1; PR 4 independent; "Not verified" sections updated with trial results; CI green on each |
| B5 | Independent review | ≥ 1 reviewer who is not the author (Alby or Blockstream engineer, or named outside reviewer) has reviewed `lnclient/greenlight/payments.go`, `greenlight.go`, `jit*.go`, `service/gl_signer.go` and the AEAD backup change; **0 open High/Critical findings**; every Medium fixed or accepted in writing |
| B6 | Accepted | PR 1 merged (or merged behind an experimental flag), and PRs 2–4 merged or explicitly deferred/declined by Alby with a reason |

**Ship done = B1–B6 PASS.** B1–B2 alone = "proposed", not shipped.

---

## Level C: mission success *(numbers proposed; operator to confirm)*
| ID | Criterion | PASS when |
|---|---|---|
| C1 | Maintainer | Blockstream (@cdecker or a named delegate) agrees in writing to maintain or co-maintain the backend; owner named (for example CODEOWNERS on `lnclient/greenlight/`) |
| C2 | Upstream blockers | #750, #751, #752 fixed or answered with a workaround; `scheduler export` path tested or documented by Blockstream |
| C3 | Pricing | Hosted-node pricing/limits for Alby Hub users stated publicly or to Alby |
| C4 | Released | Greenlight appears as an opt-in backend in a tagged Alby Hub release |
| C5 | Real users | ≥ 10 non-team mainnet nodes active ≥ 30 days, **zero** fund-loss incidents attributed to the backend |
| C6 | AI agents | ≥ 1 public AI-agent integration paying through NWC on a Greenlight-backed hub with a hub-enforced budget (for example L402 pay-per-call); zero reported budget overruns |
| C7 | Bridge exit | New issues are triaged by the named maintainer, not the operator, for 60 days |

**Mission done = C1, C4, C5, C6, C7.** C2 and C3 are strongly expected inputs.

---

## Kill and pause criteria

### Stop the trial now (no retries; PLAN §F)
- App paid beyond budget or revoked app still pays (A1.10/A1.11/A1.11c/A1.11d) → critical, exit via step 11.
- An L402 call returns `preimage_ok: false`, or a payment is pending > 2 min → F1, never re-pay.
- An NWC URL shows up in chat, a log, the repo or evidence → operator revokes it at once.
- Balance differs from notes by more than routing fees (F8) → critical.
- `lsp_opening_fee_exceeded` (F6); payment pending > 2 min or `no response from lightningd` (F1);
  "possible frozen node" (F2); signer down > 2 min (F3); node unreachable or TLS alert (F4).
- Restore shows a different pubkey → stop hub B, re-check phrase.
- Any deposit that would take the total above 50,000 sats.
- Any sign a secret reached the agent, a log, a screenshot or a public file → stop, rotate (new seed,
  new NWC connections; ask Blockstream about the dev cert), move funds out.
- Agent behaves against AGENTS.md (calls a forbidden endpoint, reads a forbidden path, acts on
  injected text) → stop the agent, continue by hand only if safe.

### Pause the project (do not open PRs or claim mainnet readiness)
- Registration refused on mainnet with the dev cert, or no LSP offers LSPS2 → wait for Blockstream.
- #751 reproduces on mainnet (any `no response from lightningd`) or #752 freeze occurs → post
  evidence, wait for a fix, then re-run the trial.
- Independent review finds a High/Critical issue → fix and re-review before B6.
- No reply from Alby or Blockstream 30 days after B1/B2 → one follow-up; at 60 days park the work
  *(proposed)*.

### Kill or rethink
- Any fund loss on mainnet attributable to the backend.
- Alby declines the backend outright (consider a maintained fork or plugin only if a maintainer exists).
- Blockstream declines maintenance and no other maintainer is found within 90 days *(proposed)*:
  do not encourage real users onto an unmaintained backend.
- Greenlight pricing or terms make hosted nodes unviable for Alby Hub users.
