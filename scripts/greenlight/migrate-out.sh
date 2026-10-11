#!/usr/bin/env bash
# migrate-out.sh — move an Alby Hub Greenlight node to a self-hosted Core Lightning node.
# Executes every step: convert the signer channel backup, derive hsm_secret from the
# mnemonic, start lightningd with the same node id, load the SCB stubs, connect to the
# peers and report funds.
# IMPORTANT (verified on CLN v26.06gl1, 2026-10-11): a CLN peer does NOT force-close on
# its own. It answers the stub's reestablish with a warning ("bad reestablish
# revocation_number") and keeps the channel. Ask each peer/LSP to force-close; once they
# do, our balance lands in this node's wallet (verified: 952,898 of 956,667 sat; the
# rest went to fees). Stubs for channels that were already closed log a harmless
# "FUNDS LOST. Unknown commitment" line: those funds are already on-chain.
# Set WAIT_CLOSE_SECS to wait for the peers' force-close and report funds again.
#
# Required env:
#   GL_DIR          hub's greenlight dir (contains backup.json)        e.g. ~/.local/share/albyhub/greenlight
#   MNEMONIC_FILE   file containing the hub's 12-word phrase (0600)
#   CLN_DIR         new lightning-dir to create (must not exist)
#   NETWORK         bitcoin | testnet | signet | regtest
#   BITCOIN_RPC     user:pass@host:port of your bitcoind
# Optional env:
#   GLCLI=glcli  LIGHTNINGD=lightningd  LIGHTNING_CLI=lightning-cli  HUB_SRC=<alby-hub checkout>
#   PEERS="pubkey@host:port ..."   peers to connect (default: none; peers reconnect to you if public)
#   CLN_ADDR=127.0.0.1:9735   EXTRA_CLN_ARGS="..."
#   EXPECTED_NODE_ID=<hex>    abort if the derived node id differs
# Stop the hub first: two signers for one node must never run at the same time.
set -euo pipefail
umask 077
: "${GL_DIR:?}" "${MNEMONIC_FILE:?}" "${CLN_DIR:?}" "${NETWORK:?}" "${BITCOIN_RPC:?}"
GLCLI=${GLCLI:-glcli}; LIGHTNINGD=${LIGHTNINGD:-lightningd}; LIGHTNING_CLI=${LIGHTNING_CLI:-lightning-cli}
HUB_SRC=${HUB_SRC:-$(cd "$(dirname "$0")/../.." && pwd)}; CLN_ADDR=${CLN_ADDR:-127.0.0.1:9735}
log(){ echo "[migrate-out $(date '+%F %T %Z')] $*"; }

[ -e "$CLN_DIR" ] && { echo "CLN_DIR $CLN_DIR already exists; refusing to overwrite" >&2; exit 1; }
[ -f "$GL_DIR/backup.json" ] || { echo "no $GL_DIR/backup.json (signer backup exists only after a channel was opened)" >&2; exit 1; }
if [ -f "$GL_DIR/signer.pid" ] && kill -0 "$(cat "$GL_DIR/signer.pid")" 2>/dev/null; then
  echo "the hub's Greenlight signer is still running (pid $(cat "$GL_DIR/signer.pid")); stop Alby Hub first" >&2; exit 1
fi
case $NETWORK in bitcoin|testnet|signet|regtest) ;; *) echo "bad NETWORK" >&2; exit 1;; esac
U=${BITCOIN_RPC%%@*}; HP=${BITCOIN_RPC#*@}

mkdir -p "$CLN_DIR/$NETWORK"
log "1/6 convert signer backup -> CLN SCB"
TMPGL=$(mktemp -d); trap 'rm -rf "$TMPGL"' EXIT
(cd "$HUB_SRC" && go run ./scripts/greenlight/seed "$MNEMONIC_FILE" "$TMPGL/hsm_secret")
"$GLCLI" -d "$TMPGL" -n "$NETWORK" signer convert-backup --path "$GL_DIR/backup.json" --output "$CLN_DIR/recover.json"
chmod 600 "$CLN_DIR/recover.json"
log "2/6 install hsm_secret derived from mnemonic"
install -m 600 "$TMPGL/hsm_secret" "$CLN_DIR/$NETWORK/hsm_secret"

log "3/6 start lightningd"
# shellcheck disable=SC2086
"$LIGHTNINGD" --lightning-dir="$CLN_DIR" --network="$NETWORK" \
  --bitcoin-rpcuser="${U%%:*}" --bitcoin-rpcpassword="${U#*:}" \
  --bitcoin-rpcconnect="${HP%%:*}" --bitcoin-rpcport="${HP##*:}" \
  --addr="$CLN_ADDR" --log-file="$CLN_DIR/log" --daemon ${EXTRA_CLN_ARGS:-} </dev/null >/dev/null 2>&1
LC=("$LIGHTNING_CLI" --lightning-dir="$CLN_DIR" --network="$NETWORK")
for _ in $(seq 1 60); do "${LC[@]}" getinfo >/dev/null 2>&1 && break; sleep 1; done
ID=$("${LC[@]}" getinfo | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
log "node id $ID"
if [ -n "${EXPECTED_NODE_ID:-}" ] && [ "$ID" != "$EXPECTED_NODE_ID" ]; then
  echo "node id mismatch (expected $EXPECTED_NODE_ID); stopping" >&2; "${LC[@]}" stop >/dev/null; exit 1
fi

log "4/6 recoverchannel"
SCB=$(python3 -c "import json;print(json.dumps(json.load(open('$CLN_DIR/recover.json'))['scb']))")
"${LC[@]}" -k recoverchannel scb="$SCB" | tee "$CLN_DIR/recoverchannel.json"

log "5/6 connect peers"
for p in ${PEERS:-}; do
  pk=${p%@*}; ha=${p#*@}
  "${LC[@]}" connect "$pk" "${ha%:*}" "${ha##*:}" >/dev/null && log "connected $pk" || log "WARN: could not connect $pk"
done

funds(){ "${LC[@]}" listfunds | python3 -c 'import sys,json;d=json.load(sys.stdin);print("confirmed_onchain_sat=",sum(o["amount_msat"] for o in d["outputs"] if o["status"]=="confirmed")//1000)'; }
log "6/6 funds now: $(funds)"
"${LC[@]}" listpeerchannels | python3 -c 'import sys,json;[print("  stub",c["channel_id"],c["state"],"peer",c["peer_id"]) for c in json.load(sys.stdin)["channels"]]'
log "ACTION: ask each peer above to force-close its channel with $ID; funds then arrive here."
if [ -n "${WAIT_CLOSE_SECS:-}" ]; then sleep "$WAIT_CLOSE_SECS"; log "funds after wait: $(funds)"; fi
log "done. Keep $CLN_DIR/recover.json and the mnemonic safe. Do NOT start Alby Hub's Greenlight signer for this node again."
