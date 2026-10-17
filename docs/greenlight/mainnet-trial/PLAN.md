# Greenlight backend for Alby Hub: capped mainnet trial runbook

**Who runs it:** welliv (or whoever picks this up), on a machine they own. Nothing in this kit has touched mainnet yet. No node, seed or credential was created while preparing it.
**What gets tested:** the Alby Hub Greenlight backend at branch `trial/mainnet` @ `15b9ef0b` (= `release/greenlight` @ `960a47de` + 3 small mainnet-safety fixes; see RISKS.md). Branch `feat/greenlight-backend` carries the same code plus these docs, so you can build from either.
**Paths:** commands below run from the repo root (`ALBYHUB=$(pwd)`) unless they start with `<workdir>`. The kit lives in `docs/greenlight/mainnet-trial/` (`KIT=$ALBYHUB/docs/greenlight/mainnet-trial`).
**Most you can lose:** whatever you deposit. The hard cap is **50,000 sats total ever deposited**. Plan for **25,000**.
**Times:** record every timestamp in UTC (`date -u +%FT%TZ`). Evidence goes in `<workdir>/evidence/` and later into EVIDENCE-TEMPLATE.md.

Rule for the whole trial: **at any STOP condition, stop testing, write down what you saw (UTC time, step number, exact error text), and go to section F (abort and recovery).** Never "try once more" with real money.

---

## 0. Preconditions (all must be true before step 1)

| # | Precondition | How to check |
|---|---|---|
| 0.1 | **Machine:** yours, not shared. Disk encryption on, screen lock on, OS updated. It can stay **online and awake for about 48 h** (the local signer must answer the hosted node). | Linux (amd64/arm64, Ubuntu 22.04+ / Debian 12+) or macOS (Intel or Apple silicon). Windows: use WSL2 Ubuntu and follow the Linux path inside it; native Windows is not covered. You always build from source (0.5). |
| 0.2 | **No other Alby Hub or glcli signer** is running on this machine, and none ever runs against the trial node at the same time as the trial hub. | `pgrep -fa "glcli.*signer run"` prints nothing. |
| 0.3 | **Developer certificate** from the Greenlight Developer Console (`client.crt`, `client-key.pem`) is in a private folder, outside any git repo. | `ls -l` shows your user only. Never paste, print or screenshot `client-key.pem`. |
| 0.4 | **glcli built from the tested commit.** crates.io only has gl-cli 0.1.1, which lacks `signer run --backup-path`, and the hub refuses it. | `cargo install --locked --git https://github.com/Blockstream/greenlight --rev 15b22cf0f9b07bd0d343133d593c92b78addc403 gl-cli` then `glcli signer run --help \| grep -- --backup-path`. Record `sha256sum $(command -v glcli)`. This is the commit the regtest/testnet work used. Use `origin/main` only if Greenlight rejects this signer as too old, and note it in the report. |
| 0.5 | **Hub binary: build it yourself from this repo.** No prebuilt binary is published. | `git clone https://github.com/welliv/alby-hub && cd alby-hub && git checkout trial/mainnet && git log -1 --format=%H` → `15b9ef0b6204bc0e83880084e0d25e41499630fe`. (Or check out `feat/greenlight-backend`; then `git diff --quiet 15b9ef0b HEAD -- . ':(exclude)docs/greenlight' && echo same-code` must print `same-code`.) Then `docs/greenlight/mainnet-trial/build.sh . ../hub-bin` (needs Go ≥ 1.26.9, a C compiler, Node ≥ 22.12, yarn 1). `build.sh` refuses a dirty tree and writes `BUILDINFO.txt` and `SHA256SUMS`. Checksums are not bit-reproducible across machines; the commit hash is what you verify. |
| 0.6 | **A second Lightning wallet you control** on mainnet, holding about 30,000 sats. It must pay invoices and create invoices. Keysend support is a bonus. | You can create an invoice in it and see its balance. |
| 0.7 | **An on-chain address you control** for the final withdrawal, from a wallet other than the hub. | Write it down. Check that it starts with `bc1`. |
| 0.8 | **Paper and pen** for the new 12-word recovery phrase. It's a new seed: never reuse a testnet phrase, and never type it into chat, email or screenshots. | — |
| 0.9 | **Clock is synced** (invoices expire). | `timedatectl` or the macOS date settings show NTP on. |
| 0.10 | You have read CHECKLIST.md and section F. | — |

## Budget

| Item | Sats | Source |
|---|---|---|
| LSP minimum payment for a JIT channel | ~2,501 | Seen live on Greenlight **testnet** (2026-10); mainnet unknown until step 3.1 |
| LSP opening fee | ~2,500 | Testnet observation. Mainnet unknown. The hub refuses quotes above **5,000** (`GREENLIGHT_JIT_MAX_FEE_SAT`) |
| Test spending (pays, keysend, NWC) | ~3,000 | Steps 4–7. Mostly returns to your second wallet |
| Channel reserve (locked until close) | ~1% of channel size | CLN default |
| On-chain withdraw fee | 300–3,000 | Depends on mempool fee rate at step 11 |

- **Exact minimum that still exercises every step:** first receive ≥ opening fee + 4,000. That's about **6,500 sats** at the testnet fee, or **9,000** at the 5,000-sat cap. Add the withdraw fee and you get about **10,000 sats**.
- **Recommended:** first receive **20,000**, second receive **2,000**, so **22,000** deposited in total.
- **Hard cap: 50,000 total ever deposited.** Never top up beyond it, even if a step fails.

---

## 1. Setup (fresh seed, mainnet, loopback only)

**1.1 Create the workdir**
```
$KIT/setup.sh --bin-dir ../hub-bin --cert-dir ~/private/gl-devcert \
           --glcli "$(command -v glcli)" --workdir ~/albyhub-greenlight-mainnet-trial
# rehearse first with --dry-run; it changes nothing
```
- **Expected:** "binaries verified", glcli version, and "done". The workdir is 0700, `trial.env` 0600, and the cert is copied into `devcert/` with mode 0600. It refuses if the workdir already exists.
- **Evidence:** the terminal output. It contains your home path, so redact it.
- **STOP if** it refuses for any reason other than a typo. Don't override checks.

**1.2 Start and check**
`~/albyhub-greenlight-mainnet-trial/run.sh` (leave this terminal open), then in another terminal `~/albyhub-greenlight-mainnet-trial/check.sh`.
- **Expected:** `listen 127.0.0.1:8029 OK`, `permissions OK`, `CHECK PASSED`.
- **STOP if** anything listens on a non-loopback address.

**1.3 Hub setup in the browser** (http://127.0.0.1:8029)
Pick Greenlight, tick both acknowledgements, and set a new strong unlock password (store it in your password manager). The hub generates a new 12-word phrase: write it on paper and keep it offline.
- **Expected:** the startup states "Provisioning Greenlight", then "Starting Greenlight signer", then "Connecting to Greenlight", then the dashboard with 0 balance.
- **Evidence:** UTC time of registration; the node public key (Settings → node info), which is a throwaway node, so it's fine to publish; a dashboard screenshot.
- **STOP if** registration fails (for example "Failed to register", "invite code", "unauthenticated", or "certificate"). Copy the error text, redacting paths. No funds are involved yet. This is the answer to "does the dev cert work on mainnet". Report it.

**1.4 Pre-funding health**
Settings → Node status (or `GET /api/node/status` from the UI's network tab): node healthy, `signer.running: true`.
Peers page: note every connected peer's node id. The LSP is one of them; LSP node ids are public.
- **STOP if** the node isn't healthy after 10 minutes, or the signer keeps restarting (the `last_error` changes every ~15 s).

**1.5 Backup before funding**
Settings → Backup: export the encrypted `.bkp` file to a second private location (an encrypted USB stick or a separate folder). You need it in step 10.

---

## 2. Rules during funded steps
- Keep the `run.sh` terminal open and the machine awake.
- One action at a time. Wait for each result before the next.
- Never pay the same invoice twice. If a payment looks stuck, go to F1.
- Save each piece of evidence as you go: `<workdir>/evidence/NN-step.txt`, with UTC times.

## 3. Receive via JIT channel, with fee check

**3.1 Quote (no money moves).** Wallet → Receive → **20,000 sats** → create the invoice. **Don't pay it yet.**
In the log (`<workdir>/log/nwc.log`), find the line `greenlight JIT invoice via LspInvoice` and its `opening_fee_msat`. The transaction view also shows the fee.
- **Expected:** a JIT invoice whose fee is ≤ 5,000 sats (otherwise the hub refuses with "above the configured maximum").
- **Your decision rule:** continue only if the fee is **≤ 5,000 sats and ≤ 25% of the amount**.
- **Evidence:** the fee, the amount, the UTC time, the payment hash, and the LSP node id (from the invoice route hint or the Peers page).
- **STOP if** you see "Could not find an LSP peer", "No opening params", or the hub's fee refusal. Without an LSP the trial can't fund cheaply. Don't open a manual channel instead: big nodes require channels far above the budget. Report it.

**3.2 Pay the invoice** from your second wallet.
- **Expected:** paid within about 1 minute. A new channel appears on the Channels page (it may say pending or zero-conf at first). The transaction shows about 20,000 − fee received. Node status shows **no** `lsp_opening_fee_exceeded` alert.
- **Evidence:** channel capacity and local/remote balance; the funding txid (public); received amount vs. advertised fee; UTC times when paid and when it showed in the hub.
- **STOP if** the payer shows paid but the hub shows nothing after 10 minutes (go to F1, receive side); if the alert `lsp_opening_fee_exceeded` appears (go to F6); or if the channel never appears.

**3.3 Wait for the funding transaction to get ≥ 1 confirmation** (check the txid on a block explorer) before step 11. Steps 4–9 can run meanwhile.

## 4. Outbound payments
Create invoices in the second wallet for **1,000**, **500** and **100** sats. Pay each from the hub (Wallet → Send), one at a time.
- **Expected:** success with a preimage. Routing fee ≤ max(5 sats, 1%), the node's xpay default (the hub reserves max(10 sats, 1%)).
- **Evidence:** for each payment: payment hash, fee, seconds to complete, and whether it worked. This success rate is the key number for #751.
- **STOP if** you get `no response from lightningd`, any other error, or a payment still pending after 2 minutes. Go to F1 and don't pay that invoice again. A clear failure ("no route") isn't a STOP: record it and try the next one.

## 5. Second receive (existing channel, no new JIT)
Check the Channels page: inbound (receiving) capacity must be ≥ 4,000 sats. Then receive **2,000 sats** from the second wallet.
- **Expected:** a regular invoice with **no** opening fee, which settles.
- **STOP if** the hub quotes a JIT fee. That means inbound was too small. Let the invoice expire and skip this step.

## 6. Keysend (optional; needs a keysend-capable node you control; do it after 7.1)
The hub UI has no keysend form, so this uses NWC. Create a second connection `trial-keysend`: budget **1,000 sats**, permission "pay keysend" only, secret saved to `<workdir>/evidence/nwc-keysend.secret` (`chmod 600`). Then run:
`HUB_SRC=$ALBYHUB node $KIT/tools/nwc.mjs payKeysend '{"amount":500000,"pubkey":"<destination node id>"}' < <workdir>/evidence/nwc-keysend.secret` (the amount is in msat, so this is 500 sats). Delete the connection afterwards.
- **Expected:** `ok: true` and the destination sees 500 sats. **Evidence:** amount, fee, UTC time. If you have no keysend target, mark it N/A.
- **STOP if** it's pending more than 2 minutes (go to F1).

## 7. App connection (NWC) with a small budget, overspend attempt, revoke
**7.1** Apps → Add connection, named `trial-agent`. Budget **1,000 sats**, renewal monthly. Permissions: pay invoice, get balance, get info, make invoice, lookup invoice, list transactions. Copy the connection secret into `<workdir>/evidence/nwc.secret` (`chmod 600`). **Never** paste it in chat or a screenshot.

**7.2** `HUB_SRC=$ALBYHUB node $KIT/tools/nwc.mjs getInfo '{}' < <workdir>/evidence/nwc.secret` and `getBudget`.
- **Expected:** network `bitcoin` and a budget of 1,000.

**7.3** Two in-budget payments: create two **300-sat** invoices in the second wallet, then run `HUB_SRC=$ALBYHUB node $KIT/tools/nwc.mjs payInvoice '{"invoice":"lnbc..."}' < …secret` for each.
- **Expected:** both succeed. Apps shows about 600 + fees used.

**7.4** Overspend attempt: create a **400-sat** invoice and pay it the same way.
- **Expected:** refused with `QUOTA_EXCEEDED`, because 600 used + 400 + a 10-sat fee reserve is over 1,000. The second wallet must **not** receive it.
- **STOP (critical) if** it succeeds or the second wallet receives anything beyond budget. Then stop all tests and go straight to step 11.

**7.5** Revoke: Apps → `trial-agent` → Delete. Run `getInfo` again with the same secret.
- **Expected:** an error or no answer; the old secret no longer works. The hub UI still works.
- **Evidence:** the JSON outputs. They're already free of secrets, but still run them through `tools/redact.py`. Add an Apps screenshot showing budget used.
- **STOP if** the revoked secret still works.

## 8. Pending or unclear payment (only if it happens naturally)
If any payment shows **pending**, see F1. Record whether the reconciler resolved it on its own (it checks every 20 s; a payment with no record on the node is failed after 10 min), whether you needed `resolve-payment`, and whether the final state matched what the other wallet saw. This is valuable evidence. Never create one on purpose with real funds.

## 9. Restart
**9.1** Ctrl-C in the `run.sh` terminal and wait 60 s. Check `pgrep -fa "glcli.*signer run"`: it should be empty, because the hub stops its signer.
**9.2** Run `run.sh` again and unlock.
- **Expected:** same node pubkey, same balances and channel. Signer running. One more 100-sat outbound payment works.
- **STOP if** the pubkey or balance differs, or the node isn't healthy after 10 minutes.

**9.3 (optional) Receive while the hub is stopped.** Stop the hub and pay a **500-sat** invoice that the hub created before stopping. Record what the payer sees. Start the hub again and record whether the payment settled (and when, UTC) or failed back to the payer.
- **STOP if** a channel closes.

## 10. Backup and restore (second workdir, same machine)
**10.1** Stop hub A (Ctrl-C) and confirm no signer is running.
**10.2** `$KIT/setup.sh --bin-dir … --cert-dir … --glcli … --workdir ~/albyhub-greenlight-mainnet-restore --port 8030`, then run its `run.sh`. In the UI, choose to import an existing wallet: enter the **12-word phrase** and choose Greenlight. The hub then runs register, which fails because the node exists, and falls back to recover.
- **Expected:** the **same node pubkey** as hub A. The channel and Lightning balance are visible. A 100-sat payment works.
- **Alternative:** restore the `.bkp` from step 1.5 with the unlock password. Do it only if the phrase route fails, and record which route worked.
- **STOP if** the pubkey differs. A mistyped phrase registers a **new, empty** node: stop B, then re-check the phrase.
**10.3** From now on use **hub B only**. Retire A so it can't start by accident: `rm ~/albyhub-greenlight-mainnet-trial/run.sh && mv ~/albyhub-greenlight-mainnet-trial ~/albyhub-greenlight-mainnet-trial.RETIRED`. Greenlight's docs allow several signers, but the two hub databases would diverge.

## 11. Exit: get every sat out
**11.1** The funding tx needs ≥ 3 confirmations.
**11.2** Pay most of the Lightning balance back to the second wallet over Lightning, leaving about **5,000 sats** in the channel so the on-chain path still gets tested. If those payments fail, skip to 11.3 anyway.
**11.3** Channels → the channel → **Close** (cooperative).
- **Expected:** a closing txid. After 1–3 blocks the on-chain balance shows your channel balance. On a mutual close the channel opener (the LSP) normally pays the closing fee; record whether that held.
- **Evidence:** the closing txid (public) and the UTC time.
- **STOP if** the close fails or nothing appears on-chain within 2 h (go to F5).
**11.4** Wallet → On-chain → Withdraw, **all** to your address from 0.7, with a fee rate from mempool.space's "low priority" level.
- **Expected:** a txid. Hub on-chain balance 0. Your external wallet receives the amount after confirmation.
- **Evidence:** txid, amount, fee rate.
**11.5** Final state: hub Lightning 0 (any unclosed reserve counts as 0 here), on-chain 0, no pending transactions. Second wallet and external address balances reconcile with your notes: deposits − fees = withdrawals. **Evidence:** a reconciliation table (EVIDENCE-TEMPLATE §5).

## 12. Optional: Greenlight `scheduler export` of the EMPTY node
Only after 11.5 shows **0 everywhere**, including pending. It's **one-way**: the node can never be scheduled on Greenlight again. There's also no published tool to decrypt the export, so this only proves that ExportNode works on mainnet.
Stop hub B, then run `glcli -n bitcoin -d ~/albyhub-greenlight-mainnet-restore/greenlight scheduler export` and answer `y`.
- **Expected:** "Node export initiated successfully!" plus a download URL. Download it; record its size and sha256. **Don't publish the URL**; it's a private link.
- **STOP if** any balance isn't 0. Don't run it.

**Not in this trial:** `convert-backup` + `recoverchannel` into self-hosted CLN. On mainnet it needs your own full bitcoind and the LSP to force-close the channel by hand, and the export download can't be decrypted with public tools. It's proven on regtest (matrix 51/51, migrate-out.sh) and stays the last-resort exit in F4.

## 13. Wrap-up
- Archive the restore workdir encrypted, and keep the paper phrase until the withdrawal has 6 confirmations. Keep the phrase afterwards too: it controls the node identity.
- Fill EVIDENCE-TEMPLATE.md. Run every pasted log through `python3 $KIT/tools/redact.py < in > out`, then read the output yourself before publishing.

---

## F. Abort and recovery procedures

**Order for getting money out (use the first one that works):** (1) pay it out over Lightning to the second wallet; (2) cooperative close, then on-chain withdraw (11.3–11.4); (3) force close, wait the CSV delay, then withdraw; (4) if Greenlight is gone for good: migrate-out (F4).

**F1. Stuck or unclear payment** (pending, `no response from lightningd`, timeout)
1. Don't pay that invoice again, and don't pay a new invoice to the same recipient for the same thing.
2. Note the payment hash and the UTC time.
3. Wait 10 minutes. The hub keeps the budget held and its reconciler checks the node every 20 s.
4. If it's still pending: Settings → Debug tools → run `resolve-payment --payment_hash=<64-hex>`. It only settles or fails on definitive node evidence.
5. Ask the recipient wallet whether it was paid. Record both views.
6. If it's still unclear after 1 hour: stop sending, keep the hub running, and report on #751 with the hash and time. Funds are either with the recipient or come back when the HTLC times out, at most about 2 weeks for long CLTVs and usually much sooner. Your seed stays in control throughout.

**F2. Node freeze** (status unhealthy "possible frozen node", every call times out; Blockstream/greenlight#739 and #752)
1. Stop all actions. Don't restart the hub in a loop. One restart (step 9) is fine.
2. Capture the node pubkey, the UTC time, the last action before the freeze, and the hub status JSON (redacted).
3. Ask Blockstream to restart the node: a comment on #752 with the node id and UTC time, or greenlight@blockstream.com. Only Blockstream can restart a hosted node.
4. Funds stay in the channel and are protected by your keys: nobody can spend them without your signer. If the LSP force-closes because of a stuck HTLC, the funds go to the node's on-chain wallet after the delay, and you withdraw them once the node is back (11.4).
5. If Blockstream can't revive the node within days, use F4.

**F3. Signer crash** (`signer.running: false`, `last_error` set)
1. The supervisor respawns it within about 15 s. Wait 1 minute.
2. If it keeps failing: check the glcli path and version (`glcli signer run --help | grep backup-path`), that the workdir is still 0700 (`check.sh`), and the disk space. Restart the hub once.
3. If it still fails: stop the hub and restore on a new workdir from the phrase (step 10). Funds are safe while the signer is down, because nothing can be signed. HTLCs that time out while it's down can make the LSP force-close (see F5).

**F4. Blockstream outage** (scheduler or node unreachable, TLS alert 225 like signet #750, hub "degraded")
1. Don't panic: your seed controls the funds. Leave the hub running; it keeps probing.
2. Record the UTC start and end and the exact error.
3. Short outage: carry on when it's back.
4. Long outage with a channel open: the LSP may force-close. The funds go to the node's on-chain wallet, which you withdraw when the node is back.
5. **Permanent loss of Greenlight** (last resort): `$ALBYHUB/scripts/greenlight/migrate-out.sh` converts the signer's `backup.json` (in `<workdir>/greenlight/` and inside the `.bkp`) into a CLN SCB, and starts your own Core Lightning with an `hsm_secret` derived from the phrase. Then ask the LSP to **force-close**, and the funds arrive in your CLN wallet. Requirements: your own mainnet bitcoind. Never run it while any Greenlight signer for this node is running. See `docs/RUNBOOK.md` and `docs/GREENLIGHT.md`.

**F5. LSP won't close cooperatively**
Channels → the channel → **Force close**. Your balance comes back on-chain after the channel's `to_self_delay` (often 144–2016 blocks, roughly 1 day to 2 weeks). It then appears in the hub's on-chain balance; withdraw with 11.4. Keep the hub available during the delay, because the node needs its signer to sweep.

**F6. LSP fee violation alert** (`lsp_opening_fee_exceeded`)
The hub has already credited only what arrived, and with `GREENLIGHT_REFUSE_JIT_AFTER_LSP_VIOLATION=true` it won't create more JIT invoices. Record the advertised fee, the actual fee, and the LSP node id. You may continue steps 4–9 with the existing channel, or go to 11.

**F7. Machine lost or disk dead**
On another machine: set up 0.4–0.5, run `setup.sh` with a new workdir, and restore from the phrase (step 10). The hosted node and its channels continue. Make sure the old machine can't come back online with a running signer.

**F8. Anything else unexpected** (balance mismatch, unknown alert, UI error): stop, capture the evidence, and go to step 11. A balance that differs from your notes by more than routing fees is a **critical** finding.
