import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "src/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "src/components/ui/dialog";
import { LoadingButton } from "src/components/ui/custom/loading-button";
import {
  getRoutstrdBalance,
  getRoutstrdKeyBalances,
  PartialRefundError,
  reclaimProviderTokens,
  refundFromHub,
} from "src/hooks/useRoutstrd";
import { handleRequestError } from "src/utils/handleRequestError";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRefundComplete: () => void;
  /** Routstr connection app ID — sats go back to this app's isolated wallet */
  appId: number;
};

export function RefundDialog({
  open,
  onOpenChange,
  onRefundComplete,
  appId,
}: Props) {
  const [isProcessing, setIsProcessing] = useState(false);
  const [step, setStep] = useState<"confirm" | "processing" | "done">(
    "confirm"
  );
  const [actualRefunded, setActualRefunded] = useState(0);
  const [actualFee, setActualFee] = useState(0);
  const [remainingDust, setRemainingDust] = useState(0);
  const [walletBalance, setWalletBalance] = useState<number | null>(null);
  // Prepaid provider tokens (apikey:* entries) — not in the wallet, but
  // reclaimable via the daemon /refund before melting.
  const [providerTokens, setProviderTokens] = useState(0);
  const [loadingBalances, setLoadingBalances] = useState(true);
  const [mintUrl, setMintUrl] = useState<string | null>(null);
  const [refundPhase, setRefundPhase] = useState("");
  // Cache last-known values so subsequent opens feel instant
  const lastBalance = useRef<number | null>(null);
  const lastProviderTokens = useRef(0);
  const hasEverLoaded = useRef(false);

  const doLoadBalances = useCallback(async () => {
    // 1. Get Cashu balance from daemon
    const balResult = await getRoutstrdBalance();
    const mints = balResult?.balances ? Object.keys(balResult.balances) : [];
    const activeMint = balResult?.activeMint || mints[0] || null;
    const walletBal = balResult?.balances
      ? Object.values(balResult.balances).reduce((a, b) => a + b, 0)
      : 0;

    // 2. Prepaid provider tokens (apikey:* entries in /keys/balance). These
    // are NOT in the wallet and cannot be melted directly — the refund
    // reclaims them first (daemon /refund), so show them as refundable.
    let providerFloat: number;
    try {
      const keyResult = await getRoutstrdKeyBalances();
      providerFloat = (keyResult?.keys || [])
        .filter((k) => k.id !== "wallet")
        .reduce((sum, k) => sum + (k.balance || 0), 0);
    } catch {
      providerFloat = 0;
    }
    // Update state and cache
    setWalletBalance(walletBal);
    setProviderTokens(providerFloat);
    setMintUrl(activeMint);
    lastBalance.current = walletBal;
    lastProviderTokens.current = providerFloat;
    hasEverLoaded.current = true;
  }, []);

  const loadBalances = useCallback(async () => {
    setLoadingBalances(true);
    try {
      await doLoadBalances();
    } catch {
      // Non-critical
    } finally {
      setLoadingBalances(false);
    }
  }, [doLoadBalances]);

  const loadBalancesSilent = useCallback(async () => {
    try {
      await doLoadBalances();
    } catch {
      // Silently fail — cached values are already displayed
    }
  }, [doLoadBalances]);

  useEffect(() => {
    if (open) {
      setStep("confirm");
      setIsProcessing(false);

      if (hasEverLoaded.current) {
        // Show cache immediately, refresh silently in background
        setWalletBalance(lastBalance.current);
        setProviderTokens(lastProviderTokens.current);
        setLoadingBalances(false);
        loadBalancesSilent();
      } else {
        // First load — show spinner
        loadBalances();
      }
    }
  }, [open, loadBalances, loadBalancesSilent]);

  const canRefund = walletBalance !== null && walletBalance > 0;

  const handleRefund = async () => {
    if (!canRefund) {
      return;
    }
    setIsProcessing(true);
    setStep("processing");
    try {
      if (providerTokens > 0 && mintUrl) {
        setRefundPhase("Reclaiming provider tokens...");
        try {
          await reclaimProviderTokens(mintUrl);
        } catch {
          // Provider refund failed — continue with the wallet balance only
        }
      }
      if (!mintUrl) {
        throw new Error("No active mint to melt from");
      }
      setRefundPhase("Refunding wallet balance...");
      const { refunded, fee } = await refundFromHub(appId, mintUrl);
      setActualRefunded(refunded);
      setActualFee(fee);

      // Check for remaining dust
      try {
        const bal = await getRoutstrdBalance();
        const rem = bal?.balances
          ? Object.values(bal.balances).reduce((a, b) => a + b, 0)
          : 0;
        setRemainingDust(rem);
      } catch {
        setRemainingDust(0);
      }
      if (fee > 0) {
        toast.success(
          `Refunded ${refunded} sats to Routstr wallet (${fee} sat fee)`
        );
      } else {
        toast.success(`Refunded ${refunded} sats to your Routstr wallet`);
      }
      setStep("done");
      onRefundComplete();
    } catch (error) {
      if (error instanceof PartialRefundError && error.totalRefunded > 0) {
        // Earlier drain passes already moved sats — report the partial
        // outcome instead of a blanket failure.
        toast.warning(
          `Partially refunded ${error.totalRefunded} sats to your Routstr wallet (${error.message})`
        );
        setStep("done");
        onRefundComplete();
      } else {
        handleRequestError("Refund failed", error);
        setStep("confirm");
      }
    } finally {
      setIsProcessing(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[400px]">
        {step === "confirm" && (
          <>
            <DialogHeader>
              <DialogTitle>Refund API Balance</DialogTitle>
            </DialogHeader>

            <div className="rounded-lg border border-border/60 bg-muted/10 p-4 space-y-2 text-sm">
              {loadingBalances ? (
                <div className="flex items-center gap-2 text-muted-foreground">
                  <span className="h-3 w-3 rounded-full border border-border animate-spin border-t-transparent" />
                  Loading…
                </div>
              ) : (
                <>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">
                      Wallet balance
                    </span>
                    <span className="font-medium tabular-nums">
                      {walletBalance ?? "?"} sats
                    </span>
                  </div>
                  {walletBalance !== null && providerTokens > 0 && (
                    <div className="text-[10px] text-muted-foreground/60">
                      +{providerTokens.toFixed(2)} sats in provider tokens
                    </div>
                  )}
                  {walletBalance === 0 && (
                    <div className="text-center space-y-1">
                      <p className="text-xs text-muted-foreground">
                        Balance is zero — nothing to refund.
                      </p>
                    </div>
                  )}
                </>
              )}
            </div>

            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <LoadingButton
                loading={isProcessing}
                onClick={handleRefund}
                disabled={!canRefund}
              >
                Confirm Refund
              </LoadingButton>
            </DialogFooter>
          </>
        )}

        {step === "processing" && (
          <>
            <DialogHeader>
              <DialogTitle>Refunding…</DialogTitle>
            </DialogHeader>
            <div className="py-8 text-center text-sm text-muted-foreground">
              {refundPhase}
            </div>
          </>
        )}

        {step === "done" && (
          <>
            <DialogHeader>
              <DialogTitle>Refund complete</DialogTitle>
              <DialogDescription>
                {actualRefunded} sats sent to Routstr wallet
                {actualFee > 0 && ` (${actualFee} sat fee)`}.
                {remainingDust > 0
                  ? ` ${remainingDust} sat${remainingDust > 1 ? "s" : ""} remain in wallet — below the mint's minimum fee and cannot be melted.`
                  : " Wallet balance is now zero."}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button onClick={() => onOpenChange(false)}>Close</Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
