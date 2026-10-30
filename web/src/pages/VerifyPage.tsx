import { Message } from "@/components/Message";
import { CircleAlert } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { SelectControl } from "@/components/SelectControl";
import { FormEvent, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { isAddress } from "viem";

import { usePublicConfig } from "@/api/publicConfig";
import { useCompilerCatalog } from "@/api/hooks";
import { useSubmitVerification } from "@/api/browserVerification";
import { useAuth } from "@/auth/AuthProvider";
import { VerificationLogin, VerificationResults } from "./VerificationResults";
import type { BrowserVerificationJob, VerificationSubmission } from "@/api/types";
import { QueryNotice } from "@/components/QueryNotice";
import {
  DuplicateJSONKeyError,
  JSONStructureLimitError,
  Page,
  UnsafeJSONNumberError,
  assertNoDuplicateJSONKeys,
  errorMessage,
} from "./pages";

import { VerificationFileUpload } from "./VerificationFileUpload";
import { CompilerVersionSelect } from "./CompilerVersionSelect";
import {
  byteLength,
  fileMap,
  MAX_UPLOAD_BYTES,
  sourceExtensions,
  useVerificationFiles,
} from "./verificationFiles";

import { SourcifyWorkspace } from "./SourcifyWorkspace";
import { VerificationProviderPicker } from "./VerificationProviderPicker";
import "./VerificationWorkflow.css";

export function VerifyPage({
  initialAddress = "",
  initialJob = "",
  onJobChange,
}: {
  initialAddress?: string;
  initialJob?: string;
  onJobChange?: (id: string) => void;
}) {
  const { t } = useTranslation();
  const publicConfig = usePublicConfig();
  const [provider, setProvider] = useState("local");
  const auth = useAuth();
  const [jobID, setJobID] = useState(initialJob);
  useEffect(() => setJobID(initialJob), [initialJob]);
  const selectJob = (id: string) => {
    setJobID(id);
    onJobChange?.(id);
  };
  const accepted = (job: BrowserVerificationJob) => selectJob(job.id);
  const [address, setAddress] = useState(initialAddress);
  const [language, setLanguage] = useState<VerificationSubmission["language"]>("solidity");
  const [inputKind, setInputKind] = useState<VerificationSubmission["input_kind"]>("standard_json");
  const [compilerVersion, setCompilerVersion] = useState("");
  const [targetFile, setTargetFile] = useState("A.vy");
  const [optimizationMode, setOptimizationMode] = useState<"" | "none" | "gas" | "codesize">("");
  const [runtimeEntrypoint, setRuntimeEntrypoint] = useState("main.eas");
  const [creationEntrypoint, setCreationEntrypoint] = useState("");
  const [contractName, setContractName] = useState("");
  const [formError, setFormError] = useState<string>();
  const submissionEnabled =
    publicConfig.isSuccess && publicConfig.data.features.verification === true;
  const sourcifyEnabled =
    submissionEnabled && publicConfig.isSuccess && publicConfig.data.features.sourcify === true;
  const showSourcify = sourcifyEnabled && provider === "sourcify";
  const compilerCatalog = useCompilerCatalog(language, submissionEnabled && !showSourcify);
  // The API guarantees semantic ascending order; display newest releases first.
  const compilerVersions = useMemo(() => {
    const versions = [...(compilerCatalog.data?.versions ?? [])];
    return language === "geas" ? versions : versions.reverse();
  }, [compilerCatalog.data, language]);
  const submission = useSubmitVerification(address, accepted);

  useEffect(() => {
    setAddress(initialAddress);
  }, [initialAddress]);

  useEffect(() => {
    if (!compilerVersions.includes(compilerVersion)) {
      setCompilerVersion(compilerVersions[0] ?? "");
    }
  }, [compilerVersions, compilerVersion]);

  const optimizationModes = useMemo(
    () => compilerCatalog.data?.capabilities?.[compilerVersion]?.optimization_modes ?? [],
    [compilerCatalog.data, compilerVersion],
  );
  useEffect(() => {
    if (optimizationMode && !optimizationModes.includes(optimizationMode)) setOptimizationMode("");
  }, [optimizationMode, optimizationModes]);

  const standardInput = language !== "geas" && inputKind === "standard_json";
  const extensions = standardInput ? [".json"] : sourceExtensions[language];
  const upload = useVerificationFiles({
    extensions,
    single: standardInput,
    onChange: () => setFormError(undefined),
  });
  const busy = upload.reading || submission.isPending;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!submissionEnabled || !auth.session.authenticated || !auth.enabled || busy) return;
    setFormError(undefined);
    submission.reset();
    if (upload.error || !upload.files.length) {
      setFormError(upload.error ?? t("verificationUpload.required"));
      return;
    }
    const isGeas = language === "geas";
    const rawInput = standardInput
      ? upload.files[0]!.content
      : JSON.stringify(fileMap(upload.files));
    if (byteLength(rawInput) > MAX_UPLOAD_BYTES) {
      setFormError(t("verificationUpload.limit"));
      return;
    }

    let parsed: unknown;
    try {
      if (standardInput) assertNoDuplicateJSONKeys(rawInput);
      parsed = JSON.parse(rawInput) as unknown;
    } catch (cause) {
      if (cause instanceof DuplicateJSONKeyError) {
        setFormError(t("verification.duplicateJSONKey"));
        return;
      }
      if (cause instanceof JSONStructureLimitError) {
        setFormError(t("verification.inputTooComplex"));
        return;
      }
      if (cause instanceof UnsafeJSONNumberError) {
        setFormError(t("verification.unsafeJSONNumber"));
        return;
      }
      setFormError(t("verification.invalidJSON"));
      return;
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      setFormError(t("verification.invalidJSONObject"));
      return;
    }
    if (
      !isAddress(address) ||
      !compilerVersions.includes(compilerVersion) ||
      !compilerVersion.trim() ||
      (isGeas && !runtimeEntrypoint.trim()) ||
      (language === "vyper" && !targetFile.trim())
    ) {
      setFormError(t("verification.invalidFields"));
      return;
    }
    const request: VerificationSubmission = {
      compiler_version: compilerVersion.trim(),
      input_kind: isGeas ? "geas_sources" : inputKind,
      language,
    };
    if (isGeas) {
      request.sources = parsed as Record<string, string>;
      request.runtime_entrypoint = runtimeEntrypoint.trim();
      if (creationEntrypoint.trim()) request.creation_entrypoint = creationEntrypoint.trim();
      if (contractName.trim()) request.contract_name_hint = contractName.trim();
    } else if (inputKind === "standard_json") {
      request.input = parsed as Record<string, unknown>;
    } else {
      request.sources = parsed as Record<string, string>;
    }
    if (language === "vyper") {
      request.target_file = targetFile;
      if (inputKind === "multipart" && optimizationMode)
        request.optimization_mode = optimizationMode;
    }
    if (byteLength(JSON.stringify(request)) > MAX_UPLOAD_BYTES) {
      setFormError(t("verificationUpload.limit"));
      return;
    }
    submission.mutate(request);
  };

  return (
    <Page title={t("page.verify")} description={t("page.verifyDescription")}>
      <QueryNotice loading={publicConfig.isPending} error={publicConfig.error} />
      {publicConfig.isSuccess && !submissionEnabled && (
        <UnavailablePanel
          title={t("verification.unavailable")}
          detail={t("verification.unavailableDetail")}
        />
      )}
      <VerificationLogin />
      <div hidden={Boolean(jobID)}>
        {sourcifyEnabled && <VerificationProviderPicker value={provider} onChange={setProvider} />}
        {showSourcify && (
          <SourcifyWorkspace
            active={!jobID}
            onSubmitted={accepted}
            initialAddress={address}
            onAddressChange={setAddress}
            chainID={publicConfig.data!.chain_id}
          />
        )}
        {submissionEnabled && !showSourcify && (
          <div className="verification-layout">
            <form className="panel verification-form" autoComplete="off" onSubmit={submit}>
              <h2>{t("verification.request")}</h2>
              <p className="quiet">{t("verification.securityNotice")}</p>
              <div className="form-grid">
                <FormField
                  id="verification-address"
                  label={t("page.address")}
                  value={address}
                  onChange={setAddress}
                />
                <label className="field-control" htmlFor="verification-language">
                  <span>{t("verification.language")}</span>
                  <SelectControl
                    id="verification-language"
                    value={language}
                    onChange={(event) => {
                      const nextLanguage = event.target.value as VerificationSubmission["language"];
                      upload.clear();
                      setCompilerVersion("");
                      setLanguage(nextLanguage);
                      setInputKind((current) =>
                        nextLanguage === "geas"
                          ? "geas_sources"
                          : current === "geas_sources"
                            ? "standard_json"
                            : current,
                      );
                    }}
                  >
                    <option value="solidity">{t("verificationLanguage.solidity")}</option>
                    <option value="yul">{t("verificationLanguage.yul")}</option>
                    <option value="geas">{t("verificationLanguage.geas")}</option>
                    <option value="vyper">{t("verificationLanguage.vyper")}</option>
                  </SelectControl>
                </label>
                <label className="field-control" htmlFor="verification-input-kind">
                  <span>{t("verification.inputKind")}</span>
                  <SelectControl
                    disabled={language === "geas"}
                    id="verification-input-kind"
                    value={inputKind}
                    onChange={(event) => {
                      upload.clear();
                      setInputKind(event.target.value as VerificationSubmission["input_kind"]);
                    }}
                  >
                    {language === "geas" ? (
                      <option value="geas_sources">{t("verification.geasSources")}</option>
                    ) : (
                      <>
                        <option value="standard_json">{t("verification.standardJSON")}</option>
                        <option value="multipart">{t("verification.multipart")}</option>
                      </>
                    )}
                  </SelectControl>
                </label>
                <div className="field-control">
                  <label htmlFor="verification-compiler">{t("verification.compilerVersion")}</label>
                  {language === "geas" ? (
                    <SelectControl
                      disabled={compilerCatalog.isPending || !compilerCatalog.data}
                      id="verification-compiler"
                      onChange={(event) => setCompilerVersion(event.target.value)}
                      value={compilerVersion}
                    >
                      {compilerVersions.map((version) => (
                        <option key={version} value={version}>
                          {version}
                        </option>
                      ))}
                    </SelectControl>
                  ) : (
                    <CompilerVersionSelect
                      key={language}
                      id="verification-compiler"
                      versions={compilerVersions}
                      value={compilerVersion}
                      onChange={setCompilerVersion}
                      disabled={compilerCatalog.isPending || !compilerVersions.length}
                    />
                  )}
                  <QueryNotice loading={compilerCatalog.isPending} error={compilerCatalog.error} />
                </div>
                {language === "vyper" && (
                  <>
                    <FormField
                      id="verification-target-file"
                      label={t("verification.targetFile")}
                      value={targetFile}
                      onChange={setTargetFile}
                    />
                    {inputKind === "multipart" && (
                      <label className="field-control" htmlFor="verification-optimization-mode">
                        <span>{t("verification.optimizationMode")}</span>
                        <SelectControl
                          id="verification-optimization-mode"
                          value={optimizationMode}
                          onChange={(event) =>
                            setOptimizationMode(
                              event.target.value as "" | "none" | "gas" | "codesize",
                            )
                          }
                        >
                          <option value="">{t("verification.compilerDefault")}</option>
                          {optimizationModes.map((mode) => (
                            <option key={mode} value={mode}>
                              {mode}
                            </option>
                          ))}
                        </SelectControl>
                      </label>
                    )}
                  </>
                )}
                {language === "geas" ? (
                  <>
                    <FormField
                      id="verification-runtime-entrypoint"
                      label={t("verification.runtimeEntrypoint")}
                      onChange={setRuntimeEntrypoint}
                      value={runtimeEntrypoint}
                    />
                    <FormField
                      id="verification-creation-entrypoint"
                      label={t("verification.creationEntrypoint")}
                      onChange={setCreationEntrypoint}
                      value={creationEntrypoint}
                    />
                    <FormField
                      id="verification-contract-name"
                      label={t("verification.contractName")}
                      onChange={setContractName}
                      value={contractName}
                    />
                  </>
                ) : null}
                <div className="wide">
                  <VerificationFileUpload
                    id="verification-input"
                    label={t(verificationInputLabel(language, standardInput))}
                    files={upload.files}
                    extensions={extensions}
                    single={standardInput}
                    editablePaths={!standardInput}
                    disabled={busy}
                    onSelect={(files) => {
                      void upload.select(files);
                    }}
                    onRemove={upload.remove}
                    onRename={upload.rename}
                  />
                  <small>{t("verificationUpload.help")}</small>
                </div>
              </div>
              {(upload.error || formError) && (
                <p className="form-error" role="alert">
                  {upload.error || formError}
                </p>
              )}
              {submission.error && (
                <Message type="error">
                  {errorMessage(submission.error, t("verification.submitFailed"))}
                </Message>
              )}
              {!jobID && submission.challenge}
              <button
                className="button primary"
                disabled={!submission.ready || busy || !auth.enabled || !auth.session.authenticated}
                type="submit"
              >
                {upload.reading
                  ? t("sourcify.reading")
                  : submission.isPending
                    ? t("verification.submitting")
                    : t("verification.submit")}
              </button>
            </form>
          </div>
        )}
      </div>
      {jobID && (
        <VerificationResults jobID={jobID} onBack={() => selectJob("")} onSubmitted={accepted} />
      )}
    </Page>
  );
}

function FormField({
  id,
  label,
  value,
  onChange,
  wide,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  wide?: boolean;
}) {
  return (
    <label className={wide ? "field-control wide" : "field-control"} htmlFor={id}>
      <span>{label}</span>
      <input
        id={id}
        onChange={(event) => onChange(event.target.value)}
        spellCheck={false}
        value={value}
      />
    </label>
  );
}

function UnavailablePanel({ title, detail }: { title: string; detail: string }) {
  return (
    <section className="capability-panel" role="status">
      <span className="capability-mark" aria-hidden="true">
        <UIIcon icon={CircleAlert} size={24} />
      </span>
      <div>
        <h2>{title}</h2>
        <p>{detail}</p>
      </div>
    </section>
  );
}

function verificationInputLabel(language: string, standardInput: boolean) {
  return language === "geas"
    ? "verification.geasSources"
    : standardInput
      ? "verification.standardJSON"
      : "verification.multipartSources";
}
