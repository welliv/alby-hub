# Pull request drafts (not opened)

These are the descriptions for the four upstream PRs to getAlby/hub. **None has been opened.** Open them only after the mainnet trial evidence is published and @rolznz has said yes in the Alby discussion (see HANDOFF.md). PR 2 and PR 3 are stacked on PR 1; PR 4 is independent.

| PR | Branch → base | Commit |
|---|---|---|
| PR 1 | `pr/1-greenlight-backend` → base `getAlby/hub` `master` (branched from ce1cd798). Open first. | `048c976f` |
| PR 2 | `pr/2-setup-recover-ui` → stacked on PR 1 (open with base `pr/1-greenlight-backend`, or retarget to `master` after PR 1 merges). | `06b98ebf` |
| PR 3 | `pr/3-lsp-jit` → stacked on PR 1 (same as PR 2). | `ad0abab3` |
| PR 4 | `pr/4-backup-aead` → base `getAlby/hub` `master` (ce1cd798). Independent of Greenlight; can be opened any time. | `25ddd28d` |

---

## PR 1: feat: Greenlight lightning backend (hosted CLN + local VLS signer) [experimental]

**Branch:** `pr/1-greenlight-backend` → base `getAlby/hub` `master` (branched from ce1cd798). Open first.

Refs #2522. Adds `LN_BACKEND_TYPE=GREENLIGHT`. Blockstream hosts a Core Lightning node, and the hub machine runs `glcli signer run` (VLS) and keeps the keys.

### What's included
- `lnclient/greenlight`: all 33 LNClient methods. Unsupported by design, with clear errors: hold invoices, `SignMessage` (the Greenlight signer blocks it, see GL#739/#740), and `ResetRouter`. One custom command, `resolve-payment`.
- Payment safety:
  - Local bolt11 validation before Xpay (network, payment secret, required features, expiry, amount).
  - Settle only when `sha256(preimage)==hash`.
  - Ambiguous errors stay pending and are reconciled with ListPays, so budgets are never freed without node evidence.
  - DB-pending hashes are re-armed if `pending_payments.json` is lost.
  - Default gRPC deadlines on every call.
- Incoming: WaitAnyInvoice pump with a persisted index (catches up on payments that arrived while the hub was down), StreamIncoming for keysend TLVs.
- Signer supervisor (`service/gl_signer.go`):
  - Respawns the signer, reaps orphans (pid and cmdline checked), and checks for glcli `--backup-path` support.
  - The seed file exists only while the hub is unlocked.
  - Output sanitizer, so the mnemonic never reaches logs.
  - glcli failures that exit 0 are treated as errors.
- Channel list parity: channels whose outputs are all resolved are hidden; funds from our own force-close count as pending closures.
- Above inbound capacity: a clear error and a `nwc_incoming_liquidity_required` event. JIT channels come in PR 3.
- `scripts/greenlight/migrate-out.sh`: exit to a self-hosted CLN with the same node id (SCB recovery).
- Docs: `docs/GREENLIGHT.md`, `docs/RUNBOOK.md`, `lnclient/greenlight/{README,ARCHITECTURE}.md`.

### Testing
- `go test -race ./...` passes on this branch alone.
- Mutation-checked: each of 12 safety fixes is caught by a failing test when it is reverted.
- Fuzz targets: bolt11 validation, incoming payment decode, creds parse, sanitize, host parsing.
- Regtest gl-testserver matrix (fresh hubs, 51/51 checks; summary in `docs/greenlight/AUDIT-SUMMARY.md`): pay and keysend both ways, NWC budgets and concurrent race, isolated apps, notifications, on-chain, channel open and close, malicious invoices, signer kill (idle and mid-signing), hub hard-kill with a payment in flight, missed payments, degraded boot, backup and restore, migrate-out.

### Not verified / known limits
- Live payment on Blockstream's signet and testnet3 isn't proven: signet returns TLS alert 225, and testnet3 Xpay fails with "no response from lightningd". This has been raised with Blockstream.
- The hub machine must stay online. Availability depends on Blockstream. Requires the glcli binary (CI installs it).

---

## PR 2: feat(frontend): Greenlight setup, recover and status UI

**Branch:** `pr/2-setup-recover-ui` → stacked on PR 1 (open with base `pr/1-greenlight-backend`, or retarget to `master` after PR 1 merges).

Depends on PR 1.
- Greenlight option in the setup wizard (`GreenlightForm`). The user must acknowledge four things: the machine must stay online, it depends on Blockstream, it needs glcli, and there are no hold invoices.
- Recover-from-mnemonic flow (`SetupRecover`, `ImportMnemonic`).
- Sign-message hidden for Greenlight. Stuck-node alert on the backup and status screens. MigrateNode warning.
- `yarn build:http` and `yarn lint` pass on Node 22.23.3; Go tests pass on this branch.

---

## PR 3: feat(greenlight): LSPS2 JIT invoices with opening-fee verification

**Branch:** `pr/3-lsp-jit` → stacked on PR 1 (same as PR 2).

Depends on PR 1. It replaces `jit_disabled.go` with `jit.go`, `lsp_invoice.go` and `jit_settle.go`.
- When an invoice exceeds inbound liquidity, call gl-plugin `LspInvoice` (LSPS2). Remember the advertised `opening_fee_msat`.
- On settle, check `received >= amount - advertised_fee`. If the LSP took more: flag the transaction, log a warning without secrets, raise a node-status alert, and credit only what arrived. `RefuseJitAfterLspViolation` stops further JIT invoices.
- An LSP `expires_at` above u32 is rejected, never truncated.
- Tests: mock-LSP skim, zero fee, refuse-after-violation, decode fuzz. `go test -race ./...` passes on this branch.
- **Not verified live:** no on-demand channel has been opened against Blockstream's LSP yet.

---

## PR 4: fix: authenticated (AES-GCM) backups, and private workdir permissions

**Branch:** `pr/4-backup-aead` → base `getAlby/hub` `master` (ce1cd798). Independent of Greenlight; can be opened any time.

Affects all backends.
1. **Backups:** `.bkp` currently uses AES-OFB without authentication, so a tampered backup imports silently. New backups are versioned AES-256-GCM. Legacy OFB backups still import, so the change is backward compatible. Tampered or truncated GCM backups are refused. Tests: `api/backup_aead_test.go`, `api/backup_tamper_test.go`.
2. **Workdir permissions:** the workdir and `log/` are now 0700 (previously `os.ModePerm`), and the sqlite DB plus `-wal`/`-shm` are 0600. The DB holds the encrypted mnemonic and app keys. Test: `service/workdir_perms_test.go`.

`go test -race ./...` passes on this branch alone.
