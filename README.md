# Routstr — self-hosted AI gateway on Lightning, for Alby Hub

This branch adds **Routstr**: an OpenAI-compatible API that gives you one key, every model, and pay-per-request pricing in sats. The Hub supervises Routstr — your sats, your gateway, no subscriptions.

## Why it matters

AI APIs are centralized, expensive, and require accounts. Routstr flips this: it discovers model providers over Nostr, routes to the cheapest one, and charges in sats through your Hub. No credit card, no signup, no vendor lock-in. Point any client at your Hub and it just works.

The Hub is the wallet and controller. Sats move through the Routstr app wallet (never the main wallet), auto top-up keeps the balance healthy, and backups cover the daemon databases.

## What this branch adds

- **AI gateway UI** — connection page, setup wizard, API key management
- **Daemon supervision** — Hub starts, monitors, and restarts routstrd + cocod Cashu wallet
- **Auto top-up** — refills the Cashu wallet from the Routstr app wallet when balance drops
- **Federated routing** — discovers providers via Nostr pubkeys, picks the cheapest
- **One key, every model** — a single API key works across all models, chosen per request
- **Backup coverage** — daemon databases and configs included in Hub backups

## Quick start

```sh
cd frontend && yarn install && yarn build:http
go build -o hub cmd/http/main.go
./hub
```

In the Hub: AI & Agents → Routstr → Connect → follow the wizard. It creates an isolated app wallet, mints your API key, and funds it with Cashu ecash.

## Video walkthrough

Repo: [welliv/alby-hub/tree/routstr](https://github.com/welliv/alby-hub/tree/routstr)

Demo video: [watch here](https://blossom.primal.net/4355a71eee3d7b4e3d05ed0e48eb123d504df241c944b12ef795805e34f5c8ea.mp4)
