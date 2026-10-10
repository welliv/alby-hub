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

| Works | Does not |
|---|---|
| Pay / receive bolt11, keysend | Hold invoices |
| Channel open / close / peers | `sign_message` (blocked by Greenlight signer) |
| NWC apps, budgets, isolated wallets | Oblivious send (not shipped upstream) |
| Hub backup / restore, convert-backup migrate-out | Reset local gossip router |

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
