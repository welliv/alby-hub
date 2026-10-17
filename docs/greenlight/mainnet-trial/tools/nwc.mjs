// nwc.mjs — minimal NWC client for the trial (uses the @getalby/sdk that build.sh installed).
// Usage: HUB_SRC=/path/to/alby-hub node nwc.mjs <method> '<json params>' < connection-secret-file
//   The connection secret (nostr+walletconnect://...) is read from stdin so it never lands in
//   shell history or `ps` output. Methods: getInfo getBalance getBudget payInvoice makeInvoice
//   payKeysend lookupInvoice listTransactions.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const src = process.env.HUB_SRC;
if (!src) { console.error("set HUB_SRC to the alby-hub checkout used by build.sh"); process.exit(2); }
const { NWCClient } = await import(pathToFileURL(`${src}/frontend/node_modules/@getalby/sdk/dist/esm/nwc.js`).href);
const [method, params] = process.argv.slice(2);
const uri = readFileSync(0, "utf8").trim();
if (!uri.startsWith("nostr+walletconnect://")) { console.error("stdin is not an NWC connection secret"); process.exit(2); }
const c = new NWCClient({ nostrWalletConnectUrl: uri });
const started = new Date().toISOString();
try {
  const r = await c[method](JSON.parse(params || "{}"));
  console.log(JSON.stringify({ utc: started, method, ok: true, result: r }));
} catch (e) {
  console.log(JSON.stringify({ utc: started, method, ok: false, error: e.code || "", message: e.message }));
}
c.close(); process.exit(0);
