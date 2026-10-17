# Greenlight backend for Alby Hub: mainnet trial report

> Fill in every `…`. All times in **UTC** (`YYYY-MM-DDTHH:MM:SSZ`). Leave out names, locations, time zones, IP addresses, file paths, machine details, the second wallet's identity, NWC secrets, certificates and the recovery phrase. Public on-chain txids, payment hashes and the throwaway trial node id are fine: they let anyone verify the claims. Run pasted logs through `docs/greenlight/mainnet-trial/tools/redact.py`, then read them yourself.

## 1. Summary
- **Result:** … (for example: "11 of 12 steps passed; outbound payments 3/3; JIT channel opened at the advertised fee; all funds withdrawn")
- **Window:** … → … (UTC)
- **Total deposited / withdrawn / cost:** … / … / … sats
- **Blocking findings:** … (or "none")

## 2. What was tested
| Item | Value |
|---|---|
| Hub source | `trial/mainnet` @ `15b9ef0b6204bc0e83880084e0d25e41499630fe` (based on `release/greenlight` @ `960a47de`), or the `feat/greenlight-backend` commit you built: … |
| Hub binary sha256 | … (self-built; from `SHA256SUMS`) |
| glcli | gl-cli 0.1.3 @ Blockstream/greenlight `15b22cf0`, sha256 … |
| Hosted node version (`getinfo.version`) | … |
| Network | bitcoin (mainnet) |
| Trial node id | … |
| LSP node id(s) seen | … |
| OS family | Linux / macOS / WSL2 (no version or hardware details) |

## 3. Step results
| # | Step | Result (PASS / FAIL / N/A / STOPPED) | Key numbers | UTC |
|---|---|---|---|---|
| 1.3 | Register on mainnet with developer cert | … | – | … |
| 1.4 | Node healthy, signer running | … | – | … |
| 3.1 | JIT quote | … | amount …, advertised fee … | … |
| 3.2 | JIT receive | … | received …, fee …, channel capacity …, funding txid … | … |
| 4 | Outbound pays | … | success …/3, fees …, seconds … | … |
| 5 | Receive on existing channel | … | amount …, fee 0? | … |
| 6 | Keysend | … | amount …, fee … | … |
| 7.3 | NWC in-budget pays | … | 2 × 300 | … |
| 7.4 | NWC overspend refused | … | error code … | … |
| 7.5 | Revoked connection refused | … | error … | … |
| 8 | Pending/unclear payment (if it happened) | … | hash …, resolved how …, minutes … | … |
| 9 | Restart: same node, balances, signer | … | – | … |
| 9.3 | Receive while hub stopped (optional) | … | outcome … | … |
| 10 | Restore from phrase on a new workdir | … | same node id: yes/no | … |
| 11.3 | Cooperative close | … | closing txid …, who paid the fee … | … |
| 11.4 | On-chain withdraw (all) | … | txid …, fee rate … sat/vB | … |
| 12 | `scheduler export` of empty node (optional) | … | export size …, sha256 … (URL not published) | … |

## 4. Issues seen
For each: **step, UTC time, exact error text (redacted), what the hub showed, what the other side saw, how it ended.** Link the matching upstream issue (Blockstream/greenlight #739, #750, #751, #752) where relevant.
1. …

## 5. Money reconciliation (sats)
| Flow | Amount | Proof |
|---|---|---|
| Deposited via JIT receive | … | payment hash … |
| Deposited via second receive | … | payment hash … |
| LSP opening fee | … | advertised … vs actual … |
| Routing fees (all pays) | … | – |
| Sent out over Lightning (tests + drain) | … | hashes … |
| Channel close (to on-chain) | … | closing txid … |
| On-chain withdraw fee | … | txid … |
| Withdrawn on-chain | … | txid … |
| **Unaccounted** | **…** (should be 0) | |

## 6. Evidence index
List the files kept privately (step → file). Publish only redacted copies. Screenshots must not show URLs with ports, paths, other tabs, or the second wallet's identity.
- …

## 7. Conclusions and asks
- **For Blockstream (@cdecker):** … (for example: mainnet registration with a developer cert works / fails; xpay success rate; LSP params; any freeze)
- **For Alby (@rolznz):** … (for example: feature parity seen live; what a reviewer should look at first)
- **Next step proposed:** …
