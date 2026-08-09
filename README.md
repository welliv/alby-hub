# Alby Hub + Greenlight

**Hosted non-custodial Lightning with Blockstream.** This is a fork of [Alby Hub](https://github.com/getAlby/hub) that adds the **Greenlight** LN backend: a Core Lightning node running on Blockstream's infrastructure with a VLS signer that runs on your Alby Hub. Blockstream hosts the node. Your hub holds the keys.

No disk management, no uptime worry, no manual channel opens. The platform provisions inbound channels automatically. The signer keeps custody even from the host — Blockstream can't spend your sats.

## Why Greenlight fits Alby Hub

- **Same RPC surface as CLN.** Under the hood it's Core Lightning + a remote signer protocol. The backend reuses the standard `cln-grpc` surface — most LN operations are identical to a self-hosted CLN node.
- **Sovereignty without ops burden.** More sovereign than custodial backends (Bark) — you hold the keys via VLS signer. Simpler than running your own CLN node — no disk management, no uptime monitoring.
- **Seed IS the identity.** A BIP-39 mnemonic derives the `hsm_secret` — valid as both a Greenlight node identity and a standalone CLN node identity. No vendor lock-in.
- **Exit strategy is built in.** If Blockstream disappears, the same seed boots a CLN node. The signer writes `backup.json` when channels exist; `glcli signer convert-backup --format cln` produces a CLN-compatible SCB.

## What this fork adds over upstream Alby Hub

| Area | Change |
|---|---|
| **Greenlight backend** | Full LNClient implementation — 30 of 35 methods via CLN gRPC (GetInfo, MakeInvoice, SendPaymentSync, SendKeysend, ListChannels, ConnectPeer, OpenChannel, MakeOffer, etc.) |
| **Signer supervision** | Process supervisor that spawns the VLS signer as a child process with health monitoring, liveness checks, and automatic restart |
| **Health watchdog** | Periodic `Getinfo` with timeout; degraded boot so the hub initialises even if the node is unreachable |
| **Provisioning** | Full registration lifecycle: mnemonic → seed → `glcli scheduler register` → extract PEM credentials → derive node domain → launch signer |
| **WaitAnyInvoice** | Persisted pay_index, backoff on non-advancing paths, raw StreamIncoming decoding without the CLN plugin |
| **Auto channel management** | Platform LSP provisions inbound channels; outbound uses standard ConnectPeer + FundChannel |
| **Frontend** | Greenlight as a first-class backend choice in setup: mnemonic entry, Nobody cert upload, channel display, degraded-state indicators |

## Quick start

**Prerequisites:** a Greenlight account with dev certificates (valid through 2036), a 12-word BIP-39 mnemonic, and the `glcli` tool.

```sh
# 1. Build the frontend and the Hub
cd frontend && yarn install && yarn build:http
cd .. && go build -o hub cmd/http/main.go

# 2. Run the Hub (Greenlight backend is auto-detected from config)
GREENLIGHT_ENABLED=true ./hub

# 3. Complete setup: choose Greenlight at the backend selection screen,
#    provide your Nobody cert, and enter your 12-word recovery phrase.
```

## Backend comparison

| Backend | Node location | Signer location | Channel model |
|---|---|---|---|
| LDK | Embedded | Embedded | JIT (LSPS2) |
| LND | Self-hosted | Self-hosted | Manual |
| CLN | Self-hosted | Self-hosted | Manual |
| Phoenixd | Self-hosted | Self-hosted | Manual |
| Cashu | Remote | Remote (custodial) | None |
| Bark | Remote | Remote (custodial) | None |
| **Greenlight** | **Blockstream cloud** | **Your Alby Hub** | **LSP (automatic)** |

## What's deferred

- Testnet/mainnet payment settlement verified end-to-end
- Frontend setup wizard refinements
- `RedeemOnchainFunds` (stub)
