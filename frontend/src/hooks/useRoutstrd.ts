import { request } from "src/utils/request";

// Module-level cache for getAllRoutstrdModels
let cachedModels: { models: RoutstrdModel[] } | null = null;
let cacheTimestamp = 0;
const CACHE_TTL = 60 * 1000; // 60 seconds — the daemon keeps its own warm cache

export type CostFields = {
  prompt: number;
  completion: number;
  request?: number;
  image?: number;
  web_search?: number;
  input_cache_read?: number;
  input_cache_write?: number;
  max_prompt_cost?: number;
  max_completion_cost?: number;
  max_cost?: number;
  internal_reasoning?: number;
};

export type RoutstrdModel = {
  id: string;
  name?: string;
  description?: string;
  context_length?: number;
  created?: number;
  enabled?: boolean;
  upstream_provider_id?: string;
  canonical_slug?: string;
  architecture?: {
    modality?: string;
    input_modalities?: string[];
    output_modalities?: string[];
    tokenizer?: string;
    instruct_type?: string | null;
  };
  pricing?: CostFields;
  sats_pricing?: CostFields;
  top_provider?: {
    context_length?: number;
    max_completion_tokens?: number;
    is_moderated?: boolean;
  };
  per_request_limits?: Record<string, unknown> | null;
};

export type RoutstrdKey = {
  id: string;
  name: string;
  balance: number;
  apiKey?: string;
  createdAt?: number;
  lastUsed?: number | null;
};

type DaemonResponse<T> = { output: T };

type RoutstrdFetchOptions = RequestInit & {
  /** Abort after N ms (default 15000). NWC status should use a short timeout. */
  timeoutMs?: number;
};

function routstrdFetch<T>(path: string, init?: RoutstrdFetchOptions) {
  const { timeoutMs = 15_000, ...rest } = init || {};
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  // Merge signals if caller already passed one
  const parentSignal = rest.signal;
  if (parentSignal) {
    if (parentSignal.aborted) {
      controller.abort();
    } else {
      parentSignal.addEventListener("abort", () => controller.abort(), {
        once: true,
      });
    }
  }

  return request<DaemonResponse<T>>(`/api/routstrd${path}`, {
    ...rest,
    signal: controller.signal,
  })
    .then((r) => r?.output)
    .finally(() => clearTimeout(timer));
}

export async function getAllRoutstrdModels(forceRefresh = false) {
  // Return cached result if fresh (unless forcing refresh)
  if (
    !forceRefresh &&
    cachedModels &&
    Date.now() - cacheTimestamp < CACHE_TTL
  ) {
    return cachedModels;
  }
  // Otherwise fetch and cache
  const qs = forceRefresh ? "?refresh=true" : "";
  const result = await routstrdFetch<{ models: RoutstrdModel[] }>(
    `/models${qs}`
  );
  if (result) {
    cachedModels = result;
    cacheTimestamp = Date.now();
  }
  return cachedModels;
}

export async function nwcConnect(connectionString: string) {
  return routstrdFetch<{ message: string }>("/nwc/connect", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ connectionString }),
    timeoutMs: 25_000,
  });
}

export async function createRoutstrdClient(name: string) {
  const id = name
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "");
  return routstrdFetch<{
    message?: string;
    client: RoutstrdKey;
    created: boolean;
  }>("/clients/add", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, id }),
  });
}

export async function deleteRoutstrdClient(id: string) {
  return routstrdFetch<{ message: string; id: string }>("/clients/delete", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id }),
  });
}

export async function getRoutstrdClients() {
  return routstrdFetch<{
    clients: {
      id?: string;
      clientId?: string;
      name?: string;
      apiKey?: string;
    }[];
  }>("/clients");
}

export async function getRoutstrdKeyBalances() {
  return routstrdFetch<{
    keys: RoutstrdKey[];
    total: number;
    unit: string;
  }>("/keys/balance");
}

export async function getRoutstrdBalance() {
  return routstrdFetch<{
    balances: Record<string, number>;
    unit: string;
    activeMint: string;
  }>("/balance");
}

/**
 * Reclaims prepaid provider tokens (the `apikey:*` entries in /keys/balance)
 * back into the daemon's Cashu wallet at the given mint. Provider tokens are
 * ecash deposited at the provider's mint for prepayment — they are NOT in the
 * wallet and cannot be melted until reclaimed. Called before a refund so the
 * refund covers all money, not just the wallet pile.
 */
export async function reclaimProviderTokens(mintUrl: string) {
  return routstrdFetch<{
    message: string;
    pendingTokens: number;
    apiKeys: number;
    results: Array<{ baseUrl: string; success: boolean }>;
  }>("/refund", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mintUrl }),
  });
}

export async function getRoutstrdModelProviders(modelId: string) {
  return routstrdFetch<{
    id: string;
    name?: string;
    providers: Array<{
      baseUrl: string;
      pricing: {
        prompt: number;
        completion: number;
        request: number;
        max_cost: number;
      };
    }>;
  }>(`/models/${encodeURIComponent(modelId)}/providers`);
}

export async function getRoutstrdUsageSummary() {
  return routstrdFetch<{
    generatedAt: number;
    totals: {
      requests: number;
      promptTokens: number;
      completionTokens: number;
      totalTokens: number;
      cost: number;
      satsCost: number;
    };
    models: Array<{ model: string; requests: number; satsCost: number }>;
    providers: Array<{ provider: string; requests: number; satsCost: number }>;
    clients: Array<{ client: string; requests: number; satsCost: number }>;
    days: Array<{ date: string; requests: number; satsCost: number }>;
  }>("/usage/summary");
}

/**
 * Fund the Cashu wallet reliably by asking the mint for a Bolt11 invoice
 * then paying it via Hub's Lightning node.
 *
 * ALWAYS sources sats from the Routstr app's isolated NWC wallet
 * (fromAppId) — never the main wallet.
 *
 * Returns the funded amount on success or throws.
 */
export async function fundFromHub(
  amount: number,
  appId: number,
  onProgress?: (msg: string) => void
): Promise<number> {
  if (!appId) {
    throw new Error("fundFromHub requires the Routstr appId");
  }

  // 1. Get invoice from the Cashu mint
  onProgress?.("Creating Lightning invoice at Cashu mint…");
  const invResult = await routstrdFetch<{
    invoice: string;
    amount: number;
    mintUrl?: string;
  }>("/wallet/receive/bolt11", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ amount }),
  });
  const invoice = invResult?.invoice;
  if (!invoice) {
    throw new Error("Failed to create invoice at Cashu mint");
  }

  // 2. Pay invoice via Hub's Lightning node, from the Routstr isolated wallet
  onProgress?.(`Paying ${amount} sats via Lightning…`);
  const encodedInvoice = encodeURIComponent(invoice);
  const payResult = await request<{
    id: number;
    type: string;
    state: string;
  }>(`/api/payments/${encodedInvoice}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ fromAppId: appId }),
  });

  if (!payResult || payResult.state !== "settled") {
    throw new Error(`Hub payment failed: ${payResult?.state || "no response"}`);
  }

  onProgress?.("Deposit complete");
  return amount;
}

/**
 * Refund the ENTIRE Cashu wallet back to the Routstr app's isolated wallet.
 *
 * Uses APP-SCOPED invoices: the invoice is created for the Routstr app
 * itself, so when the mint melts Cashu to pay it, the sats are credited
 * DIRECTLY to the Routstr app wallet — no main-wallet hop, no transfer.
 *
 * Drains to zero: each pass quotes the mint's melt fee fresh for the full
 * remaining balance, then melts `balance − fee`. The mint returns any
 * change (fee overestimate or proof overshoot) to the wallet, which the
 * next pass re-quotes and melts. The loop stops when the balance is zero
 * or when the fee exceeds the remainder (a sub-fee amount no melt can
 * move). The fee is never assumed — it is queried every pass (re-quoted
 * only when the remaining balance moved materially, to avoid a throwaway
 * quote invoice per pass).
 */
export class PartialRefundError extends Error {
  totalRefunded: number;

  constructor(totalRefunded: number, message: string) {
    super(message);
    this.name = "PartialRefundError";
    this.totalRefunded = totalRefunded;
  }
}

// previewRefund creates the melt invoice and quotes the fee so the confirm
// screen shows exact numbers. Returns the invoice (to be passed to
// refundFromHub) along with balance/fee/net. The invoice IS the one that
// will be melted — no throwaway invoices, no fee discrepancy.
export async function previewRefund(
  appId: number,
  mintUrl: string
): Promise<{
  invoice: string;
  balance: number;
  fee: number;
  net: number;
} | null> {
  const bal = await getRoutstrdBalance();
  const walletBal = bal?.balances
    ? Object.values(bal.balances).reduce((a, b) => a + b, 0)
    : 0;
  if (walletBal <= 0) {
    return null;
  }

  // Estimate conservatively (3 sats). Create the invoice at balance−3,
  // then quote the fee. If the actual fee differs, recreate at balance−fee.
  // The invoice returned IS the one that gets melted.
  let fee = 3;
  let net = Math.max(1, walletBal - fee);
  let invoice = await createAppScopedInvoice(net, appId);
  fee = await fetchMeltFee(invoice, mintUrl);

  if (fee !== 3 && walletBal > fee) {
    // Fee was different from estimate — recreate at the correct net.
    net = walletBal - fee;
    invoice = await createAppScopedInvoice(net, appId);
  }

  return { invoice, balance: walletBal, fee, net };
}

async function fetchMeltFee(invoice: string, mintUrl: string): Promise<number> {
  const resp = await fetch(
    `${mintUrl.replace(/\/+$/, "")}/v1/melt/quote/bolt11`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request: invoice, unit: "sat" }),
    }
  );
  if (!resp.ok) {
    return 0;
  }
  const quote: unknown = await resp.json();
  if (
    typeof quote === "object" &&
    quote !== null &&
    "fee_reserve" in quote &&
    typeof (quote as { fee_reserve?: unknown }).fee_reserve === "number"
  ) {
    return (quote as { fee_reserve: number }).fee_reserve;
  }
  return 0;
}

export async function refundFromHub(
  appId: number,
  mintUrl: string,
  prebuiltInvoice?: string
): Promise<{ refunded: number; fee: number }> {
  if (!appId) {
    throw new Error("refundFromHub requires the Routstr appId");
  }
  if (!mintUrl) {
    throw new Error("refundFromHub requires the active mint URL");
  }

  const bal = await getRoutstrdBalance();
  const walletBal = bal?.balances
    ? Object.values(bal.balances).reduce((a, b) => a + b, 0)
    : 0;
  if (walletBal <= 0) {
    return { refunded: 0, fee: 0 };
  }

  let meltInvoice: string;
  // eslint-disable-next-line no-useless-assignment
  let fee = 0;

  if (prebuiltInvoice) {
    // Use the invoice created by previewRefund — same invoice, same fee.
    meltInvoice = prebuiltInvoice;
    fee = await fetchMeltFee(meltInvoice, mintUrl);
  } else {
    // Standalone call (no preview): quote + create fresh.
    const quoteInvoice = await createAppScopedInvoice(walletBal, appId);
    fee = await fetchMeltFee(quoteInvoice, mintUrl);
    const send = Math.max(0, walletBal - fee);
    if (send <= 0) {
      return { refunded: 0, fee };
    }
    meltInvoice = await createAppScopedInvoice(send, appId);
  }

  if (!meltInvoice) {
    return { refunded: 0, fee: 0 };
  }

  // 2. Melt the invoice. If proof-selection causes a "non-negative" error,
  //    retry with 1 sat less (creates new invoices for each attempt).
  let actualRefunded = 0;
  const estimatedNet = prebuiltInvoice
    ? walletBal - fee // net = balance - fee from prebuilt invoice
    : walletBal - fee;

  for (let attempt = estimatedNet; attempt >= 1; attempt--) {
    const invoice =
      prebuiltInvoice && attempt === estimatedNet
        ? prebuiltInvoice // first attempt uses the prebuilt invoice
        : await createAppScopedInvoice(attempt, appId);
    try {
      void (await routstrdFetch<{ message: string }>("/wallet/send/bolt11", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ invoice, mintUrl }),
        timeoutMs: 90_000,
      }));
      actualRefunded = attempt;
      break;
    } catch (error) {
      const msg = error instanceof Error ? error.message : String(error);
      if (/insufficient|not enough (funds|proofs)|non-negative/i.test(msg)) {
        if (attempt <= 1) {
          throw error;
        }
        continue;
      }
      throw error;
    }
  }

  // 3. Verify wallet is zero. Loop cleanup until the balance is truly
  //    gone or falls below the mint's minimum melt threshold. Track
  //    cleanup sats so the final fee is accurate.
  for (let cleanupPass = 0; cleanupPass < 5; cleanupPass++) {
    const verifyBal = await getRoutstrdBalance();
    const remaining = verifyBal?.balances
      ? Object.values(verifyBal.balances).reduce((a, b) => a + b, 0)
      : 0;
    if (remaining <= 0) {
      break;
    }
    const cleaned = await cleanupRemainder(remaining, appId, mintUrl);
    if (typeof cleaned === "number") {
      actualRefunded += cleaned;
    } else {
      break; // sub-fee dust — can't melt any further
    }
  }

  // Fee = original balance − actual total refunded.
  // This accounts for: mint fee + proof-selection shrinkage + cleanup melts.
  const actualFee = walletBal - actualRefunded;
  return { refunded: actualRefunded, fee: actualFee };
}

// cleanupRemainder drains the last few sats from the wallet.
// Returns the amount melted, or null if the balance is sub-fee dust.
// Quotes fee fresh for the tiny balance melt — no caching, no stale quotes.
async function cleanupRemainder(
  remaining: number,
  appId: number,
  mintUrl: string
): Promise<number | null> {
  const quoteInvoice = await createAppScopedInvoice(remaining, appId);
  const feeResp = await fetch(
    `${mintUrl.replace(/\/+$/, "")}/v1/melt/quote/bolt11`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request: quoteInvoice, unit: "sat" }),
    }
  );

  let fee = 0;
  if (feeResp.ok) {
    const quote: unknown = await feeResp.json();
    if (
      typeof quote === "object" &&
      quote !== null &&
      "fee_reserve" in quote &&
      typeof (quote as { fee_reserve?: unknown }).fee_reserve === "number"
    ) {
      fee = (quote as { fee_reserve: number }).fee_reserve;
    }
  }

  const send = Math.max(0, remaining - fee);
  if (send <= 0) {
    return null; // sub-fee dust — genuinely un-meltable
  }

  for (let attempt = send; attempt >= 1; attempt--) {
    const invoice = await createAppScopedInvoice(attempt, appId);
    try {
      await routstrdFetch<{ message: string }>("/wallet/send/bolt11", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ invoice, mintUrl }),
        timeoutMs: 90_000,
      });
      return attempt;
    } catch (error) {
      const msg = error instanceof Error ? error.message : String(error);
      if (/insufficient|not enough (funds|proofs)|non-negative/i.test(msg)) {
        if (attempt <= 1) {
          return null;
        }
        continue;
      }
      return null; // non-retryable — give up on cleanup
    }
  }
  return null;
}

async function createAppScopedInvoice(
  amountSat: number,
  appId: number
): Promise<string> {
  const invResult = await request<{ invoice: string }>("/api/invoices", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ amount: amountSat * 1000, appId }),
  });
  const invoice = invResult?.invoice;
  if (!invoice) {
    throw new Error("Failed to create invoice on Hub");
  }
  return invoice;
}
