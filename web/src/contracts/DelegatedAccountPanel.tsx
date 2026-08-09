import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { QueryNotice } from "@/components/QueryNotice";
import { AbiFunctionExplorer } from "@/contracts/AbiFunctionForm";
import {
  useAddressDelegation,
  useAddressDelegationHistory,
} from "@/contracts/delegation";
import {
  useVerifiedContractArtifact,
  verifiedArtifactMatchesIdentity,
} from "@/contracts/proxy";
import { buildDelegatedEOAInteractionTarget } from "@/contracts/targets";

export function DelegatedAccountPanel({ authority }: { authority: string }) {
  const { t } = useTranslation();
  const [cursors, setCursors] = useState<string[]>([""]);
  const binding = useAddressDelegation(authority);
  const history = useAddressDelegationHistory(authority, cursors.at(-1) || undefined);
  const delegate = binding.data?.status === "delegated" ? binding.data.delegate ?? "" : "";
  const codeHash = binding.data?.status === "delegated"
    ? binding.data.delegate_code_hash
    : undefined;
  const artifact = useVerifiedContractArtifact(delegate, delegate.length > 0, codeHash);
  const artifactMatches = verifiedArtifactMatchesIdentity(
    artifact.data,
    delegate,
    codeHash,
  );
  const targets = useMemo(() => {
    if (!binding.data) return [];
    try {
      return [buildDelegatedEOAInteractionTarget(authority, binding.data)];
    } catch {
      return [];
    }
  }, [authority, binding.data]);

  return (
    <div className="contract-detail-stack">
      <section className="panel" aria-labelledby="delegation-binding-title">
        <h2 id="delegation-binding-title">{t("delegation.currentBinding")}</h2>
        <QueryNotice loading={binding.isPending} error={binding.error} />
        {binding.data ? (
          <>
            <p className="capability-panel" role="note">{t("delegation.securityWarning")}</p>
            <dl className="detail-list">
              <div><dt>{t("delegation.authority")}</dt><dd><code>{binding.data.authority}</code></dd></div>
              <div><dt>{t("delegation.status")}</dt><dd>{t(`delegation.statuses.${binding.data.status}`)}</dd></div>
              <div><dt>{t("delegation.delegate")}</dt><dd>{binding.data.delegate ? (
                <Link to="/address/$address" params={{ address: binding.data.delegate }} search={{ tab: "transactions" }}>
                  <code>{binding.data.delegate}</code>
                </Link>
              ) : "—"}</dd></div>
              <div><dt>{t("delegation.codeHash")}</dt><dd><code>{binding.data.delegate_code_hash ?? "—"}</code></dd></div>
              <div><dt>{t("delegation.snapshot")}</dt><dd>
                <Link to="/blocks/$blockID" params={{ blockID: binding.data.block_hash }}><code>{binding.data.block_number}</code></Link>
              </dd></div>
            </dl>
          </>
        ) : null}
      </section>

      {binding.data?.status === "delegated" ? (
        <section className="panel" aria-labelledby="delegation-interaction-title">
          <h2 id="delegation-interaction-title">{t("delegation.interaction")}</h2>
          <p className="quiet">{t("delegation.interactionTarget")}</p>
          <QueryNotice loading={artifact.isPending} error={artifact.error} />
          {artifactMatches && artifact.data?.abi && targets.length > 0 ? (
            <AbiFunctionExplorer abi={artifact.data.abi} mode="all" targets={targets} />
          ) : !artifact.isPending && !artifact.error ? (
            <p className="empty-result">{t("delegation.abiUnavailable")}</p>
          ) : null}
        </section>
      ) : null}

      <section className="panel" aria-labelledby="delegation-history-title">
        <h2 id="delegation-history-title">{t("delegation.history")}</h2>
        <QueryNotice loading={history.isPending} error={history.error} />
        {history.data?.items.length === 0 ? <p className="empty-result">{t("delegation.noHistory")}</p> : null}
        {history.data && history.data.items.length > 0 ? (
          <div className="table-scroll" tabIndex={0}>
            <table>
              <thead><tr><th>{t("delegation.kind")}</th><th>{t("delegation.delegate")}</th><th>{t("table.transaction")}</th><th>{t("table.block")}</th></tr></thead>
              <tbody>{history.data.items.map((item) => (
                <tr key={`${item.block_hash}:${item.transaction_hash}:${item.authorization_index}`}>
                  <td>{t(`delegation.kinds.${item.kind}`)}</td>
                  <td><code>{item.delegate}</code></td>
                  <td><Link to="/tx/$hash" params={{ hash: item.transaction_hash }} search={{ tab: "overview" }}><code>{item.transaction_hash}</code></Link></td>
                  <td><Link to="/blocks/$blockID" params={{ blockID: item.block_hash }}><code>{item.block_number}</code></Link></td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        ) : null}
        {history.data ? (
          <div className="pagination-actions">
            <button disabled={cursors.length <= 1 || history.isFetching} onClick={() => setCursors((value) => value.slice(0, -1))} type="button">{t("pagination.previous")}</button>
            <button disabled={!history.data.nextCursor || history.isFetching} onClick={() => history.data?.nextCursor && setCursors((value) => [...value, history.data!.nextCursor!])} type="button">{t("pagination.next")}</button>
          </div>
        ) : null}
      </section>
    </div>
  );
}
