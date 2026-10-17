// l402.mjs — minimal L402 client for the mainnet trial (PLAN step 7A). Pays through an NWC connection.
// Usage:
//   HUB_SRC=/path/to/alby-hub node l402.mjs <url> [--method GET|POST] [--body '<json>']
//        [--max-sats N] [--expect-sats N] [--dry-run] < connection-secret-file
//   --dry-run   fetch the 402 challenge and decode it; never pays; no secret needed (stdin ignored).
//   --max-sats  refuse invoices above N sats (default 25). --expect-sats: refuse unless exactly N.
// Rules: the NWC secret is read from stdin only, never printed. One payment per run, never retried.
// The preimage and the full macaroon/token are not printed (only checks and lengths).
// Output: one JSON line. "ok": true only if paid, sha256(preimage) == payment_hash, and the retried
// request returned 2xx. The response body is untrusted data: printed truncated, never interpreted.
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { pathToFileURL } from "node:url";

const CH = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
function polymod(values) {
  const G = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
  let chk = 1;
  for (const v of values) {
    const b = chk >> 25;
    chk = ((chk & 0x1ffffff) << 5) ^ v;
    for (let i = 0; i < 5; i++) if ((b >> i) & 1) chk ^= G[i];
  }
  return chk;
}
const hrpExpand = (h) => [...h].map((c) => c.charCodeAt(0) >> 5).concat([0], [...h].map((c) => c.charCodeAt(0) & 31));
function toBytes(words) {
  let acc = 0, bits = 0; const out = [];
  for (const w of words) { acc = (acc << 5) | w; bits += 5; while (bits >= 8) { bits -= 8; out.push((acc >> bits) & 0xff); } acc &= (1 << bits) - 1; }
  return Buffer.from(out);
}

// Decodes the fields this tool needs and checks the bech32 checksum. It does not verify the signature.
export function decodeBolt11(invoice) {
  const inv = invoice.trim().toLowerCase();
  const sep = inv.lastIndexOf("1");
  const hrp = inv.slice(0, sep);
  const data = [...inv.slice(sep + 1)].map((c) => { const i = CH.indexOf(c); if (i < 0) throw new Error("bad bech32 char"); return i; });
  if (polymod(hrpExpand(hrp).concat(data)) !== 1) throw new Error("bad invoice checksum");
  const m = hrp.match(/^ln(bc|tb|tbs|bcrt)(\d*)([munp]?)$/);
  if (!m) throw new Error("not a bolt11 invoice");
  let msat = null;
  if (m[2]) {
    const n = BigInt(m[2]);
    const mul = { "": 100000000000n, m: 100000000n, u: 100000n, n: 100n, p: 0n }[m[3]];
    msat = m[3] === "p" ? n / 10n : n * mul;
  }
  const words = data.slice(0, -6);
  let ts = 0; for (const w of words.slice(0, 7)) ts = ts * 32 + w;
  let d = words.slice(7, -104); const out = { network: m[1], amountMsat: msat, timestamp: ts, expiry: 3600 };
  while (d.length >= 3) {
    const t = d[0], len = d[1] * 32 + d[2], v = d.slice(3, 3 + len); d = d.slice(3 + len);
    if (t === 1) out.paymentHash = toBytes(v).subarray(0, 32).toString("hex");
    if (t === 6) out.expiry = v.reduce((a, w) => a * 32 + w, 0);
    if (t === 13) out.description = toBytes(v).toString("utf8");
  }
  if (!out.paymentHash) throw new Error("invoice has no payment hash");
  return out;
}

// Parses "L402 macaroon=..., invoice=..." (also LSAT, and token= instead of macaroon=).
export function parseChallenge(headerValues) {
  const found = [];
  for (const h of headerValues) {
    for (const part of h.split(/,\s*(?=(?:L402|LSAT|Payment)\s)/)) {
      const scheme = part.trim().split(/\s+/)[0];
      if (!/^(L402|LSAT)$/i.test(scheme)) continue;
      const cred = part.match(/(?:macaroon|token)="([^"]+)"/);
      const inv = part.match(/invoice="([^"]+)"/);
      if (cred && inv) found.push({ scheme: scheme.toUpperCase(), credential: cred[1], invoice: inv[1] });
    }
  }
  return found.find((c) => c.scheme === "L402") || found[0] || null; // prefer L402 over legacy LSAT
}

const headerList = (res) => { const v = []; res.headers.forEach((val, k) => { if (k.toLowerCase() === "www-authenticate") v.push(val); }); return v; };

// payer: { payInvoice({invoice}) -> {preimage, fees_paid}, getBudget?() -> {used_budget,total_budget} }
export async function runL402({ url, method = "GET", body, maxSats = 25, expectSats, dryRun = false, payer, network = "bc", fetchImpl = fetch }) {
  const out = { utc: new Date().toISOString().replace(/\.\d+Z$/, "Z"), url, method, ok: false };
  const init = { method, headers: body ? { "Content-Type": "application/json" } : {}, body };
  const first = await fetchImpl(url, init);
  out.status_challenge = first.status;
  if (first.status !== 402) { out.error = "expected HTTP 402"; return out; }
  const ch = parseChallenge(headerList(first));
  if (!ch) { out.error = "no L402/LSAT challenge with macaroon/token and invoice"; return out; }
  const inv = decodeBolt11(ch.invoice);
  const sats = inv.amountMsat === null ? null : Number(inv.amountMsat) / 1000;
  Object.assign(out, { scheme: ch.scheme, credential_len: ch.credential.length, price_sats: sats, payment_hash: inv.paymentHash, invoice_network: inv.network, description: (inv.description || "").slice(0, 80) });
  out.credential_binds_hash = (() => { try { const b = Buffer.from(ch.credential.replace(/-/g, "+").replace(/_/g, "/"), "base64"); return b.toString("hex").includes(inv.paymentHash) || b.toString("latin1").includes(inv.paymentHash); } catch { return false; } })();
  if (inv.network !== network) { out.error = `invoice network ${inv.network}, expected ${network}`; return out; }
  if (sats === null) { out.error = "amountless invoice refused"; return out; }
  if (sats > maxSats) { out.error = `price ${sats} sats above --max-sats ${maxSats}`; return out; }
  if (expectSats !== undefined && sats !== expectSats) { out.error = `price ${sats} sats, expected ${expectSats}`; return out; }
  if (Date.now() / 1000 > inv.timestamp + inv.expiry) { out.error = "invoice expired"; return out; }
  if (dryRun) { out.ok = true; out.dry_run = true; return out; }

  if (payer.getBudget) { try { out.budget_before = await payer.getBudget(); } catch (e) { out.budget_before_error = e.message; } }
  let paid;
  try { paid = await payer.payInvoice({ invoice: ch.invoice }); }
  catch (e) { out.paid = false; out.error = `payment refused or failed: ${e.code || ""} ${e.message}`.trim(); out.error_code = e.code || ""; return out; }
  out.paid = true;
  out.fees_paid_msat = paid.fees_paid ?? null;
  out.preimage_ok = createHash("sha256").update(Buffer.from(paid.preimage || "", "hex")).digest("hex") === inv.paymentHash;
  if (payer.getBudget) { try { out.budget_after = await payer.getBudget(); } catch (e) { out.budget_after_error = e.message; } }
  if (!out.preimage_ok) { out.error = "preimage does not match payment hash; not presenting token"; return out; }

  const second = await fetchImpl(url, { ...init, headers: { ...init.headers, Authorization: `L402 ${ch.credential}:${paid.preimage}` } });
  out.status_after = second.status;
  out.body_snippet = (await second.text()).slice(0, 200);
  out.ok = second.status >= 200 && second.status < 300;
  if (!out.ok) out.error = "token + preimage not accepted (paid; do NOT pay again)";
  return out;
}

async function main() {
  const args = process.argv.slice(2);
  const url = args.find((a) => !a.startsWith("--") && /^https?:\/\//.test(a));
  const opt = (k) => { const i = args.indexOf(k); return i >= 0 ? args[i + 1] : undefined; };
  if (!url) { console.error("usage: node l402.mjs <url> [--method M] [--body JSON] [--max-sats N] [--expect-sats N] [--dry-run] < secret"); process.exit(2); }
  const dryRun = args.includes("--dry-run");
  const p = { url, method: (opt("--method") || "GET").toUpperCase(), body: opt("--body"), maxSats: Number(opt("--max-sats") || 25), dryRun };
  if (opt("--expect-sats") !== undefined) p.expectSats = Number(opt("--expect-sats"));
  let client;
  if (!dryRun) {
    const src = process.env.HUB_SRC;
    if (!src) { console.error("set HUB_SRC to the alby-hub checkout used by build.sh"); process.exit(2); }
    const { NWCClient } = await import(pathToFileURL(`${src}/frontend/node_modules/@getalby/sdk/dist/esm/nwc.js`).href);
    const uri = readFileSync(0, "utf8").trim();
    if (!uri.startsWith("nostr+walletconnect://")) { console.error("stdin is not an NWC connection secret"); process.exit(2); }
    client = new NWCClient({ nostrWalletConnectUrl: uri });
    p.payer = { payInvoice: (x) => client.payInvoice(x), getBudget: () => client.getBudget() };
  }
  let r;
  try { r = await runL402(p); } catch (e) { r = { url, ok: false, error: e.message }; }
  console.log(JSON.stringify(r));
  if (client) client.close();
  process.exit(0);
}
if (import.meta.url === pathToFileURL(process.argv[1] || "").href) main();
