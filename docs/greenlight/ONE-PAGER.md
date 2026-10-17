# Greenlight backend for Alby Hub

**Today, Alby Hub users must choose: run a server, or trust a custodian.**
Greenlight adds a third option: **your keys, no server, and your apps still connect**, with a way out to your own Core Lightning node at any time.

## How it works
- **Blockstream** hosts the always-on Core Lightning node.
- **The user's Hub** runs the local signer (VLS). The keys never leave it.
- **Apps and AI agents** connect to the Hub over Nostr Wallet Connect (NWC).

## The new angle: wallets for AI agents and apps
An agent gets a wallet it can reach over an API, with limits a human sets, and it never holds the user's keys:
- **Budgets per connection, enforced by the Hub.** The agent can't raise its own limit. In testing, 10 parallel payments against one budget couldn't overspend it.
- **Revoke one connection** and the others keep working.
- **Unclear payment, no double spend.** If the node drops mid-payment, the payment stays pending and its budget stays held, so a retry can't spend twice.
- **Bad invoices stop at the Hub.** Wrong-network, expired, no-secret and unknown-feature invoices are rejected before they reach the node.
- **On-demand channels with a fee check.** The Hub refuses an LSP channel if the fee doesn't match the quote (unit-tested; not yet tried live).

Mobile wallets like Phoenix and Breez keep keys on the phone. This gives apps an always-on node with budgets they can't change.

## What Blockstream can and can't see
- **Can see:** channels, amounts, destinations and timing. Oblivious send never shipped.
- **Can't do:** spend. Signing happens only on the user's signer.
- **Users depend on Blockstream's uptime.** The way out is to export to their own CLN node.

## Status
- **Code complete.** 51/51 regtest checks pass, plus 19 adversarial tests, fuzzing with no crashes, and `go test -race`. All 33 LNClient methods are implemented; hold invoices, signmessage and reset-router are unsupported by design. Evidence: https://gist.github.com/welliv/cf1f9b22a3b9047e5a31f6dc4cc2de24#file-evidence-pack-md
- **No live hosted payment yet.** Signet fails with TLS alert 225 (https://github.com/Blockstream/greenlight/issues/750). On testnet3, pay fails with "no response from lightningd" (https://github.com/Blockstream/greenlight/issues/751).
- **Exit:** `convert-backup` to self-hosted CLN works on regtest and keeps the node id. `scheduler export` (keeps channels open) is untested.
- **Signer freeze:** if the signer keeps rejecting the same request, the node's queue can get stuck. Reported in https://github.com/Blockstream/greenlight/issues/752; background in https://github.com/Blockstream/greenlight/issues/739.
- **Not mainnet-ready** until a live hosted payment works.

## Next step: a small mainnet trial and a demo
Fresh seed, capped amount: on-demand channel, receive, pay, keysend, an agent with a budget, restore, export, withdraw. We stop at the first problem and publish the report and a short demo.

## Asks
- **@cdecker (Blockstream):** a look at #750, #751 and #752, one integration contact, support for the capped mainnet trial, and long-term maintenance once it's proven.
- **@rolznz (Alby):** a yes to Greenlight as an opt-in, experimental backend, and review of four PRs (backend, setup/recover UI, LSPS2 on-demand channels, authenticated backups). The branches are ready; the PRs open on your go-ahead.

## welliv's role: the bridge
I'll prove the use case end to end and connect both teams. I won't be the long-term maintainer: once it's proven, Blockstream maintains it and Alby ships it as an opt-in backend.
