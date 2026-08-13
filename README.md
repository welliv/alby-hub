# Greenlight backend for Alby Hub

A Core Lightning node running on Blockstream's cloud, with the keys and signer on your Alby Hub.

Blockstream runs the node.
Your Hub holds the keys.

## Why this exists

Running your own Lightning node is operationally heavy: disk, uptime, watchtowers, updates.
Custodial services take your keys.

Greenlight sits in the middle:

- **Keys stay local** (like LDK)
- **Node process runs in the cloud** (Blockstream)
- **Channels are real Core Lightning channels** (unlike Bark)

You get real Lightning self-custody without becoming a node operator.

## How it fits with existing backends

| Backend | Keys | Node process | Channels | Operational burden |
|---|---|---|---|---|
| LDK (default) | Local | Inside Hub | Real | Medium |
| Remote CLN/LND | External | Your own server | Real | High |
| Bark | Local | None (Ark) | Abstracted | Very low |
| **Greenlight** | **Local** | **Blockstream** | **Real** | **Low** |

Greenlight is a gentle extension of the existing CLN path.
It is still Core Lightning. The difference is that Blockstream runs the process and your Hub only runs the signer.

## User flow

Greenlight is an **advanced** option only. Get Started stays stock LDK.

1. Advanced setup
2. Create wallet with custom node
3. Create unlock password
4. Choose **Greenlight**
5. Generate or import a 12-word recovery phrase

After that the experience is normal Alby Hub:

- Unlock: node becomes ready
- Receive, send, NWC, sub-wallets, lightning address all work
- Lock: signer stops and seed material is removed from disk
- On a new device, the same 12 words restore the identical node

The user never runs `glcli` or manages certificates in the normal path.

## Core design rules

- One 12-word mnemonic only. It is both the Greenlight seed and the Hub recovery phrase.
- Mnemonic is encrypted at rest with the unlock password.
- `hsm_secret` exists only while unlocked.
- Hub starts and stops the supervised signer.
- `isReady` requires both the hosted node and the local signer to be healthy.
- Recover from the phrase alone restores the same node ID and channels.

## What is implemented

- Product-path ownership of keys and signer
- Supervised signer lifecycle (start on unlock, stop and shred on lock)
- Recover-from-phrase
- Invoices, payments API, NWC surface
- `LspInvoice` JIT path when inbound is short and an LSP peer is present
- Honest status reporting (signer down → not ready)
- Real channel management (hybrid: easy capacity plus full manual control)

## Current status (testnet)

Proven:

- Unlock → `isReady`
- Lock / unlock cycle
- Recover same node from phrase
- Invoices + NWC
- Live `LspInvoice` fee path
- Signer-down correctly reports not ready

Still open:

- Settled third-party receive + send (blocked by public testnet liquidity)

## Relationship to the existing CLN backend

This is not a replacement for remote CLN.
It is the same Core Lightning, with the operational burden moved to Blockstream while the Hub keeps full control of the keys and recovery phrase.

If Blockstream disappeared tomorrow, the same seed can boot a standalone CLN node. There is no vendor lock-in on the critical path.

## Development

Branch: `feat/greenlight-backend`

```sh
cd frontend && yarn install && yarn build:http
cd .. && go build -o hub cmd/http/main.go
./hub
```

Do not set `LN_BACKEND_TYPE`. Get Started stays LDK.
Greenlight appears under Advanced setup → Create wallet with custom node.
