# Greenlight — hosted non-custodial Lightning for Alby Hub

This branch adds the **Greenlight** backend: a Core Lightning node running on Blockstream's cloud, with a VLS signer on your Alby Hub. Blockstream hosts the node — your hub holds the keys.

## Why it matters

Running your own Lightning node is ops-heavy: disk management, uptime, manual channel opens. Custodial backends give up your keys. Greenlight is the middle ground — nobody can spend your sats, but you don't have to run a server.

The signer runs as a child process on your hub. When Blockstream's node needs a signature, it asks your signer. Your seed IS your node identity — if Blockstream disappears tomorrow, the same seed boots a standalone CLN node. No vendor lock-in.

## What this branch adds

- **Full LNClient backend** — 30 of 35 methods via CLN gRPC: payments, invoices, keysend, offers, channel management
- **Signer supervision** — spawns and monitors the VLS signer, automatic restart on failure
- **Health watchdog** — periodic Getinfo checks, degraded boot so the hub stays usable even if the node is offline
- **Provisioning** — registration, credential extraction, node domain derivation, all from a 12-word phrase
- **Channels** — inbound from Blockstream's LSP, outbound via standard ConnectPeer/FundChannel
- **Frontend** — Greenlight as a setup option: mnemonic entry, cert upload, channel status

## Quick start

```sh
cd frontend && yarn install && yarn build:http
go build -o hub cmd/http/main.go
GREENLIGHT_ENABLED=true ./hub
```

Choose Greenlight at setup, provide your Nobody cert, enter your 12-word phrase.
