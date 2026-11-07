import { Card } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Message } from "@/components/Message";
import { UIIcon } from "@/components/UIIcon";
import { useState, type SubmitEvent } from "react";
import { useTranslation } from "react-i18next";
import { isAddress } from "viem";
import { Download, ArrowRight } from "lucide-react";
import { useImportSourcifyContract } from "@/api/browserVerification";
import { errorMessage } from "./pages";
import { useAuth } from "@/auth/AuthProvider";
import type { BrowserVerificationJob } from "@/api/types";

export function SourcifyImportForm({
  active = true,
  onSubmitted,
  initialAddress,
  chainID,
  onPublish,
  onAddressChange,
}: {
  active?: boolean;
  onSubmitted: (job: BrowserVerificationJob) => void;
  initialAddress: string;
  chainID: string;
  onPublish: () => void;
  onAddressChange: (address: string) => void;
}) {
  const { t } = useTranslation();
  const auth = useAuth();
  const address = initialAddress;
  const [sourceChainID, setSourceChainID] = useState(chainID);
  const [formError, setFormError] = useState<string>();
  const submission = useImportSourcifyContract(onSubmitted);

  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submission.isPending || !auth.enabled || !auth.session.authenticated) return;
    setFormError(undefined);
    if (
      !/^[1-9][0-9]{0,19}$/.test(sourceChainID) ||
      BigInt(sourceChainID) > 18446744073709551615n
    ) {
      setFormError(t("sourcify.invalidChainID"));
      return;
    }
    if (!isAddress(address)) {
      setFormError(t("sourcify.importInvalid"));
      return;
    }
    submission.mutate({ address, chainID: sourceChainID });
  };
  return (
    <div className="verification-layout">
      <Card asChild>
        <form className="panel verification-form" autoComplete="off" onSubmit={submit}>
          <div className="verification-flow-heading">
            <UIIcon icon={Download} />
            <h2>{t("sourcify.importTitle")}</h2>
          </div>
          <p className="quiet">{t("sourcify.importHelp", { chain: chainID })}</p>
          <div className="verification-flow-steps" aria-label={t("sourcify.importSteps")}>
            <span>Sourcify</span>
            <UIIcon icon={ArrowRight} />
            <span>{t("sourcify.localCheck")}</span>
            <UIIcon icon={ArrowRight} />
            <span>Etherview</span>
          </div>
          <div className="form-grid">
            <Label className="field-control wide" htmlFor="sourcify-import-chain">
              <span id="sourcify-import-chain-label">{t("sourcify.sourceChainID")}</span>
              <Input
                id="sourcify-import-chain"
                value={sourceChainID}
                inputMode="numeric"
                maxLength={20}
                disabled={submission.isPending}
                onChange={(event) => setSourceChainID(event.target.value)}
                aria-describedby="sourcify-import-chain-help"
                aria-labelledby="sourcify-import-chain-label"
                spellCheck={false}
              />
              <small id="sourcify-import-chain-help">
                {t("sourcify.sourceChainHelp", { chain: chainID })}
              </small>
            </Label>
            <Label className="field-control wide" htmlFor="sourcify-import-address">
              <span>{t("page.address")}</span>
              <Input
                id="sourcify-import-address"
                value={address}
                disabled={submission.isPending}
                onChange={(event) => onAddressChange(event.target.value)}
                spellCheck={false}
              />
            </Label>
          </div>
          {formError && (
            <p className="form-error" role="alert">
              {formError}
            </p>
          )}
          {submission.error && (
            <Message type="error">
              {errorMessage(submission.error, t("sourcify.importFailed"))}
            </Message>
          )}
          {active && submission.challenge}
          <Button
            className="button primary"
            type="submit"
            disabled={
              !submission.ready ||
              submission.isPending ||
              !auth.enabled ||
              !auth.session.authenticated
            }
          >
            {submission.isPending ? t("sourcify.importing") : t("sourcify.importAction")}
          </Button>
          <p className="quiet">
            {t("sourcify.notPublished")}{" "}
            <Button variant="ghost" type="button" className="button" onClick={onPublish}>
              {t("sourcify.publishTab")}
            </Button>
          </p>
        </form>
      </Card>
    </div>
  );
}
