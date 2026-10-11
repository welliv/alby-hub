# Greenlight backend — user notes

Alby Hub can use [Blockstream Greenlight](https://blockstream.com/lightning/greenlight/)
as its Lightning backend (`LN_BACKEND_TYPE=GREENLIGHT`). The node runs in
Blockstream's cloud; your Hub machine runs the VLS signer and holds the keys.

**Status:** experimental. Prefer LDK for production wallets until a successful
public-network pilot is documented.

## Requirements

- `glcli` **0.1.3+** on `PATH`, or set `GREENLIGHT_GLCLI_PATH`
- This machine must stay **online and unlocked** while you send or receive
- A Greenlight developer certificate (or invite code) for first registration

## What works / what doesn't

| Works (regtest verified) | Does not / unverified on hosted |
|---|---|
| Pay / receive bolt11, keysend (regtest E2E) | Hold invoices (no plugins on hosted nodes) |
| Channel open / close / peers | `sign_message` (blocked by Greenlight signer, GL#739) |
| NWC apps, budgets, isolated wallets | Oblivious send (not shipped upstream) |
| Hub backup / restore, convert-backup migrate-out | Hosted pay/receive/JIT (see blockers root-cause) |
| | Reset local gossip router |

### Network notes (docs disagree — be careful)

- **gl-cli README / CLI help:** supported networks are `bitcoin` and `signet`; "we do not support `testnet` at the moment."
- **Official docs Register page:** "chose between the following networks: `testnet` `bitcoin`."
- **Root greenlight README (stale):** lists `bitcoin`, `testnet`, `regtest` and says signet is planned.
- In practice (2026-10): **testnet3** register/getinfo/invoice work with a developer cert; **hosted pay** currently fails (`no response from lightningd`). **Signet** register/schedule return a URI but node connect gets TLS alert **225** (SchedulerError).

## Stuck or frozen node

Symptoms: `/api/node/status` shows `isReady: false`, payments hang, health
watchdog logs "possible frozen node".

1. Confirm the **local signer** is running (`signer.running: true`). If not,
   the hub respawns it within ~15s; check `glcli` and file permissions under
   `$WORK_DIR/greenlight/`.
2. If the signer is fine but the node is unhealthy for several minutes, the
   **hosted node** may need a Blockstream restart. Open a Greenlight support
   ticket; reference [greenlight#739](https://github.com/Blockstream/greenlight/issues/739)
   only if you suspect an hsmd wedge from an unsupported signing request
   (the Hub never calls `signmessage`).
3. Meanwhile the Hub stays usable in degraded mode: apps, backups, and
   settings work; new Lightning payments will fail until the node returns.
4. Escape hatch if Greenlight is permanently lost: Hub `.bkp` includes
   `backup.json` — convert with
   `glcli signer convert-backup --path backup.json --format cln`
   and recover on a self-hosted CLN (**only** after the Greenlight node is
   confirmed gone — see `docs/RUNBOOK.md`).

Full operator detail: [`docs/RUNBOOK.md`](./RUNBOOK.md).
