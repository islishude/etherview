import { useEffect, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { QueryNotice } from "@/components/QueryNotice";
import { AbiFunctionExplorer } from "@/contracts/AbiFunctionForm";
import { ContractArtifactPanel } from "@/contracts/ContractArtifactPanel";
import {
  useAddressDelegation,
  useAddressDelegationHistory,
} from "@/contracts/delegation";
import {
  useVerifiedContractArtifact,
  verifiedArtifactMatchesIdentity,
} from "@/contracts/proxy";
import { buildDelegatedEOAInteractionTarget } from "@/contracts/targets";

export type DelegatedAccountTab = "code" | "read-contract" | "write-contract" | "history";

export const DELEGATED_ACCOUNT_TAB_IDS: readonly DelegatedAccountTab[] = [
  "code",
  "read-contract",
  "write-contract",
  "history",
];

export function isDelegatedAccountTabHash(hash: string): hash is DelegatedAccountTab {
  return DELEGATED_ACCOUNT_TAB_IDS.includes(hash.replace(/^#/u, "") as DelegatedAccountTab);
}

function delegatedAccountTabFromHash(hash: string): DelegatedAccountTab | undefined {
  const normalized = hash.replace(/^#/u, "");
  return isDelegatedAccountTabHash(normalized) ? normalized : undefined;
}

export function DelegatedAccountPanel({
  authority,
  currentlyDelegated,
}: {
  authority: string;
  currentlyDelegated: boolean;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const requestedTab = delegatedAccountTabFromHash(location.hash);
  const activeTab = requestedTab ?? "code";
  const binding = useAddressDelegation(
    authority,
    currentlyDelegated || activeTab !== "history",
  );
  const delegate = binding.data?.status === "delegated" ? binding.data.delegate ?? "" : "";
  const codeHash = binding.data?.status === "delegated"
    ? binding.data.delegate_code_hash
    : undefined;
  const artifactRelevant = delegate.length > 0;
  const artifact = useVerifiedContractArtifact(delegate, artifactRelevant, codeHash);
  const artifactPending = artifactRelevant && artifact.isPending;
  const artifactMatches = verifiedArtifactMatchesIdentity(
    artifact.data,
    delegate,
    codeHash,
  );
  const delegatedView = binding.data
    ? binding.data.status === "delegated"
    : currentlyDelegated;
  const codeTabLabel = delegatedView
    ? t("contracts.tabs.code")
    : t("delegation.tabs.status");
  const targets = useMemo(() => {
    if (!binding.data) return [];
    try {
      return [buildDelegatedEOAInteractionTarget(authority, binding.data)];
    } catch {
      return [];
    }
  }, [authority, binding.data]);
  const [historyState, setHistoryState] = useState<{ identity: string; cursors: string[] }>({
    identity: authority,
    cursors: [""],
  });
  const historyCursors = historyState.identity === authority ? historyState.cursors : [""];
  const history = useAddressDelegationHistory(
    authority,
    historyCursors.at(-1) || undefined,
    20,
    activeTab === "history",
  );

  useEffect(() => {
    if (historyState.identity === authority) return;
    setHistoryState({ identity: authority, cursors: [""] });
  }, [authority, historyState.identity]);

  const bindingTemporarilyUnavailable = binding.error !== undefined && isTemporaryError(binding.error);
  const artifactTemporarilyUnavailable = artifactRelevant && artifact.error !== undefined && isTemporaryError(artifact.error);
  const tabs = useMemo(() => {
    const next: Array<{ id: DelegatedAccountTab; label: string }> = [
      { id: "code", label: codeTabLabel },
    ];
    if (binding.data?.status === "delegated" && artifactMatches && artifact.data?.abi) {
      next.push(
        { id: "read-contract", label: t("contracts.tabs.readContract") },
        { id: "write-contract", label: t("contracts.tabs.writeContract") },
      );
    }
    next.push({ id: "history", label: t("delegation.tabs.history") });
    if (
      requestedTab &&
      (binding.isPending || artifactPending || bindingTemporarilyUnavailable || artifactTemporarilyUnavailable) &&
      !next.some((tab) => tab.id === requestedTab)
    ) {
      next.push({
        id: requestedTab,
        label: requestedTab === "read-contract"
          ? t("contracts.tabs.readContract")
          : requestedTab === "write-contract"
            ? t("contracts.tabs.writeContract")
            : requestedTab === "history"
              ? t("delegation.tabs.history")
              : codeTabLabel,
      });
    }
    return next;
  }, [artifact.data?.abi, artifactMatches, artifactPending, artifactTemporarilyUnavailable, binding.data?.status, binding.isPending, bindingTemporarilyUnavailable, codeTabLabel, requestedTab, t]);

  useEffect(() => {
    if (!requestedTab || binding.isPending || artifactPending || tabs.some((tab) => tab.id === requestedTab)) return;
    void navigate({
      to: "/address/$address",
      params: { address: authority },
      search: { tab: "delegation" },
      hash: "code",
      replace: true,
    });
  }, [artifactPending, authority, binding.isPending, navigate, requestedTab, tabs]);

  const selectTab = (tabID: DelegatedAccountTab) => {
    void navigate({
      to: "/address/$address",
      params: { address: authority },
      search: { tab: "delegation" },
      hash: tabID,
    });
  };

  const navigateTabs = (event: ReactKeyboardEvent<HTMLButtonElement>, tabID: DelegatedAccountTab) => {
    const current = tabs.findIndex((tab) => tab.id === tabID);
    let next = current;
    if (event.key === "ArrowRight") next = (current + 1) % tabs.length;
    else if (event.key === "ArrowLeft") next = (current - 1 + tabs.length) % tabs.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = tabs.length - 1;
    else return;
    event.preventDefault();
    const selected = tabs[next];
    if (!selected) return;
    void navigate({
      to: "/address/$address",
      params: { address: authority },
      search: { tab: "delegation" },
      hash: selected.id,
    }).then(() => document.getElementById(`delegated-tab-${selected.id}`)?.focus());
  };

  return (
    <div className="contract-detail-stack">
      <nav aria-label={t("delegation.sections")} aria-orientation="horizontal" className="contract-tabs" role="tablist">
        {tabs.map((tab) => (
          <button
            aria-controls={`delegated-panel-${tab.id}`}
            aria-selected={activeTab === tab.id}
            className={activeTab === tab.id ? "contract-tab active" : "contract-tab"}
            id={`delegated-tab-${tab.id}`}
            key={tab.id}
            onClick={() => selectTab(tab.id)}
            onKeyDown={(event) => navigateTabs(event, tab.id)}
            role="tab"
            tabIndex={activeTab === tab.id ? 0 : -1}
            type="button"
          >
            {tab.label}
          </button>
        ))}
      </nav>

      {tabs.map((tab) => (
        <section
          aria-labelledby={`delegated-tab-${tab.id}`}
          className="panel contract-tab-panel"
          hidden={activeTab !== tab.id}
          id={`delegated-panel-${tab.id}`}
          key={tab.id}
          role="tabpanel"
        >
          {activeTab === tab.id ? (
            <>
              {activeTab === "code" ? (
                <DelegatedCodePanel
                  artifact={artifact.data}
                  artifactError={artifact.error}
                  artifactLoading={artifactPending}
                  binding={binding.data}
                  bindingError={binding.error}
                  bindingLoading={binding.isPending}
                  delegatedView={delegatedView}
                  onViewHistory={() => selectTab("history")}
                />
              ) : null}
              {(activeTab === "read-contract" || activeTab === "write-contract") ? (
                <DelegatedInteractionPanel
                  abi={artifact.data?.abi}
                  artifactError={artifact.error}
                  artifactLoading={artifactPending}
                  artifactMatches={artifactMatches}
                  bindingError={binding.error}
                  bindingLoading={binding.isPending}
                  mode={activeTab === "read-contract" ? "read" : "write"}
                  onBindingChanged={() => void binding.refetch()}
                  targets={targets}
                />
              ) : null}
              {activeTab === "history" ? (
                <DelegationHistory
                  data={history.data}
                  error={history.error}
                  loading={history.isPending}
                  busy={history.isFetching}
                  cursors={historyCursors}
                  onNext={(cursor) => setHistoryState({ identity: authority, cursors: [...historyCursors, cursor] })}
                  onPrevious={() => setHistoryState({ identity: authority, cursors: historyCursors.slice(0, -1) })}
                />
              ) : null}
            </>
          ) : null}
        </section>
      ))}
    </div>
  );
}

function DelegatedCodePanel({
  artifact,
  artifactError,
  artifactLoading,
  binding,
  bindingError,
  bindingLoading,
  delegatedView,
  onViewHistory,
}: {
  artifact?: Parameters<typeof ContractArtifactPanel>[0]["artifact"];
  artifactError: unknown;
  artifactLoading: boolean;
  binding?: ReturnType<typeof useAddressDelegation>["data"];
  bindingError: unknown;
  bindingLoading: boolean;
  delegatedView: boolean;
  onViewHistory: () => void;
}) {
  const { t } = useTranslation();
  const delegated = binding?.status === "delegated";
  const statusOnly = binding !== undefined && !delegated;
  return (
    <div className="delegated-code-stack">
      <section className="panel detail-card" aria-labelledby="delegation-binding-title">
        <h2 id="delegation-binding-title">
          {delegatedView ? t("delegation.currentBinding") : t("delegation.statusTitle")}
        </h2>
        <QueryNotice loading={bindingLoading} error={bindingError} />
        {delegated ? (
          <>
            <p className="capability-panel context-note" role="note">{t("delegation.securityWarning")}</p>
            <dl className="detail-grid">
              <div className="detail-item"><dt>{t("delegation.authority")}</dt><dd><code>{binding.authority}</code></dd></div>
              <div className="detail-item"><dt>{t("delegation.status")}</dt><dd>{t(`delegation.statuses.${binding.status}`)}</dd></div>
              <div className="detail-item"><dt>{t("delegation.delegate")}</dt><dd>{binding.delegate ? (
                <Link to="/address/$address" params={{ address: binding.delegate }} search={{ tab: "transactions" }}>
                  <code>{binding.delegate}</code>
                </Link>
              ) : "—"}</dd></div>
              <div className="detail-item"><dt>{t("delegation.codeHash")}</dt><dd><code>{binding.delegate_code_hash ?? "—"}</code></dd></div>
              <div className="detail-item wide"><dt>{t("delegation.snapshot")}</dt><dd>
                <Link to="/blocks/$blockID" params={{ blockID: binding.block_hash }}><code>{binding.block_number}</code></Link>
              </dd></div>
            </dl>
          </>
        ) : null}
        {statusOnly ? (
          <>
            <p className="capability-panel context-note" role="note">
              {binding.status === "not_delegated"
                ? t("delegation.clearedDescription")
                : t("delegation.unavailableDescription")}
            </p>
            <dl className="detail-grid">
              <div className="detail-item"><dt>{t("delegation.authority")}</dt><dd><code>{binding.authority}</code></dd></div>
              <div className="detail-item"><dt>{t("delegation.status")}</dt><dd>{t(`delegation.statuses.${binding.status}`)}</dd></div>
              <div className="detail-item wide"><dt>{t("delegation.snapshot")}</dt><dd>
                <Link to="/blocks/$blockID" params={{ blockID: binding.block_hash }}><code>{binding.block_number}</code></Link>
              </dd></div>
            </dl>
            <button className="button secondary" onClick={onViewHistory} type="button">
              {t("delegation.viewHistory")}
            </button>
          </>
        ) : null}
      </section>
      {delegated ? (
        <section className="panel detail-card" aria-labelledby="delegation-artifact-title">
          <h2 id="delegation-artifact-title">{t("contracts.verifiedArtifact")}</h2>
          <p className="quiet">{t("contracts.readIndependent")}</p>
          <QueryNotice loading={artifactLoading} error={artifactError} />
          {artifact ? <ContractArtifactPanel artifact={artifact} /> : null}
        </section>
      ) : null}
    </div>
  );
}

function DelegatedInteractionPanel({
  abi,
  artifactError,
  artifactLoading,
  artifactMatches,
  bindingError,
  bindingLoading,
  mode,
  onBindingChanged,
  targets,
}: {
  abi?: Parameters<typeof AbiFunctionExplorer>[0]["abi"];
  artifactError: unknown;
  artifactLoading: boolean;
  artifactMatches: boolean;
  bindingError: unknown;
  bindingLoading: boolean;
  mode: "read" | "write";
  onBindingChanged: () => void;
  targets: Parameters<typeof AbiFunctionExplorer>[0]["targets"];
}) {
  const { t } = useTranslation();
  return (
    <div className="delegated-interaction-stack">
      <p className="quiet">{t("delegation.interactionTarget")}</p>
      <QueryNotice loading={bindingLoading || artifactLoading} error={bindingError ?? artifactError} />
      {artifactMatches && abi && targets.length > 0 ? (
        <AbiFunctionExplorer
          abi={abi}
          mode={mode}
          onBindingChanged={onBindingChanged}
          targets={targets}
        />
      ) : !bindingLoading && !artifactLoading && !bindingError && !artifactError ? (
        <p className="empty-result">{t("delegation.abiUnavailable")}</p>
      ) : null}
    </div>
  );
}

function DelegationHistory({
  busy,
  cursors,
  data,
  error,
  loading,
  onNext,
  onPrevious,
}: {
  busy: boolean;
  cursors: string[];
  data?: Awaited<ReturnType<typeof useAddressDelegationHistory>>["data"];
  error: unknown;
  loading: boolean;
  onNext: (cursor: string) => void;
  onPrevious: () => void;
}) {
  const { t } = useTranslation();
  return (
    <section className="detail-card">
      <h2 id="delegation-history-title">{t("delegation.history")}</h2>
      <QueryNotice loading={loading} error={error} />
      {data?.items.length === 0 ? <p className="empty-result">{t("delegation.noHistory")}</p> : null}
      {data && data.items.length > 0 ? (
        <div className="table-scroll" tabIndex={0}>
          <table>
            <thead><tr><th>{t("delegation.kind")}</th><th>{t("delegation.delegate")}</th><th>{t("table.transaction")}</th><th>{t("table.block")}</th></tr></thead>
            <tbody>{data.items.map((item) => (
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
      {data ? (
        <nav className="cursor-pagination" aria-label={t("delegation.history")}>
          <button
            className="button secondary"
            disabled={cursors.length <= 1 || busy}
            onClick={onPrevious}
            type="button"
          >
            {t("pagination.previous")}
          </button>
          <button
            className="button secondary"
            disabled={!data.nextCursor || busy}
            onClick={() => data.nextCursor && onNext(data.nextCursor)}
            type="button"
          >
            {t("pagination.next")}
          </button>
        </nav>
      ) : null}
    </section>
  );
}

function isTemporaryError(error: unknown): boolean {
  if (error instanceof TypeError) return true;
  if (typeof error !== "object" || error === null || !("status" in error)) return false;
  const status = (error as { status?: unknown }).status;
  return status === 429 || (typeof status === "number" && status >= 500);
}
