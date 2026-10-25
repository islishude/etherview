import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { isAddress } from "viem";
import { useSubmitSourcifyVerification } from "@/api/browserVerification";
import { assertNoDuplicateJSONKeys, errorMessage } from "./pages";
import { useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity } from "@/api/privateAccount";
import type { BrowserVerificationJob } from "@/api/types";
import { VerificationFileUpload } from "./VerificationFileUpload";
import {
  byteLength,
  fileMap,
  MAX_UPLOAD_BYTES,
  MAX_UPLOAD_FILES,
  sourceExtensions,
  useVerificationFiles,
} from "./verificationFiles";

export function SourcifyVerificationForm({
  onSubmitted,
  initialAddress,
  chainID,
  onAddressChange,
}: {
  onSubmitted: (job: BrowserVerificationJob) => void;
  initialAddress: string;
  chainID: string;
  onAddressChange?: (address: string) => void;
}) {
  const { t } = useTranslation();
  const auth = useAuth();
  const identity = usePrivateIdentity();
  const [mode, setMode] = useState<"sources" | "etherscan">("sources");
  const [address, setAddress] = useState(initialAddress);
  const [etherscanAPIKey, setEtherscanAPIKey] = useState("");
  const [consent, setConsent] = useState(false);
  const [formError, setFormError] = useState<string>();
  const submission = useSubmitSourcifyVerification(onSubmitted);

  const changedFiles = () => {
    setConsent(false);
    setFormError(undefined);
  };
  const sources = useVerificationFiles({
    extensions: sourceExtensions.sourcify,
    onChange: changedFiles,
  });
  const metadata = useVerificationFiles({
    extensions: [".json"],
    single: true,
    onChange: changedFiles,
    validateContent: (content) => {
      try {
        assertNoDuplicateJSONKeys(content);
        const parsed: unknown = JSON.parse(content);
        if (
          !parsed ||
          typeof parsed !== "object" ||
          Array.isArray(parsed) ||
          !["compiler", "settings", "output"].every(
            (key) => key in parsed && parsed[key as keyof typeof parsed] != null,
          )
        )
          return t("sourcify.invalidMetadata");
      } catch {
        return t("sourcify.invalidMetadata");
      }
    },
  });
  const reading = sources.reading || metadata.reading;
  const busy = reading || submission.isPending;
  const uploadError = mode === "sources" ? (sources.error ?? metadata.error) : undefined;

  useEffect(() => {
    setAddress(initialAddress);
    setConsent(false);
  }, [initialAddress]);

  useEffect(() => {
    setEtherscanAPIKey("");
    setConsent(false);
  }, [identity]);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || !auth.enabled || !auth.session.authenticated) return;
    setFormError(undefined);
    if (!consent || !isAddress(address)) {
      setFormError(t("sourcify.invalidFields"));
      return;
    }
    const target = { chain_id: chainID, address };
    if (mode === "etherscan" && etherscanAPIKey && !/^[!-~]{1,256}$/.test(etherscanAPIKey)) {
      setFormError(t("sourcify.invalidEtherscanKey"));
      return;
    }
    let sourceFiles: Record<string, string> = {};
    if (mode === "sources") {
      if (uploadError || !sources.files.length || !metadata.files.length) {
        setFormError(uploadError ?? t("verificationUpload.requireSourcesMetadata"));
        return;
      }
      sourceFiles = { ...fileMap(sources.files), "metadata.json": metadata.files[0]!.content };
      if (
        Object.keys(sourceFiles).length > MAX_UPLOAD_FILES ||
        byteLength(JSON.stringify({ ...target, files: sourceFiles })) > MAX_UPLOAD_BYTES
      ) {
        setFormError(t("verificationUpload.limit"));
        return;
      }
    }
    setConsent(false);
    submission.mutate(
      mode === "sources"
        ? { kind: "sources", body: { ...target, files: sourceFiles } }
        : {
            kind: "etherscan",
            body: { ...target, ...(etherscanAPIKey ? { etherscan_api_key: etherscanAPIKey } : {}) },
          },
    );
    setEtherscanAPIKey("");
  };

  return (
    <div className="verification-layout">
      <form className="panel verification-form" autoComplete="off" onSubmit={submit}>
        <h2>{t("verification.sourcifyProvider")}</h2>
        <p className="chain-warning" id="sourcify-publication-notice">
          {t("sourcify.publicationNotice")}
        </p>
        <p className="quiet">{t("sourcify.localBoundary")}</p>
        <div className="form-grid">
          <label className="field-control" htmlFor="sourcify-mode">
            <span>{t("sourcify.method")}</span>
            <select
              id="sourcify-mode"
              disabled={busy}
              value={mode}
              onChange={(event) => {
                setMode(event.target.value as typeof mode);
                setEtherscanAPIKey("");
                setConsent(false);
                setFormError(undefined);
              }}
            >
              <option value="sources">{t("sourcify.sources")}</option>
              <option value="etherscan">{t("sourcify.etherscan")}</option>
            </select>
          </label>
          <label className="field-control" htmlFor="sourcify-chain">
            <span>{t("sourcify.chainID")}</span>
            <input id="sourcify-chain" value={chainID} readOnly />
          </label>
          <label className="field-control wide" htmlFor="sourcify-address">
            <span>{t("page.address")}</span>
            <input
              id="sourcify-address"
              disabled={busy}
              value={address}
              spellCheck={false}
              onChange={(event) => {
                setAddress(event.target.value);
                onAddressChange?.(event.target.value);
                setConsent(false);
              }}
            />
          </label>
          {mode === "sources" ? (
            <>
              <div className="sourcify-uploads wide">
                <VerificationFileUpload
                  id="sourcify-upload-sources"
                  label={t("sourcify.selectSources")}
                  files={sources.files}
                  extensions={sourceExtensions.sourcify}
                  editablePaths
                  disabled={busy}
                  onSelect={(selected) => {
                    void sources.select(
                      selected,
                      metadata.files.map((file) => ({ ...file, path: "metadata.json" })),
                    );
                  }}
                  onRemove={sources.remove}
                  onRename={sources.rename}
                />
                <VerificationFileUpload
                  id="sourcify-upload-metadata"
                  label={t("sourcify.selectMetadata")}
                  files={metadata.files}
                  extensions={[".json"]}
                  single
                  disabled={busy}
                  onSelect={(selected) => {
                    void metadata.select(selected, sources.files);
                  }}
                  onRemove={metadata.remove}
                />
              </div>
              <small className="wide">{t("sourcify.filesHelp")}</small>
            </>
          ) : (
            <>
              <p className="quiet wide">{t("sourcify.etherscanHelp")}</p>
              <label className="field-control wide" htmlFor="sourcify-etherscan-api-key">
                <span>{t("sourcify.etherscanAPIKey")}</span>
                <input
                  id="sourcify-etherscan-api-key"
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  disabled={busy}
                  maxLength={256}
                  value={etherscanAPIKey}
                  onChange={(event) => {
                    setEtherscanAPIKey(event.target.value);
                    setConsent(false);
                  }}
                />
                <small>{t("sourcify.etherscanKeyNotice")}</small>
              </label>
            </>
          )}

          <label className="checkbox-control wide">
            <input
              type="checkbox"
              checked={consent}
              disabled={busy}
              aria-describedby="sourcify-publication-notice"
              onChange={(event) => setConsent(event.target.checked)}
            />
            <span>{t("sourcify.consent")}</span>
          </label>
        </div>
        {(uploadError || formError || submission.error) && (
          <p className="form-error" role="alert">
            {uploadError ??
              formError ??
              errorMessage(submission.error, t("verification.submitFailed"))}
          </p>
        )}
        <button
          className="button primary"
          type="submit"
          disabled={busy || !consent || !auth.enabled || !auth.session.authenticated}
        >
          {reading
            ? t("sourcify.reading")
            : submission.isPending
              ? t("verification.submitting")
              : mode === "sources"
                ? t("sourcify.submitSources")
                : t("sourcify.submitEtherscan")}
        </button>
      </form>
    </div>
  );
}
