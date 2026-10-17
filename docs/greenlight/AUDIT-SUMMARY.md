# Audit summary: Greenlight backend for Alby Hub

A concise, public summary of the security and quality audits of this backend (October 2026), the fixes, and the tests that cover them. It lists only weaknesses that are fixed in this branch or already public in Blockstream/greenlight issues. Detailed internal notes and raw logs are kept out of this repo on purpose.

**Scope:** `lnclient/greenlight`, `service/gl_signer.go`, the Greenlight parts of `service/` and `api/`, the setup/recover UI, and two upstream-wide patches (backups, workdir permissions).
**Code:** `release/greenlight` @ `960a47de` (base getAlby/hub `master` @ ce1cd798) and `trial/mainnet` @ `15b9ef0b`. Greenlight `main` @ `15b22cf0`; glcli 0.1.3 built from source; lightningd v26.06gl1 (Blockstream gl-testing).
**Rounds:** initial audit, fix-and-harden re-audit, live testnet3/signet attempts, local regtest bench (Blockstream `gl-testserver`), adversarial round, an independent re-verification from a fresh clone and bench, a final regression, and a mainnet-specific review.

## 1. Findings and fixes

| # | Finding | Fix | Test / evidence |
|---|---|---|---|
| 1 | An invoice the signer rejects could leave a payment stuck and the hosted node unresponsive (same class as Blockstream/greenlight #739). | Invoices are validated locally before any pay call (network, signature, payment secret, required features, expiry, amount). Every unary gRPC call has a deadline (30 s; 3 min for pay/keysend). | `TestValidateBolt11`, `FuzzValidateBolt11`, `TestDeadlineInterceptorAppliesContextDeadline`; regtest `malicious-*`, `node-healthy-after-malicious` |
| 2 | With an unresponsive node, startup and `/api/info` could hang, locking the user out of the UI and backups. | Startup `GetInfo` bounded to 15 s, `/api/info` to 5 s; the hub boots degraded and recovers. | regtest `degraded-boot`, `degraded-recovers` |
| 3 | Any pay error was treated as failure, so a payment that succeeded in transit could free the app budget (overspend risk). | `ErrPaymentPending`: ambiguous errors stay pending with the budget held; a reconciler checks the node (`ListPays`) and only settles or fails on node evidence. Tracked set persisted; DB pending hashes re-armed if the file is lost; admin `resolve-payment`. | payments tests; regtest `hardkill-inflight-settled`, `budget-race` |
| 4 | Mnemonic redaction regex never matched, so glcli output could leak a seed to logs. | Fixed regex; `sanitizeGlcliOutput` drops seed/mnemonic/private-key lines and any run of 12+ BIP39 words on every glcli output path and the signer log tail. | `TestAuditMnemonicRedaction`, `FuzzSanitize`; regtest `no-mnemonic-in-logs` |
| 5 | `credentials.gfs` was written 0644. | chmod 0600; data dir hardened to 0700/0600. | `perms` check; unit tests |
| 6 | crates.io gl-cli 0.1.1 lacks `--backup-path`, so the backend failed to start. | `checkGlcliCompat` feature-detects and gives a clear "install gl-cli >= 0.1.3" error. | unit test with fake old/new glcli |
| 7 | The incoming-invoice cursor was saved before publishing, so a failed lookup could drop a receipt. | Publish first, then persist; lookup retried, then falls back to stream fields. | unit tests; regtest `missed-payment-recorded` |
| 8 | Keysends received while the hub was down could be missed. | Cursor replay covers keysend invoices; handling is idempotent by hash. | regtest missed-keysend check |
| 9 | Amounts below the LSP minimum fell back to an unpayable invoice; JIT accounting could over-credit isolated sub-wallets. | Clear "below LSP minimum" error; JIT credits what actually arrived and reports the skim as a fee (LDK parity). | jit tests |
| 10 | `isReady` and the watchdog disagreed. | One readiness rule; status adds `ready`, `syncing`, `not_ready_reason`. | health tests |
| 11 | PID-file fallback could signal an unrelated process after PID reuse. | Only the `exec.Cmd` handle is used; orphans are reaped only if their cmdline is our glcli and data dir. | unit test; regtest `orphan-signer-reaped` |
| 12 | `ResetRouter` failure tolerance during backup applied to all backends. | Tolerated only on Greenlight; OAuth-token removal stays fatal. | code review |
| 13 | Credentials were extracted with a Python script found on `PATH`. | Ported to Go (`creds.go`); no Python dependency. | `creds_test.go`, `FuzzParseDeviceCreds` |
| 14 | Docs said the backup "includes the seed". | Docs corrected; `hsm_secret` exists only while the hub is unlocked and is shredded on stop. | regtest seed-shred check |
| 15 | Toolchain had reachable vulnerabilities; fee subtraction could underflow; msat casts unchecked. | Go 1.26.9, x/net and grpc bumped (govulncheck: 0 reachable); `feeOf` guard; bounded casts. | govulncheck, gosec, staticcheck |
| 16 | Malformed pubkeys or hashes passed to the node could make it misbehave (DoS of the user's own node). | Every pubkey and hash is validated locally before any node call. | unit tests; regtest `graph-malformed` |
| 17 | A hostile node reporting success with a wrong preimage would be believed. | Settle only when `sha256(preimage) == payment_hash` (pay, keysend, reconcile). | `TestAdversarialNodeLiesAboutPaymentSuccess` |
| 18 | Unclear client errors for bad descriptions; negative channel amounts could wrap. | Clear client errors; amounts bounds-checked. | adversarial tests |
| 19 | Closed channels lingered as "offline", and funds from our own force-close were missing from pending balances. | Hidden once all outputs resolve; own unilateral close counted as pending. | `TestListChannelsHidesResolvedClosedChannels`, `TestOnchainBalanceCountsOwnUnilateralCloseAwaitingSweep` |
| 20 | glcli prints failures but exits 0. | Output parsed: failures become errors. | `TestGlcliReportedFailure` |
| 21 | An LSP `expires_at` above u32 was truncated. | Rejected instead. | `TestDecodeLspInvoiceResponseRejectsOversizedExpiry` |
| 22 | Hub backups (all backends) are encrypted without authentication, so tampering isn't detected. | **PR 4:** versioned AES-256-GCM; legacy backups still import; tampered or truncated ones are refused. | `api/backup_aead_test.go`, `api/backup_tamper_test.go`; regtest `tampered-backup-refused` |
| 23 | Workdir and DB permissions were too open (all backends). | **PR 4:** workdir and `log/` 0700, sqlite DB and `-wal`/`-shm` 0600. | `TestHardenWorkdirPerms` |
| 24 | Mainnet review: unknown `NETWORK` labels silently fell back to signet; LSPS2 opening fee had no ceiling; `RefuseJitAfterLspViolation` was never wired. | **trial/mainnet:** fail closed on unknown networks; refuse JIT quotes with fee ≥ amount or above `GREENLIGHT_JIT_MAX_FEE_SAT`; new env `GREENLIGHT_REFUSE_JIT_AFTER_LSP_VIOLATION`. | `TestGreenlightNetwork`, `TestCheckJitQuote`, `TestMakeInvoice_JitRefusedWhen*` |

## 2. Corrections from the independent re-verification

A second pass re-ran everything from a fresh clone and a fresh regtest bench. It confirmed most claims and corrected these nine. They are listed here because the earlier reports overstated them.

1. **Timeout fix had no effective test.** The original test only checked constants, so reverting the fix didn't fail it. A real test was added (`TestDeadlineInterceptorAppliesContextDeadline`).
2. **"Signer killed mid-payment" was mis-designed.** The test delay ran *before* the pay call, so the signer was killed before signing. A genuine mid-signing kill (freeze the signer, issue the pay, then kill it) was then run and the payment settled.
3. **"Tamper-proof backups" was only true on a side branch.** It is now merged into `release/greenlight` and proposed upstream as PR 4.
4. **`migrate-out.sh` only printed steps.** It is now `scripts/greenlight/migrate-out.sh` and runs end to end on regtest.
5. **Greenlight's official `scheduler export` was not covered.** It is untestable on `gl-testserver` and one-way on a real node, so it remains **untested**.
6. **The gl-plugin signer wedge was overstated as a novel critical.** Unanswered requests are replayed to reconnecting signers, so only requests rejected every time wedge the node, and Blockstream's code and #739 already describe it. Reported as a hardening suggestion in Blockstream/greenlight #752.
7. **"Custom commands not supported" was outdated.** `resolve-payment` exists.
8. **"Behaves like LDK" missed closed-channel handling.** Fixed (finding 19).
9. **Malicious-invoice rejection was attributed wholly to the backend.** Wrong-network and expired invoices are rejected first by upstream hub code; the backend's own checks cover missing payment secret and unknown features (plus defense in depth).

## 3. Results
- **Regtest matrix:** 51/51 checks pass on fresh hubs against Blockstream's open-source test stack (pay and keysend both ways, NWC budgets and a concurrent race, isolated apps, notifications, on-chain, channel open and close, malicious invoices, signer kill idle and mid-signing, hub hard-kill with a payment in flight, missed payments, degraded boot, backup and restore, migrate-out).
- **Adversarial Go suite:** 19 pass. **Mutation check:** each of 12 safety fixes is caught by a failing test when reverted.
- **Fuzzing:** 6 targets × 300 s (earlier rounds 10 min × 5), no crashes.
- **Static/deps:** `go test -race ./...` passes on `release/greenlight`, `trial/mainnet` and each PR branch alone; govulncheck 0 reachable; staticcheck style-only; frontend production deps 0 advisories.

## 4. Known limits (not fixed here)
- **No live hosted payment yet.** Signet nodes can't be scheduled (TLS alert 225, Blockstream/greenlight #750). On testnet3, pay fails with "no response from lightningd" after the signer approves (#751). Root causes need Blockstream's logs.
- **Signer wedge:** a request the signer rejects every time can block the hosted node's hsmd queue until Blockstream restarts it (#752; background #739). The hub pre-validates inputs and never forwards `signmessage`.
- **Privacy:** Blockstream (the operator) can see channels, balances, peers, invoices, payment destinations, amounts and timing. Oblivious send is not shipped in Greenlight and not used here. Blockstream cannot spend: signing happens only on the user's signer.
- **Exit:** `convert-backup` + `recoverchannel` to self-hosted CLN works on regtest (same node id; needs the peer to force-close). `scheduler export` is untested.
- **JIT/LSPS2** channel opening and fee checks are tested against a mock only.
- **No independent external review yet.** All audits so far were done by the author's side.
