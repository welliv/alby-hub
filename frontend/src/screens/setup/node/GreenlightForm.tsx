import { AlertTriangleIcon, FlaskConicalIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { wordlist } from "@scure/bip39/wordlists/english.js";
import * as bip39 from "@scure/bip39";

import { Button } from "src/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "src/components/ui/card";
import { Checkbox } from "src/components/ui/checkbox";
import { Label } from "src/components/ui/label";
import Container from "src/components/Container";
import TwoColumnLayoutHeader from "src/components/TwoColumnLayoutHeader";
import useSetupStore from "src/state/SetupStore";

/**
 * Greenlight setup step: experimental label, online+Blockstream dependency,
 * hold-invoice gap, and a reminder that glcli must be installed (the hub
 * verifies the binary at start and fails with a clear error if missing /
 * too old — see service/gl_signer.go checkGlcliCompat).
 */
export function GreenlightForm() {
  const navigate = useNavigate();
  const [ackOnline, setAckOnline] = useState(false);
  const [ackExperimental, setAckExperimental] = useState(false);

  useEffect(() => {
    useSetupStore.getState().updateNodeInfo({ backendType: "GREENLIGHT" });
    if (!useSetupStore.getState().nodeInfo.mnemonic) {
      useSetupStore.getState().updateNodeInfo({
        mnemonic: bip39.generateMnemonic(wordlist, 128),
      });
    }
  }, []);

  function next() {
    if (!ackOnline || !ackExperimental) {
      return;
    }
    navigate("/setup/security");
  }

  return (
    <Container>
      <TwoColumnLayoutHeader
        title="Greenlight (experimental)"
        pageTitle="Greenlight setup"
        description="Hosted Core Lightning node by Blockstream; keys stay on this machine."
      />
      <div className="flex flex-col gap-4 w-full mt-6">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base flex items-center gap-2">
              <FlaskConicalIcon className="size-4" />
              Experimental
            </CardTitle>
            <CardDescription>
              The Greenlight backend in Alby Hub is experimental. Prefer LDK for
              production wallets until a successful public-network pilot is
              documented.
            </CardDescription>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base flex items-center gap-2">
              <AlertTriangleIcon className="size-4" />
              What you need to know
            </CardTitle>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground space-y-3">
            <ul className="list-disc pl-5 space-y-2">
              <li>
                <span className="font-medium text-foreground">
                  This machine must stay online
                </span>{" "}
                with the hub unlocked so the local signer can answer the hosted
                node. Payments and receives stop if the signer is down.
              </li>
              <li>
                The Lightning node itself runs on{" "}
                <span className="font-medium text-foreground">
                  Blockstream Greenlight
                </span>
                . Blockstream can see channel and payment metadata (see the
                privacy notes in the Greenlight README). Funds cannot move
                without your local signer.
              </li>
              <li>
                Requires <code className="text-xs">glcli</code> 0.1.3+ on{" "}
                <code className="text-xs">PATH</code> (or{" "}
                <code className="text-xs">GREENLIGHT_GLCLI_PATH</code>). The hub
                checks the version at start and refuses to launch an
                incompatible binary.
              </li>
              <li>
                Hold invoices and <code className="text-xs">sign_message</code>{" "}
                are not supported. Channel open/close work via the hosted node.
              </li>
            </ul>
          </CardContent>
        </Card>
        <div className="flex items-start gap-3">
          <Checkbox
            id="ack-exp"
            checked={ackExperimental}
            onCheckedChange={(v) => setAckExperimental(v === true)}
          />
          <Label htmlFor="ack-exp" className="text-sm font-normal leading-snug">
            I understand Greenlight support is experimental and depends on
            Blockstream&apos;s hosted service.
          </Label>
        </div>
        <div className="flex items-start gap-3">
          <Checkbox
            id="ack-online"
            checked={ackOnline}
            onCheckedChange={(v) => setAckOnline(v === true)}
          />
          <Label
            htmlFor="ack-online"
            className="text-sm font-normal leading-snug"
          >
            I will keep this machine online while the hub is unlocked so the
            signer can process payments.
          </Label>
        </div>
        <Button onClick={next} disabled={!ackOnline || !ackExperimental}>
          Continue
        </Button>
      </div>
    </Container>
  );
}
