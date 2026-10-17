# Mainnet trial: go / no-go (one page)

Tick every box. **Any unticked GO item = NO-GO.** Any ABORT item during the trial: stop and go to PLAN.md §F.

## Before setup
- [ ] My own machine, encrypted disk, screen lock, can stay online and awake ~48 h
- [ ] OS: Linux, macOS, or WSL2 Ubuntu on Windows
- [ ] Hub self-built with `build.sh` from `trial/mainnet` @ `15b9ef0b` (or `feat/greenlight-backend` with identical code outside `docs/greenlight`); `BUILDINFO.txt` shows the commit
- [ ] glcli built from Blockstream/greenlight `15b22cf0`; `signer run --help` shows `--backup-path`
- [ ] Developer certificate files in a private folder; never pasted or shown anywhere
- [ ] No other Alby Hub or glcli signer running (`pgrep -fa "glcli.*signer run"` is empty)
- [ ] Second Lightning wallet with ~30k sats; an external `bc1…` withdraw address written down
- [ ] Paper ready for a **new** 12-word phrase (never a testnet phrase)
- [ ] Budget decided: **≤ 25k planned, 50k hard cap**, sats I can afford to lose
- [ ] I've read PLAN.md §F (abort and recovery)

## After setup, before funding
- [ ] `setup.sh` finished; `check.sh` says **CHECK PASSED** (loopback only, private permissions)
- [ ] Registration succeeded on **bitcoin**; node pubkey recorded (UTC time)
- [ ] Node healthy and signer running for 10 min straight
- [ ] Recovery phrase on paper; `.bkp` exported to a second private location
- [ ] JIT quote seen **before paying**: fee ≤ 5,000 sats and ≤ 25% of the amount, LSP peer recorded

## Abort immediately if (no retries)
- [ ] An app paid beyond its budget, or a revoked app can still pay  → **critical**, go to exit (step 11)
- [ ] Balance differs from my notes by more than routing fees  → **critical**
- [ ] `lsp_opening_fee_exceeded` alert  → F6
- [ ] A payment is pending for more than 2 min, or `no response from lightningd`  → F1, never re-pay
- [ ] Node status "possible frozen node"  → F2
- [ ] Signer down for more than 2 min  → F3
- [ ] Node unreachable or TLS alert  → F4
- [ ] Restore shows a **different** node pubkey  → stop the restored hub, re-check the phrase
- [ ] Any total deposit would go above 50k sats

## Done when
- [ ] Hub Lightning 0, on-chain 0, nothing pending; withdrawal tx has ≥ 6 confirmations
- [ ] Deposits − fees = withdrawals (reconciled)
- [ ] Evidence redacted (`tools/redact.py`) and read by me before publishing; UTC only; no paths, IPs, NWC secrets, cert or phrase
