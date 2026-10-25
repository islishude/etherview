import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { isAddress } from "viem";
import { useSubmitSourcifyVerification, useVerificationJob } from "@/api/hooks";
import { assertNoDuplicateJSONKeys, errorMessage } from "./pages";
import { VerificationJobPanel } from "./VerificationJobPanel";
import { SourcifyPostImport } from "./SourcifyPostImport";
import { SourcifyFileUpload } from "./SourcifyFileUpload";

const MAX_REQUEST_BYTES = 5 * 1024 * 1024;
const MAX_FILES = 1024;
const byteLength = (value: string) => new TextEncoder().encode(value).byteLength;

export function SourcifyVerificationForm({
  initialAddress,
  chainID,
  onAddressChange,
}: {
  initialAddress: string;
  chainID: string;
  onAddressChange?: (address: string) => void;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"sources" | "etherscan">("sources");
  const [address, setAddress] = useState(initialAddress);
  const [files, setFiles] = useState("{}");
  const [apiKey, setAPIKey] = useState("");
  const [etherscanAPIKey, setEtherscanAPIKey] = useState("");
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [submittedAddress, setSubmittedAddress] = useState("");
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

  let selectedNames: string[] = [];
  try {
    const parsed: unknown = JSON.parse(files);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed))
      selectedNames = Object.keys(parsed);
  } catch {
    /* The editable map reports validation errors on submit. */
  }

  const readFiles = async (selected: File[], kind: "sources" | "metadata") => {
    setConsent(false);
    setFormError(undefined);
    if (!selected.length) return;
    if (
      selected.length > MAX_FILES ||
      selected.reduce((total, file) => total + file.size, 0) > MAX_REQUEST_BYTES
    ) {
      setFormError(t("sourcify.inputLimit"));
      return;
    }
    const paths = selected.map((file) => file.webkitRelativePath || file.name);
    if (
      kind === "sources" &&
      paths.some((path) => path.split("/").pop()?.toLowerCase() === "metadata.json")
    ) {
      setFormError(t("sourcify.separateMetadata"));
      return;
    }
    if (kind === "metadata" && selected.length !== 1) {
      setFormError(t("sourcify.invalidMetadata"));
      return;
    }
    if (new Set(paths).size !== paths.length) {
      setFormError(t("sourcify.duplicateFiles"));
      return;
    }
    setReading(true);
    try {
      assertNoDuplicateJSONKeys(files);
      const current: unknown = JSON.parse(files);
      if (
        !current ||
        typeof current !== "object" ||
        Array.isArray(current) ||
        Object.values(current).some((content) => typeof content !== "string")
      ) {
        setFormError(t("sourcify.invalidFiles"));
        return;
      }
      const contents = await Promise.all(selected.map((file) => file.text()));
      let next: Record<string, string>;
      if (kind === "metadata") {
        try {
          assertNoDuplicateJSONKeys(contents[0]!);
          const metadata: unknown = JSON.parse(contents[0]!);
          if (
            !metadata ||
            typeof metadata !== "object" ||
            Array.isArray(metadata) ||
            !["compiler", "settings", "output"].every(
              (key) => key in metadata && metadata[key as keyof typeof metadata] != null,
            )
          )
            throw new Error();
        } catch {
          setFormError(t("sourcify.invalidMetadata"));
          return;
        }
        next = { ...(current as Record<string, string>), "metadata.json": contents[0]! };
      } else {
        next = Object.fromEntries(paths.map((path, index) => [path, contents[index]!]));
        if ("metadata.json" in current)
          next["metadata.json"] = (current as Record<string, string>)["metadata.json"]!;
      }
      const encoded = JSON.stringify(next, null, 2);
      if (Object.keys(next).length > MAX_FILES || byteLength(encoded) > MAX_REQUEST_BYTES) {
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
    if (mode === "etherscan" && etherscanAPIKey && !/^[!-~]{1,256}$/.test(etherscanAPIKey)) {
      setFormError(t("sourcify.invalidEtherscanKey"));
      return;
    }
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
    setSubmittedAddress(address);
    setConsent(false);
    submission.mutate(
      mode === "sources"
        ? { kind: "sources", body: { ...target, files: sourceFiles } }
        : {
            kind: "etherscan",
            body: { ...target, ...(etherscanAPIKey ? { etherscan_api_key: etherscanAPIKey } : {}) },
          },
      { onSettled: () => setEtherscanAPIKey("") },
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
                <SourcifyFileUpload
                  kind="sources"
                  names={selectedNames.filter((name) => name !== "metadata.json")}
                  disabled={busy}
                  onSelect={(selected) => {
                    void readFiles(selected, "sources");
                  }}
                />
                <SourcifyFileUpload
                  kind="metadata"
                  names={selectedNames.filter((name) => name === "metadata.json")}
                  disabled={busy}
                  onSelect={(selected) => {
                    void readFiles(selected, "metadata");
                  }}
                />
              </div>
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
      <div className="sourcify-import-result">
        <VerificationJobPanel
          job={job.data ?? submission.data}
          loading={job.isPending && Boolean(submission.data)}
          error={job.error}
        />
        {(job.data ?? submission.data)?.status === "succeeded" && (
          <SourcifyPostImport
            key={submission.data?.id}
            address={submittedAddress}
            apiKey={submittedAPIKey}
            chainID={chainID}
          />
        )}
      </div>
    </div>
  );
}
