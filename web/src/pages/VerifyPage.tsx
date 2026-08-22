import {
  FormEvent,
  useEffect,
  useId,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { isAddress, } from "viem";

import {
  usePublicConfig,
  useSubmitVerification,
  useCompilerCatalog,
  useVerificationJob,
} from "@/api/hooks";
import type {
  VerificationJob,
  VerificationMatchDetails,
  VerificationSuccess,
  VerificationSubmission,
} from "@/api/types";
import { QueryNotice } from "@/components/QueryNotice";
import {
  DuplicateJSONKeyError,
  JSONStructureLimitError,
  Page,
  TextArtifact,
  UnsafeJSONNumberError,
  assertNoDuplicateJSONKeys,
  errorMessage,
  verificationJobStatusLabel,
  verificationMatchLabel,
} from "./pages";

const MAX_STANDARD_JSON_BYTES = 5 * 1024 * 1024;
const UUID_PATTERN = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

export function VerifyPage({ initialAddress = "" }: { initialAddress?: string }) {
  const { t } = useTranslation();
  const publicConfig = usePublicConfig();
  const [apiKey, setAPIKey] = useState("");
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [address, setAddress] = useState(initialAddress);
  const [language, setLanguage] = useState<VerificationSubmission["language"]>("solidity");
  const [inputKind, setInputKind] = useState<VerificationSubmission["input_kind"]>("standard_json");
  const [compilerVersion, setCompilerVersion] = useState("");
  const [standardJSON, setStandardJSON] = useState('{\n  "language": "Solidity",\n  "sources": {},\n  "settings": {}\n}');
  const [multipartSources, setMultipartSources] = useState('{\n  "Contract.sol": "contract Contract {}"\n}');
  const [geasSources, setGeasSources] = useState('{\n  "main.eas": "push 1"\n}');
  const [runtimeEntrypoint, setRuntimeEntrypoint] = useState("main.eas");
  const [creationEntrypoint, setCreationEntrypoint] = useState("");
  const [contractName, setContractName] = useState("");
  const [formError, setFormError] = useState<string>();
  const submissionEnabled =
    publicConfig.isSuccess && publicConfig.data.features.verification === true;
  const compilerCatalog = useCompilerCatalog(language, submissionEnabled);
  const submission = useSubmitVerification(address, apiKey);
  const job = useVerificationJob(
    submission.data?.id ?? "",
    submittedAPIKey,
    submission.data ? 1 : 0,
    Boolean(submission.data),
  );
  const currentJob = job.data ?? submission.data;

  useEffect(() => {
    setAddress(initialAddress);
  }, [initialAddress]);

  useEffect(() => {
    const versions = compilerCatalog.data?.versions ?? [];
    if (versions.length > 0 && !versions.includes(compilerVersion)) {
      setCompilerVersion(versions[0] ?? "");
    }
  }, [compilerCatalog.data, compilerVersion]);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(undefined);
    submission.reset();
    if (!submissionEnabled) return;

    const isGeas = language === "geas";
    const rawInput = isGeas
      ? geasSources
      : inputKind === "standard_json"
        ? standardJSON
        : multipartSources;
    if (new TextEncoder().encode(rawInput).byteLength > MAX_STANDARD_JSON_BYTES) {
      setFormError(t("verification.inputTooLarge"));
      return;
    }

    let parsed: unknown;
    try {
      assertNoDuplicateJSONKeys(rawInput);
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
      !apiKey ||
      !isAddress(address) ||
      !compilerVersion.trim() ||
      (isGeas && !runtimeEntrypoint.trim())
    ) {
      setFormError(t("verification.invalidFields"));
      return;
    }
    if (
      (inputKind === "multipart" || isGeas) &&
      (Object.keys(parsed).length === 0 || Object.values(parsed).some((value) => typeof value !== "string"))
    ) {
      setFormError(t(isGeas ? "verification.invalidGeasSources" : "verification.invalidMultipart"));
      return;
    }

    setSubmittedAPIKey(apiKey);
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
    submission.mutate(request);
  };

  return (
    <Page title={t("page.verify")} description={t("page.verifyDescription")}>
      <QueryNotice loading={publicConfig.isPending} error={publicConfig.error} />
      {publicConfig.isSuccess && !submissionEnabled && (
        <UnavailablePanel title={t("verification.unavailable")} detail={t("verification.unavailableDetail")} />
      )}
      {submissionEnabled && (
        <div className="verification-layout">
          <form className="panel verification-form" autoComplete="off" onSubmit={submit}>
            <h2>{t("verification.request")}</h2>
            <p className="quiet">{t("verification.securityNotice")}</p>
            <div className="form-grid">
              <FormField id="verification-address" label={t("page.address")} value={address} onChange={setAddress} />
              <label className="field-control" htmlFor="verification-language">
                <span>{t("verification.language")}</span>
                <select
                  id="verification-language"
                  value={language}
                  onChange={(event) => {
                    const nextLanguage = event.target.value as VerificationSubmission["language"];
                    setLanguage(nextLanguage);
                    setInputKind((current) => nextLanguage === "geas"
                      ? "geas_sources"
                      : current === "geas_sources" ? "standard_json" : current);
                  }}
                >
                  <option value="solidity">{t("verificationLanguage.solidity")}</option>
                  <option value="yul">{t("verificationLanguage.yul")}</option>
                  <option value="geas">{t("verificationLanguage.geas")}</option>
                </select>
              </label>
              <label className="field-control" htmlFor="verification-input-kind">
                <span>{t("verification.inputKind")}</span>
                <select
                  disabled={language === "geas"}
                  id="verification-input-kind"
                  value={inputKind}
                  onChange={(event) => setInputKind(event.target.value as VerificationSubmission["input_kind"])}
                >
                  {language === "geas" ? (
                    <option value="geas_sources">{t("verification.geasSources")}</option>
                  ) : (
                    <>
                      <option value="standard_json">{t("verification.standardJSON")}</option>
                      <option value="multipart">{t("verification.multipart")}</option>
                    </>
                  )}
                </select>
              </label>
              <label className="field-control" htmlFor="verification-compiler">
                <span>{t("verification.compilerVersion")}</span>
                <select
                  disabled={compilerCatalog.isPending || !compilerCatalog.data}
                  id="verification-compiler"
                  onChange={(event) => setCompilerVersion(event.target.value)}
                  value={compilerVersion}
                >
                  {(compilerCatalog.data?.versions ?? []).map((version) => <option key={version} value={version}>{version}</option>)}
                </select>
                <QueryNotice loading={compilerCatalog.isPending} error={compilerCatalog.error} />
              </label>
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
              <label className="field-control wide" htmlFor="verification-input">
                <span>{language === "geas"
                  ? t("verification.geasSources")
                  : inputKind === "standard_json"
                    ? t("verification.standardJSON")
                    : t("verification.multipartSources")}</span>
                <textarea
                  id="verification-input"
                  spellCheck={false}
                  value={language === "geas"
                    ? geasSources
                    : inputKind === "standard_json" ? standardJSON : multipartSources}
                  onChange={(event) => language === "geas"
                    ? setGeasSources(event.target.value)
                    : inputKind === "standard_json"
                      ? setStandardJSON(event.target.value)
                      : setMultipartSources(event.target.value)}
                />
                <small>{t("verification.sizeLimit")}</small>
              </label>
              <label className="field-control wide" htmlFor="verification-api-key">
                <span>{t("verification.apiKey")}</span>
                <input
                  autoComplete="off"
                  id="verification-api-key"
                  name="verification-api-key"
                  onChange={(event) => setAPIKey(event.target.value)}
                  spellCheck={false}
                  type="password"
                  value={apiKey}
                />
                <small>{t("verification.apiKeyNotice")}</small>
              </label>
            </div>
            {(formError || submission.error) && (
              <p className="form-error" role="alert">{formError ?? errorMessage(submission.error, t("verification.submitFailed"))}</p>
            )}
            <button className="button primary" disabled={submission.isPending} type="submit">
              {submission.isPending ? t("verification.submitting") : t("verification.submit")}
            </button>
          </form>
          <VerificationJobPanel job={currentJob} loading={job.isPending && Boolean(submission.data)} error={job.error} />
        </div>
      )}
      <VerificationJobLookup />
    </Page>
  );
}

function VerificationJobLookup() {
  const { t } = useTranslation();
  const [jobID, setJobID] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const [submittedJobID, setSubmittedJobID] = useState("");
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [requestRevision, setRequestRevision] = useState(0);
  const [formError, setFormError] = useState<string>();
  const job = useVerificationJob(
    submittedJobID,
    submittedAPIKey,
    requestRevision,
    requestRevision > 0,
  );

  const load = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(undefined);
    if (!UUID_PATTERN.test(jobID) || !apiKey) {
      setFormError(t("verification.invalidJobLookup"));
      return;
    }
    setSubmittedJobID(jobID.toLowerCase());
    setSubmittedAPIKey(apiKey);
    setRequestRevision((current) => current + 1);
  };

  return (
    <div className="verification-read-layout">
      <form className="panel verification-job-lookup" autoComplete="off" onSubmit={load}>
        <h2>{t("verification.openJob")}</h2>
        <p className="quiet">{t("verification.readNotice")}</p>
        <FormField
          id="verification-job-lookup-id"
          label={t("verification.jobID")}
          onChange={setJobID}
          value={jobID}
        />
        <label className="field-control" htmlFor="verification-job-lookup-api-key">
          <span>{t("verification.jobAPIKey")}</span>
          <input
            autoComplete="off"
            id="verification-job-lookup-api-key"
            name="verification-job-lookup-api-key"
            onChange={(event) => setAPIKey(event.target.value)}
            spellCheck={false}
            type="password"
            value={apiKey}
          />
        </label>
        {formError && <p className="form-error" role="alert">{formError}</p>}
        <button className="button primary" type="submit">{t("verification.loadJob")}</button>
      </form>
      <VerificationJobPanel
        emptyMessage={t("verification.lookupEmpty")}
        error={job.error}
        job={job.data}
        loading={job.isPending && requestRevision > 0}
      />
    </div>
  );
}

function FormField({ id, label, value, onChange, wide }: { id: string; label: string; value: string; onChange: (value: string) => void; wide?: boolean }) {
  return (
    <label className={wide ? "field-control wide" : "field-control"} htmlFor={id}>
      <span>{label}</span>
      <input id={id} onChange={(event) => onChange(event.target.value)} spellCheck={false} value={value} />
    </label>
  );
}

function VerificationJobPanel({
  emptyMessage,
  error,
  job,
  loading,
}: {
  emptyMessage?: string;
  error: unknown;
  job?: VerificationJob;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const headingID = useId();
  const success = job?.outcome?.kind === "verification_success"
    ? job.outcome as VerificationSuccess
    : undefined;
  return (
    <section className="panel job-panel" aria-labelledby={headingID}>
      <h2 id={headingID}>{t("verification.job")}</h2>
      {!job && !loading && !error && (
        <p className="quiet">{emptyMessage ?? t("verification.jobEmpty")}</p>
      )}
      <QueryNotice loading={loading} error={error} />
      {job && (
        <dl className="job-details" aria-live="polite">
          <div><dt>{t("verification.jobID")}</dt><dd><code>{job.id}</code></dd></div>
          <div><dt>{t("verification.jobKind")}</dt><dd><code>{job.kind}</code></dd></div>
          <div><dt>{t("table.status")}</dt><dd><span className={`job-status ${job.status}`}>{verificationJobStatusLabel(job.status, t)}</span></dd></div>
          <div><dt>{t("verification.result")}</dt><dd><code>{job.outcome?.kind ?? "—"}</code></dd></div>
          <div><dt>{t("verification.runtimeMatch")}</dt><dd>{verificationMatchLabel(success?.runtime_match?.match_type, t)}</dd></div>
          <div><dt>{t("verification.creationMatch")}</dt><dd>{verificationMatchLabel(success?.creation_match?.match_type, t)}</dd></div>
          <div><dt>{t("verification.errorCode")}</dt><dd><code>{job.error_code ?? "—"}</code></dd></div>
          <div><dt>{t("verification.updated")}</dt><dd>{job.updated_at}</dd></div>
        </dl>
      )}
      {success?.creation_match && (
        <VerificationMatchView title={t("verification.creationTransformations")} match={success.creation_match} />
      )}
      {success?.runtime_match && (
        <VerificationMatchView title={t("verification.runtimeTransformations")} match={success.runtime_match} />
      )}
      {job?.outcome?.kind === "batch_results" && (
        <TextArtifact title={t("verification.batchResults")} value={job.outcome.results} />
      )}
    </section>
  );
}

function VerificationMatchView({ title, match }: { title: string; match: VerificationMatchDetails }) {
  return (
    <section className="artifact-panel">
      <h3>{title}</h3>
      <p><code>{match.match_type}</code></p>
      <pre tabIndex={0}>{JSON.stringify({
        transformations: match.transformations,
        values: match.values,
      }, null, 2)}</pre>
    </section>
  );
}

function UnavailablePanel({ title, detail }: { title: string; detail: string }) {
  return (
    <section className="capability-panel" role="status">
      <span className="capability-mark" aria-hidden="true">!</span>
      <div><h2>{title}</h2><p>{detail}</p></div>
    </section>
  );
}
