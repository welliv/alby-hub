// l402-mock.mjs — tiny self-hosted L402 endpoint (fallback for PLAN step 7A when no public L402
// service works). Invoices come from YOUR second wallet's Lightning address (LNURL-pay), so every
// sat the hub pays goes back to you. No wallet secret is needed: only the public Lightning address.
// Usage: node l402-mock.mjs --ln-address name@your-wallet.example [--price-sats 5] [--port 8402]
//   Serves http://127.0.0.1:<port>/l402/ping on loopback only.
// Token: an Aperture-style bearer token (base64url JSON {payment_hash, path, price_sats, exp} + HMAC-SHA256
// with a per-run random key). It is not a macaroon library, but it is bound to the payment hash the same
// way: the server accepts "Authorization: L402 <token>:<preimage>" only if the HMAC is valid, the token is
// unexpired, the path matches and sha256(preimage) == payment_hash.
import http from "node:http";
import { createHash, createHmac, randomBytes, timingSafeEqual } from "node:crypto";
import { pathToFileURL } from "node:url";
import { decodeBolt11 } from "./l402.mjs";

const b64u = (b) => Buffer.from(b).toString("base64url");

export function lnurlInvoiceSource(lnAddress) {
  const [name, domain] = lnAddress.split("@");
  if (!name || !domain) throw new Error("--ln-address must look like name@domain");
  return async (sats) => {
    const meta = await (await fetch(`https://${domain}/.well-known/lnurlp/${encodeURIComponent(name)}`)).json();
    if (meta.tag !== "payRequest") throw new Error("not an LNURL-pay endpoint");
    const msat = sats * 1000;
    if (msat < meta.minSendable || msat > meta.maxSendable) throw new Error(`price outside ${meta.minSendable}-${meta.maxSendable} msat`);
    const cb = new URL(meta.callback); cb.searchParams.set("amount", String(msat));
    const r = await (await fetch(cb)).json();
    if (!r.pr) throw new Error(`LNURL callback gave no invoice: ${r.reason || "unknown"}`);
    return r.pr;
  };
}

export function createL402Server({ priceSats, getInvoice, path = "/l402/ping", ttlSec = 600, network = "bc" }) {
  const key = randomBytes(32);
  const sign = (payload) => createHmac("sha256", key).update(payload).digest();
  const stats = { challenges: 0, accepted: 0, rejected: 0 };
  const server = http.createServer(async (req, res) => {
    const send = (code, obj, headers = {}) => { res.writeHead(code, { "Content-Type": "application/json", ...headers }); res.end(JSON.stringify(obj)); };
    const url = new URL(req.url, "http://127.0.0.1");
    if (url.pathname !== path) return send(404, { error: "not found" });
    const auth = req.headers.authorization || "";
    const m = auth.match(/^(?:L402|LSAT)\s+([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+):([0-9a-f]{64})$/i);
    if (m) {
      const [, payload, sig, preimage] = m;
      let claims; try { claims = JSON.parse(Buffer.from(payload, "base64url").toString()); } catch { claims = null; }
      const good = claims && timingSafeEqual(sign(payload), Buffer.from(sig, "base64url").length === 32 ? Buffer.from(sig, "base64url") : Buffer.alloc(32))
        && claims.path === path && claims.exp > Date.now() / 1000
        && createHash("sha256").update(Buffer.from(preimage, "hex")).digest("hex") === claims.payment_hash;
      if (good) { stats.accepted++; return send(200, { ok: true, pong: new Date().toISOString().replace(/\.\d+Z$/, "Z"), paid_sats: claims.price_sats }); }
      stats.rejected++;
      return send(401, { error: "invalid L402 token or preimage" });
    }
    try {
      const pr = await getInvoice(priceSats);
      const inv = decodeBolt11(pr);
      if (inv.network !== network || Number(inv.amountMsat) !== priceSats * 1000) throw new Error("wallet returned an invoice with the wrong network or amount");
      const payload = b64u(JSON.stringify({ payment_hash: inv.paymentHash, path, price_sats: priceSats, exp: Math.floor(Date.now() / 1000) + ttlSec }));
      const token = `${payload}.${b64u(sign(payload))}`;
      stats.challenges++;
      return send(402, { error: "Payment Required", price_sats: priceSats }, { "WWW-Authenticate": `L402 macaroon="${token}", invoice="${pr}"` });
    } catch (e) { return send(502, { error: `could not create invoice: ${e.message}` }); }
  });
  server.stats = stats;
  return server;
}

async function main() {
  const a = process.argv.slice(2); const opt = (k) => { const i = a.indexOf(k); return i >= 0 ? a[i + 1] : undefined; };
  const ln = opt("--ln-address"); if (!ln) { console.error("usage: node l402-mock.mjs --ln-address name@domain [--price-sats 5] [--port 8402]"); process.exit(2); }
  const priceSats = Number(opt("--price-sats") || 5), port = Number(opt("--port") || 8402);
  const server = createL402Server({ priceSats, getInvoice: lnurlInvoiceSource(ln) });
  server.listen(port, "127.0.0.1", () => console.log(`l402-mock: http://127.0.0.1:${port}/l402/ping (${priceSats} sats per call, invoices from your Lightning address)`));
}
if (import.meta.url === pathToFileURL(process.argv[1] || "").href) main();
