import { Message } from "@/components/Message";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { authErrorTranslationKey, useAuth } from "@/auth/AuthProvider";
import { useImportSourcifyContract, useVerificationJob } from "@/api/browserVerification";
import { usePrivateIdentity } from "@/api/privateAccount";
import { usePublicConfig } from "@/api/publicConfig";
import type { BrowserVerificationJob } from "@/api/types";
import { SIWELoginControl } from "@/components/SIWELoginControl";
import { QueryNotice } from "@/components/QueryNotice";
import { VerificationJobPanel } from "./VerificationJobPanel";
import { errorMessage } from "./pages";

export function VerificationLogin() {
  const { t } = useTranslation();
  const auth = useAuth();
  if (auth.loading) return <QueryNotice loading error={null} />;
  if (auth.session.authenticated && auth.enabled) return null;
  return (
    <section className="panel verification-login">
      <p>{t(auth.enabled ? "verification.signInRequired" : "verification.signInUnavailable")}</p>
      {auth.enabled && <SIWELoginControl />}
      {auth.error && <Message type="error">{t(authErrorTranslationKey(auth.error))}</Message>}
    </section>
  );
}

export function VerificationResults({
  jobID,
  onBack,
  onSubmitted,
}: {
  jobID: string;
  onBack: () => void;
  onSubmitted: (job: BrowserVerificationJob) => void;
}) {
  const { t } = useTranslation();
  const auth = useAuth();
  const config = usePublicConfig();
  const identity = usePrivateIdentity();
  const job = useVerificationJob(jobID);
  const [external, setExternal] = useState<{ owner: string; job: BrowserVerificationJob }>();
  const [importedFrom, setImportedFrom] = useState("");
  const submission = useImportSourcifyContract((accepted) => {
    if (job.data) setExternal({ owner: identity, job: job.data });
    setImportedFrom(jobID);
    onSubmitted(accepted);
  });
  useEffect(() => {
    setExternal(undefined);
    setImportedFrom("");
  }, [identity]);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, [jobID]);
  const current = job.data;
  const canImport =
    config.data?.features.verification &&
    config.data.features.sourcify &&
    current?.status === "succeeded" &&
    current.outcome?.kind === "sourcify_success" &&
    current.address &&
    current.chain_id &&
    importedFrom !== jobID;
  return (
    <div className="verification-results">
      <h2 ref={heading} tabIndex={-1}>
        {t("verification.resultsTitle")}
      </h2>
      {auth.enabled && auth.session.authenticated && (
        <>
          <QueryNotice loading={job.isPending} error={job.error} />
          {job.error && (
            <button
              className="button secondary"
              type="button"
              disabled={job.isFetching}
              onClick={() => void job.refetch()}
            >
              {t("verification.retryRead")}
            </button>
          )}
          {external?.owner === identity && external.job.id !== jobID && (
            <VerificationJobPanel job={external.job} loading={false} error={null} />
          )}
          {current && <VerificationJobPanel job={current} loading={false} error={null} />}
          {canImport && (
            <section className="panel verification-login">
              <h3>{t("sourcify.importTitle")}</h3>
              <p>{t("sourcify.afterPublish")}</p>
              {submission.challenge}
              <button
                className="button primary"
                type="button"
                disabled={submission.isPending || !submission.ready}
                onClick={() =>
                  submission.mutate({ address: current.address!, chainID: current.chain_id! })
                }
              >
                {t(submission.isPending ? "sourcify.importing" : "sourcify.importAction")}
              </button>
              {submission.error && (
                <Message type="error">
                  {errorMessage(submission.error, t("sourcify.importFailed"))}
                </Message>
              )}
            </section>
          )}
        </>
      )}
      <button className="button secondary" type="button" onClick={onBack}>
        {t("verification.backToForm")}
      </button>
      <p className="quiet">{t("verification.resultLinkHelp")}</p>
    </div>
  );
}
