import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { isAddress } from "viem";
import { useSubmitSourcifyVerification, useVerificationJob } from "@/api/hooks";
import { assertNoDuplicateJSONKeys, errorMessage } from "./pages";
import { VerificationJobPanel } from "./VerificationJobPanel";

const MAX_REQUEST_BYTES = 5 * 1024 * 1024;
const MAX_FILES = 1024;
const byteLength = (value: string) => new TextEncoder().encode(value).byteLength;

export function SourcifyVerificationForm({
  initialAddress,
  chainID,
}: {
  initialAddress: string;
  chainID: string;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"sources" | "etherscan">("sources");
  const [address, setAddress] = useState(initialAddress);
  const [files, setFiles] = useState("{}");
  const [apiKey, setAPIKey] = useState("");
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [consent, setConsent] = useState(false);
  const [reading, setReading] = useState(false);
  const [formError, setFormError] = useState<string>();
  const submission = useSubmitSourcifyVerification(apiKey);
  const job = useVerificationJob(
    submission.data?.id ?? "",
    submittedAPIKey,
    1,
    Boolean(submission.data),
  );
  const busy = reading || submission.isPending;

  useEffect(() => {
    setAddress(initialAddress);
    setConsent(false);
  }, [initialAddress]);

  const readFiles = async (selected: File[]) => {
    setConsent(false);
    setFormError(undefined);
    if (!selected.length) return;
    setFiles("{}");
    if (
      selected.length > MAX_FILES ||
      selected.reduce((total, file) => total + file.size, 0) > MAX_REQUEST_BYTES
    ) {
      setFormError(t("sourcify.inputLimit"));
      return;
    }
    const paths = selected.map((file) => file.webkitRelativePath || file.name);
    if (new Set(paths).size !== paths.length) {
      setFormError(t("sourcify.duplicateFiles"));
      return;
    }
    setReading(true);
    try {
      const contents = await Promise.all(selected.map((file) => file.text()));
      const encoded = JSON.stringify(
        Object.fromEntries(paths.map((path, index) => [path, contents[index]])),
        null,
        2,
      );
      if (byteLength(encoded) > MAX_REQUEST_BYTES) {
        setFormError(t("sourcify.inputLimit"));
        return;
      }
      setFiles(encoded);
    } catch {
      setFormError(t("sourcify.readFailed"));
    } finally {
      setReading(false);
    }
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) return;
    setFormError(undefined);
    if (!consent || !apiKey.trim() || !isAddress(address)) {
      setFormError(t("sourcify.invalidFields"));
      return;
    }
    const target = { chain_id: chainID, address };
    let sourceFiles: Record<string, string> = {};
    if (mode === "sources") {
      if (byteLength(files) > MAX_REQUEST_BYTES) {
        setFormError(t("sourcify.inputLimit"));
        return;
      }
      try {
        assertNoDuplicateJSONKeys(files);
        const parsed: unknown = JSON.parse(files);
        if (
          !parsed ||
          typeof parsed !== "object" ||
          Array.isArray(parsed) ||
          Object.keys(parsed).length === 0 ||
          Object.keys(parsed).length > MAX_FILES ||
          Object.values(parsed).some((content) => typeof content !== "string")
        ) {
          setFormError(t("sourcify.invalidFiles"));
          return;
        }
        sourceFiles = parsed as Record<string, string>;
      } catch {
        setFormError(t("sourcify.invalidFiles"));
        return;
      }
      if (byteLength(JSON.stringify({ ...target, files: sourceFiles })) > MAX_REQUEST_BYTES) {
        setFormError(t("sourcify.inputLimit"));
        return;
      }
    }
    setSubmittedAPIKey(apiKey);
    setConsent(false);
    submission.mutate(
      mode === "sources"
        ? { kind: "sources", body: { ...target, files: sourceFiles } }
        : { kind: "etherscan", body: target },
    );
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
                setConsent(false);
              }}
            />
          </label>
          {mode === "sources" ? (
            <>
              <label className="field-control wide" htmlFor="sourcify-upload">
                <span>{t("sourcify.selectFiles")}</span>
                <input
                  id="sourcify-upload"
                  disabled={busy}
                  type="file"
                  multiple
                  onChange={(event) => {
                    void readFiles(Array.from(event.target.files ?? []));
                    event.target.value = "";
                  }}
                />
              </label>
              <label className="field-control wide" htmlFor="sourcify-files">
                <span>{t("sourcify.files")}</span>
                <textarea
                  id="sourcify-files"
                  disabled={busy}
                  value={files}
                  spellCheck={false}
                  onChange={(event) => {
                    setFiles(event.target.value);
                    setConsent(false);
                  }}
                />
                <small>{t("sourcify.filesHelp")}</small>
              </label>
            </>
          ) : (
            <p className="quiet wide">{t("sourcify.etherscanHelp")}</p>
          )}
          <label className="field-control wide" htmlFor="sourcify-api-key">
            <span>{t("verification.apiKey")}</span>
            <input
              id="sourcify-api-key"
              type="password"
              autoComplete="off"
              spellCheck={false}
              disabled={busy}
              value={apiKey}
              onChange={(event) => setAPIKey(event.target.value)}
            />
            <small>{t("verification.apiKeyNotice")}</small>
          </label>
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
        {(formError || submission.error) && (
          <p className="form-error" role="alert">
            {formError ?? errorMessage(submission.error, t("verification.submitFailed"))}
          </p>
        )}
        <button className="button primary" type="submit" disabled={busy || !consent}>
          {reading
            ? t("sourcify.reading")
            : submission.isPending
              ? t("verification.submitting")
              : mode === "sources"
                ? t("sourcify.submitSources")
                : t("sourcify.submitEtherscan")}
        </button>
      </form>
      <VerificationJobPanel
        job={job.data ?? submission.data}
        loading={job.isPending && Boolean(submission.data)}
        error={job.error}
      />
    </div>
  );
}
