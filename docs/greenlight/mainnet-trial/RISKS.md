# Mainnet-specific code risks (review of `release/greenlight` @ 960a47de)

## Fixed on branch `trial/mainnet` @ 15b9ef0b (one commit on top of `release/greenlight`)
| # | Risk | Fix | Test |
|---|---|---|---|
| 1 | An unknown `NETWORK` label (for example the typo `mainet`, or `testnet4`) silently fell back to **signet**. The hub would register or sign on a network the user didn't ask for. | `greenlightNetwork()` in service/start.go returns an error for unknown labels. `testnet3` now maps to `testnet`. | `TestGreenlightNetwork` |
| 2 | The LSPS2 opening fee had **no ceiling**. gl-plugin picks the first fee offer from the first LSP (peers sorted by node id), not the cheapest, and the hub only checked the settle against that same quote. A fee ≥ the amount would leave the user with 0. | jit.go `checkJitQuote`: a JIT invoice is refused if fee ≥ amount, and also if the fee is above the optional `GREENLIGHT_JIT_MAX_FEE_SAT` (setup.sh sets 5,000). A refused invoice is never shown to a payer. | `TestCheckJitQuote`, `TestMakeInvoice_JitRefusedWhenFeeAboveCap`, `TestMakeInvoice_JitRefusedWhenFeeNotBelowAmount` |
| 3 | `RefuseJitAfterLspViolation` existed but was never wired, so it was always off. | New env `GREENLIGHT_REFUSE_JIT_AFTER_LSP_VIOLATION` (setup.sh sets `true`). | Existing `TestJitSettle_RefuseAfterViolation` covers the behaviour |

The full `go test -race ./...` passed on this commit when the kit was prepared (23 packages ok, 0 failures). `go vet` is clean on the changed packages.

## Checked and fine
- **Invoice network check:** `validateBolt11` uses MainNetParams for `bitcoin`, so `lntb…`/`lnbcrt…` invoices are rejected on mainnet.
- **Outbound fee limit:** the hub passes no `maxfee` to xpay, so CLN's default applies: max(5 sats, 1%) (docs.corelightning.org/reference/xpay). That's within the hub's fee reserve of max(10 sats, 1%), so budgets hold. The upstream CLN backend behaves the same way. Keysend uses CLN defaults (0.5% plus a 5-sat exemption).
- **Withdraw fee-rate units:** sat/vB × 1000 → `perkb`, bounds-checked. This is identical to the upstream CLN backend.
- **Node URI:** derived as `gl1<bech32m id>.nodes.gl.blckstrm.com`. That's what the scheduler returned on testnet and signet. The docs' node-domain page says `.gl.blckstrm.com`, which is a docs inconsistency, not seen live.
- **Dev/regtest hooks** (`GREENLIGHT_XPAY_PREDELAY_MS`, the scheduler node-URI override) only act when the network is `regtest`.
- **Loopback binding:** `PORT=127.0.0.1:N` binds loopback only. Verified with setup.sh's self-test: not reachable from the LAN address.
- **Hardcoded LSPs:** the Greenlight backend has none. gl-plugin asks the node's *connected peers* for LSPS2 offers. Alby's LSPS1 LSP list (Megalith/Olympus) is a separate LDK-oriented flow; the Greenlight UI points to `/channels` instead.

## Not fixed (report only)
| Risk | Why not fixed here | Mitigation in the kit |
|---|---|---|
| The hub forwards its **entire environment** to glcli, so a leftover `GL_SCHEDULER_GRPC_URI` or `GL_CA_CRT` from regtest work would point registration at another scheduler. | Touches signer spawn and provisioning. Better done in a reviewed PR (strip `GL_SCHEDULER_*`/`GL_CA_CRT` unless the network is regtest). | `run.sh` starts the hub with `env -i` plus `trial.env` only, and setup.sh warns about stray variables. |
| Upstream default: with `NETWORK` unset, the hub uses `bitcoin`. A Greenlight tester who forgets `NETWORK=testnet` registers on mainnet. | Upstream Alby Hub behaviour, shared by all backends. | setup.sh always writes `NETWORK` explicitly. |
| **No invite-code support.** The hub requires `GL_NOBODY_CRT/KEY` (a developer cert) to register. glcli supports `--invite-code`. | Feature, not a safety fix. | The trial uses a developer cert. |
| **JIT channels are zero-conf.** Until the funding tx confirms, received funds depend on the LSP not double-spending (an LSPS2 property, not a hub bug). | Inherent. | PLAN 3.3: wait for confirmations before treating funds as final; small amounts. |
| A frontend banner says "Inbound channels are provisioned automatically by the platform". That's only true on receive, and only if an LSP peer is connected. | Copy change; belongs in the UI PR. | PLAN 1.4 and 3.1 check for an LSP peer first. |
| glcli prints failures but exits 0. | Upstream glcli. The hub already parses the output (`glcliReportedFailure`). | — |
| No way to decrypt a `scheduler export` with public tools. | Upstream. | Export only an empty node (PLAN 12). |
| A signer request rejected deterministically can wedge the hosted node (#752). Blockstream also added `FORGET_CHANNEL` to its "stuck HSM" types on 2026-10-14 (`1499d7d`), a related stall after channel close in background-sync sessions. | Upstream (gl-plugin). | The hub pre-validates invoices and has no sign-message; PLAN F2 covers it. |
