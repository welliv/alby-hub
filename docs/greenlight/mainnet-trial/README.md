# Mainnet trial kit: Greenlight backend for Alby Hub

Prepared 2026-10-17 (UTC). Nothing here has touched mainnet yet: no node, seed or credential has been created for the trial. Start at [../HANDOFF.md](../HANDOFF.md) if you are new to this work.

## Files
| File | What it's for |
|---|---|
| PLAN.md | Numbered runbook: preconditions, budget, steps 1–13, abort and recovery (§F) |
| CHECKLIST.md | One-page go / no-go list |
| EVIDENCE-TEMPLATE.md | Fill-in public report (UTC, no personal info) |
| RISKS.md | Mainnet code review: 3 fixes on `trial/mainnet`, plus the remaining risks |
| setup.sh | Creates the isolated mainnet workdir, `trial.env`, `run.sh` (clean env) and `check.sh` |
| build.sh | Builds the hub (frontend + Go, CGO) on Linux or macOS; WSL2 for Windows |
| tools/nwc.mjs | Minimal NWC client for steps 6–7 (secret read from stdin) |
| tools/l402.mjs | L402 client for step 7A: pays through an NWC connection (secret from stdin), checks the preimage, presents the macaroon/token; `--dry-run` never pays |
| tools/l402-mock.mjs | Fallback L402 endpoint on loopback; invoices come from your own Lightning address |
| tools/l402-selftest.mjs | Offline test of both L402 tools (fake invoices, fake hub budget); no network, no money |
| tools/redact.py | Scrubs logs before publishing (secrets, IPs, paths; offsets converted to UTC) |

No binaries are published. You build from this repo.

## Quick start (from a fresh clone)
```bash
git clone https://github.com/welliv/alby-hub && cd alby-hub
git checkout trial/mainnet && git log -1 --format=%H     # 15b9ef0b6204bc0e83880084e0d25e41499630fe
# or: git checkout feat/greenlight-backend && git diff --quiet 15b9ef0b HEAD -- . ':(exclude)docs/greenlight' ':(exclude)AGENTS.md' && echo same-code
export ALBYHUB=$(pwd) KIT=$(pwd)/docs/greenlight/mainnet-trial
# glcli 0.1.3 from Blockstream's repo at the tested commit (crates.io 0.1.1 is too old):
cargo install --locked --git https://github.com/Blockstream/greenlight --rev 15b22cf0f9b07bd0d343133d593c92b78addc403 gl-cli
$KIT/build.sh . ../hub-bin                               # Go >= 1.26.9, Node >= 22.12, yarn 1, a C compiler
$KIT/setup.sh --bin-dir ../hub-bin --cert-dir /path/to/your/devcert --dry-run
```
Then read PLAN.md and CHECKLIST.md and start at step 0. If a coding agent helps, start with [../AGENTS.md](../AGENTS.md) and [../START-PROMPT.md](../START-PROMPT.md). Step 1.3 registers the node before any money goes in and settles whether the developer certificate works on mainnet.

Branch commits (code identical to what the kit was reviewed against):
| Branch | Commit |
|---|---|
| trial/mainnet | 15b9ef0b6204bc0e83880084e0d25e41499630fe |
| release/greenlight | 960a47deddba8fac093ee6b4cddf368caf4c3282 |

Self-built checksums are machine-specific (the build isn't bit-reproducible); verify the commit hash in `BUILDINFO.txt` instead.

## What a Greenlight mainnet registration needs (checked 2026-10-17 against docs and Blockstream/greenlight `main` @ 08c004d)
| Question | Answer | Status |
|---|---|---|
| Developer cert or invite code? | **Either.** A developer cert (`client.crt` + `client-key.pem` from the Greenlight Developer Console) registers nodes with no invite. glcli's built-in default "nobody" cert needs an unused invite code (`glcli scheduler register --invite-code`). docs/partner.md and reference/certs.md. | Verified (docs + code) |
| Does this hub support invite codes? | **No.** The hub requires `GL_NOBODY_CRT/KEY` (a developer cert) and never passes `--invite-code`. | Verified (code) |
| Does the existing (testnet) dev cert work on bitcoin mainnet? | **Probably, not proven.** There is one scheduler for all networks (`https://scheduler.gl.blckstrm.com`), and the network is a field of each registration. The docs' own examples use the developer cert with `Network::Bitcoin`, and the same cert already registered nodes on testnet **and** signet. Nothing says a cert is network-scoped, but no mainnet registration has been attempted. PLAN 1.3 settles it at no risk (before any funds). | Unknown until 1.3 |
| Which LSP serves mainnet LSPS2? Minimum and fees? | **Not published.** gl-plugin asks every **connected peer** for `lsps2.get_info` and picks the first usable offer; no LSP is hardcoded in the open-source code. On Greenlight testnet a peer LSP was auto-connected and quoted a **2,500-sat opening fee and a 2,501-sat minimum**. Mainnet LSP identity and fees are unknown until PLAN 1.4 and 3.1. | Unknown (testnet values verified) |
| Supported networks | glcli help: "signet and bitcoin" (default `bitcoin`); register.md: `testnet`, `bitcoin`; README: bitcoin, testnet, regtest. The docs disagree; bitcoin is in all of them. | Verified (inconsistent docs) |
| Pricing or limits page? | **None on the docs site or repo.** Blockstream's 2023 launch post and blog: a free tier of **up to 1,000 on-demand nodes** per developer account; enterprise plans by contact (greenlight@blockstream.com), no public prices. Nodes that sit idle may eventually be shut down (README), with no deadline given. | Verified (2023 sources; may be outdated) |
| glcli version | crates.io has only gl-cli 0.1.1, which lacks `signer run --backup-path`, and the hub refuses it. Build 0.1.3 from git (commit 15b22cf0 was used for all prior testing). | Verified |
| Leaving Greenlight | `scheduler export` is one-way (the node becomes unschedulable) and returns a URL to an encrypted DB; no public decrypt tool. convert-backup + recoverchannel works without Greenlight but needs your own bitcoind and a peer force-close. | Verified (proto + code; regtest) |
| Multiple signers | Supported by Greenlight's design (docs/reference/signer-state.md, new 2026-10-14). The kit still runs one hub at a time to avoid two diverging hub databases. | Verified (docs) |
