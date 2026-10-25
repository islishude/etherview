import { useTranslation } from "react-i18next";
import { useImportSourcifyContract } from "@/api/sourcifyImport";
import { useVerificationJob } from "@/api/hooks";
import { VerificationJobPanel } from "./VerificationJobPanel";
import { errorMessage } from "./pages";

export function SourcifyPostImport({
  address,
  apiKey,
  chainID,
}: {
  address: string;
  apiKey: string;
  chainID: string;
}) {
  const { t } = useTranslation();
  const submission = useImportSourcifyContract(apiKey);
  const job = useVerificationJob(submission.data?.id ?? "", apiKey, 1, Boolean(submission.data));
  return (
    <div className="sourcify-import-result">
      <section className="panel">
        <h2>{t("sourcify.importTitle")}</h2>
        <p className="quiet">{t("sourcify.afterPublish")}</p>
        <button
          className="button primary"
          type="button"
          disabled={submission.isPending || Boolean(submission.data)}
          onClick={() => submission.mutate({ address, chainID })}
        >
          {submission.isPending ? t("sourcify.importing") : t("sourcify.importAction")}
        </button>
        {submission.error && (
          <p role="alert" className="form-error">
            {errorMessage(submission.error, t("sourcify.importFailed"))}
          </p>
        )}
      </section>
      {submission.data && (
        <VerificationJobPanel
          job={job.data ?? submission.data}
          loading={job.isPending}
          error={job.error}
        />
      )}
    </div>
  );
}
