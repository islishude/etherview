import { ChevronRight } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import "@/i18n/resources/sourcify";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import type { BrowserVerificationJob, VerificationMatchDetails } from "@/api/types";
import { QueryNotice } from "@/components/QueryNotice";
import { verificationJobStatusLabel, verificationMatchLabel } from "./pages";

export function VerificationJobPanel({
  emptyMessage,
  error,
  job,
  loading,
}: {
  emptyMessage?: string;
  error: unknown;
  job?: BrowserVerificationJob;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const headingID = useId();
  const external = job?.kind === "sourcify" || job?.kind === "sourcify_from_etherscan";
  const rejected =
    job?.outcome?.kind === "compilation_failure" || job?.outcome?.kind === "verification_failure";
  const status = rejected ? "failed" : job?.status;
  const externalCompleted = external && status === "succeeded";
  const success =
    !external && job?.outcome?.kind === "verification_success" ? job.outcome : undefined;
  return (
    <section className="panel job-panel" aria-labelledby={headingID}>
      <h2 id={headingID}>{t("verification.job")}</h2>
      {job && (
        <p className="quiet">
          {external ? t("sourcify.localBoundary") : t("verification.localResultNotice")}
        </p>
      )}
      {!job && !loading && !error && (
        <p className="quiet">{emptyMessage ?? t("verification.jobEmpty")}</p>
      )}
      <QueryNotice loading={loading} error={error} />
      {job && (
        <div className="verification-result-summary" aria-live="polite">
          <strong className={`job-status ${status}`}>
            {externalCompleted ? t("sourcify.completed") : verificationJobStatusLabel(status!, t)}
          </strong>
          <p>{t(resultSummary(job, external))}</p>
          {job.address && (
            <p>
              <code>{job.address}</code> · {t("sourcify.chainID")}: {job.chain_id}
            </p>
          )}
          {!external && success && job.address && (
            <a className="button primary" href={`/address/${job.address}#code`}>
              {t("verification.viewContract")}
            </a>
          )}
        </div>
      )}
      {job && (
        <details className="verification-technical-details">
          <summary>
            <UIIcon icon={ChevronRight} className="native-disclosure-icon" />
            {t("verification.technicalDetails")}
          </summary>
          {job && (
            <dl className="job-details" aria-live="polite">
              <div>
                <dt>{t("verification.provider")}</dt>
                <dd>
                  {external ? t("verification.sourcifyProvider") : t("verification.localProvider")}
                </dd>
              </div>
              <div>
                <dt>{t("verification.jobID")}</dt>
                <dd>
                  <code>{job.id}</code>
                </dd>
              </div>
              <div>
                <dt>{t("verification.jobKind")}</dt>
                <dd>
                  <code>{job.kind}</code>
                </dd>
              </div>
              <div>
                <dt>{t("verification.result")}</dt>
                <dd>
                  <code>{job.outcome?.kind ?? "—"}</code>
                </dd>
              </div>
              {external && job.outcome?.kind === "sourcify_success" && (
                <div>
                  <dt>{t("sourcify.externalID")}</dt>
                  <dd>
                    <code>{job.outcome.verification_id}</code>
                  </dd>
                </div>
              )}
              {!external && (
                <>
                  <div>
                    <dt>{t("verification.runtimeMatch")}</dt>
                    <dd>{verificationMatchLabel(success?.runtime_match?.match_type, t)}</dd>
                  </div>
                  <div>
                    <dt>{t("verification.creationMatch")}</dt>
                    <dd>{verificationMatchLabel(success?.creation_match?.match_type, t)}</dd>
                  </div>
                </>
              )}
              <div>
                <dt>{t("verification.errorCode")}</dt>
                <dd>
                  <code>{job.error_code ?? "—"}</code>
                </dd>
              </div>
              <div>
                <dt>{t("verification.updated")}</dt>
                <dd>{job.updated_at}</dd>
              </div>
            </dl>
          )}
          {success?.creation_match && (
            <VerificationMatchView
              title={t("verification.creationTransformations")}
              match={success.creation_match}
            />
          )}
          {success?.runtime_match && (
            <VerificationMatchView
              title={t("verification.runtimeTransformations")}
              match={success.runtime_match}
            />
          )}
        </details>
      )}
    </section>
  );
}

function VerificationMatchView({
  title,
  match,
}: {
  title: string;
  match: VerificationMatchDetails;
}) {
  return (
    <section className="artifact-panel">
      <h3>{title}</h3>
      <p>
        <code>{match.match_type}</code>
      </p>
      <pre tabIndex={0}>
        {JSON.stringify(
          {
            transformations: match.transformations,
            values: match.values,
          },
          null,
          2,
        )}
      </pre>
    </section>
  );
}

function resultSummary(job: BrowserVerificationJob, external: boolean) {
  if (external && job.status === "succeeded")
    return job.outcome?.kind === "sourcify_success" ? "sourcify.success" : "sourcify.failure";

  if (job.outcome?.kind === "compilation_failure" || job.error_code === "compile_failed")
    return "verification.compilationFailure";
  if (job.outcome?.kind === "verification_failure" || job.error_code === "match_failed")
    return "verification.matchFailure";
  if (job.status === "succeeded")
    return job.outcome?.kind === "batch_results"
      ? "verification.batchCompleted"
      : "verification.successSummary";
  if (job.status === "cancelled") return "verification.cancelledSummary";
  if (job.status !== "failed") return "verification.pendingSummary";
  if (job.error_code === "target_not_canonical") return "verification.targetChanged";
  if (job.error_code?.includes("too_large")) return "verification.resourceLimit";
  if (job.error_code?.startsWith("compiler_") || job.error_code === "sandbox_required")
    return "verification.compilerUnavailable";
  if (job.error_code?.startsWith("sourcify_")) return "verification.upstreamUnavailable";
  return "verification.failedSummary";
}
