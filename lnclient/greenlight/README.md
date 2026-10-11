# Greenlight backend for Alby Hub

A Core Lightning node on Blockstream's infrastructure. A VLS signer on your
Alby Hub. The two communicate over mTLS — Blockstream runs the node, your hub
holds the keys.

## Why Greenlight for Alby Hub?

Greenlight is the only backend where the node lives in the cloud but the keys
stay local. Every other backend bundles both together:

| | Node | Keys |
|---|---|---|
| LND | Self-hosted | Self-hosted |
| LDK | Embedded | Embedded |
| CLN | Self-hosted | Self-hosted |
| Bark | Cloud | Cloud (custodial) |
| **Greenlight** | **Cloud** | **Local** |

You get CLN's full feature set (bolt12 offers, keysend, plugins) without disk
management, uptime worry, or channel operations. Channels are provisioned
automatically by Blockstream's LSP. If the platform disappears tomorrow, the
same 32-byte seed that runs your signer boots a standalone CLN node — no
vendor lock-in.

## Setup (two paths)

**Product path** — the hub wizard registers a new node from a 12-word mnemonic,
extracts credentials, and starts the signer under supervision.

**Preconfigured path** — point the hub at existing credentials and a node URI:

```sh
LN_BACKEND_TYPE=GREENLIGHT
GREENLIGHT_CREDS_PATH=/path/to/greenlight-creds
GREENLIGHT_NODE_URI=gl1<node_id>.nodes.gl.blckstrm.com:443
```

## Requirements

- `glcli` **>= 0.1.3** built from https://github.com/Blockstream/greenlight
  (`cargo install --git https://github.com/Blockstream/greenlight gl-cli --locked`).
  The hub checks for `signer run --backup-path` at signer start and refuses
  an older glcli with a clear error. crates.io `gl-cli 0.1.1` is too old.
- No Python: device credentials are extracted in Go (`creds.go`).

## Fund-safety behaviour

- Every invoice is validated in Go before it reaches the node/signer
  (network, signature, payment secret, var_onion, unknown required feature
  bits, expiry, amount). A malformed invoice once froze a live testnet node
  when the VLS signer rejected it, so such invoices are never forwarded.
- Every gRPC call has a deadline (30 s default, 3 min for pay/keysend).
- Pay/keysend errors are classified: only a node-side failure with no
  pending/complete attempt marks a payment failed. Transport errors,
  timeouts and anything else return `lnclient.ErrPaymentPending`. The hub
  keeps the payment pending (budget stays reserved) and the reconciler
  (`payments.go`) polls `ListPays` until it is final, then emits
  `nwc_lnclient_payment_sent` / `_failed`. On startup the last 7 days of node
  payments are replayed (this is idempotent in the hub).
- Keysends are labelled so they can be reconciled even though CLN derives
  its own preimage. The hub remaps the recorded hash.
- The incoming invoice cursor is persisted only after publish, so a replay
  is harmless and a skip is impossible. Keysends recorded as invoices
  without bolt11 are still credited.
- JIT (LSPS2) receives credit what the node actually received; the LSP skim
  is reported as the fee, as on LDK. Amounts above inbound liquidity that
  the LSP refuses (for example below its minimum) fail with a clear error
  instead of producing an unpayable invoice.
- Secrets: the mnemonic is stored encrypted in the hub config DB (same as
  LDK). `hsm_secret` is written only while the hub is unlocked and is
  shredded on lock or stop. All glcli output is sanitized. The data dir is
  forced to 0700 and its files to 0600.

## Parity with LDK and inherent gaps

| Area | LDK | Greenlight |
|---|---|---|
| Pay / keysend result | sync, plus events | sync, plus reconciler events (regtest verified; **hosted pay currently broken** — see blockers root-cause) |
| Payment received / sent notifications | yes | yes |
| Hold invoices | yes | **no**: hosted nodes run no plugins |
| sign_message | yes | **no**: intentionally blocked by the Greenlight signer (GL#739, by design); older node stacks wedged on it |
| Channel ready/closed notifications | yes | **no**: GL leaves Subscribe* unimplemented |
| Open channel manually | yes | yes (`FundChannel`/`Close`; peer must be reachable from Blockstream infra) |
| ResetRouter | yes | not applicable (the node owns the graph) |
| Node availability | local | depends on Blockstream; a frozen node needs their restart |
| Backup | seed + VSS | mnemonic in encrypted DB; `backup.json` SCB once channels exist |

## Backups and exit strategy

The signer writes `backup.json` when channels exist. The hub `.bkp` includes
it. If Blockstream's cloud goes away: `hsm_secret` → CLN node identity,
`backup.json` → `glcli signer convert-backup --format cln` → SCB →
`lightning-cli recoverchannel`. Everything needed to self-host is already on
your disk.

[ARCHITECTURE.md](ARCHITECTURE.md) covers the integration design.
[docs/RUNBOOK.md](docs/RUNBOOK.md) covers operations and troubleshooting.


## Channel management

Manual channel open/close use the hosted node's `FundChannel` / `Close`
RPCs (same as the CLN backend). Verified on the local `gl-testserver`
regtest bench. Signet/testnet require the peer to be reachable from
Blockstream's infrastructure (local `127.0.0.1` peers will not work on
hosted nodes).
