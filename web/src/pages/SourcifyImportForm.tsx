import { useState, type FormEvent } from "react";
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

  const submit = (event: FormEvent) => {
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
      <form className="panel verification-form" autoComplete="off" onSubmit={submit}>
        <div className="verification-flow-heading">
          <Download aria-hidden="true" />
          <h2>{t("sourcify.importTitle")}</h2>
        </div>
        <p className="quiet">{t("sourcify.importHelp", { chain: chainID })}</p>
        <div className="verification-flow-steps" aria-label={t("sourcify.importSteps")}>
          <span>Sourcify</span>
          <ArrowRight aria-hidden="true" />
          <span>{t("sourcify.localCheck")}</span>
          <ArrowRight aria-hidden="true" />
          <span>Etherview</span>
        </div>
        <div className="form-grid">
          <label className="field-control wide" htmlFor="sourcify-import-chain">
            <span id="sourcify-import-chain-label">{t("sourcify.sourceChainID")}</span>
            <input
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
          </label>
          <label className="field-control wide" htmlFor="sourcify-import-address">
            <span>{t("page.address")}</span>
            <input
              id="sourcify-import-address"
              value={address}
              disabled={submission.isPending}
              onChange={(event) => onAddressChange(event.target.value)}
              spellCheck={false}
            />
          </label>
        </div>
        {(formError || submission.error) && (
          <p className="form-error" role="alert">
            {formError ?? errorMessage(submission.error, t("sourcify.importFailed"))}
          </p>
        )}
        {active && submission.challenge}
        <button
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
        </button>
        <p className="quiet">
          {t("sourcify.notPublished")}{" "}
          <button type="button" className="button" onClick={onPublish}>
            {t("sourcify.publishTab")}
          </button>
        </p>
      </form>
    </div>
  );
}
