import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { isAddress } from "viem";
import { ApiError } from "@/api/client";
import { QueryNotice } from "@/components/QueryNotice";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { AbiFunctionExplorer } from "@/contracts/AbiFunctionForm";
import {
  useContractProxy,
  useContractDiamondCuts,
  useContractProxyInitializations,
  useContractProxyUpgrades,
  useVerifiedContractArtifact,
  verifiedArtifactMatchesIdentity,
  type ContractDiamondCutPage,
  type ContractProxyInitializationPage,
  type ContractProxyUpgradePage,
  type VerifiedContractArtifact,
} from "@/contracts/proxy";
import {
  buildContractInteractionTargets,
  type ContractInteractionTarget,
  type DiamondFacetInteractionTarget,
} from "@/contracts/targets";
import {
  type PublicProxyTarget,
  ProxySummary,
  type Translate,
  diamondCutActionLabel,
  proxyVerificationLabel,
  proxyManagementLabel,
} from "./ContractProxySummary";
export {
  formatCWIAArgumentValue,
  type CWIAArgumentRow,
  type CWIAArgumentOmission,
  flattenCWIAArgumentRows,
} from "./ContractProxySummary";

const ContractArtifactPanel = lazy(async () => {
  const module = await import("@/contracts/ContractArtifactPanel");
  return { default: module.ContractArtifactPanel };
});

export type ContractTab =
  | "code"
  | "read-contract"
  | "write-contract"
  | "read-implementation"
  | "write-implementation"
  | "management"
  | "diamond-facets"
  | "diamond-cuts"
  | "upgrades"
  | "initializations";

export const CONTRACT_TAB_IDS: readonly ContractTab[] = [
  "code",
  "read-contract",
  "write-contract",
  "read-implementation",
  "write-implementation",
  "management",
  "diamond-facets",
  "diamond-cuts",
  "upgrades",
  "initializations",
];

export function isContractTabHash(hash: string): hash is ContractTab {
  return CONTRACT_TAB_IDS.includes(hash.replace(/^#/u, "") as ContractTab);
}

function contractTabFromHash(hash: string): ContractTab | undefined {
  const normalized = hash.replace(/^#/u, "");
  return isContractTabHash(normalized) ? normalized : undefined;
}

function contractTabLabel(tab: ContractTab, t: Translate): string {
  switch (tab) {
    case "code":
      return t("contracts.tabs.code");
    case "read-contract":
      return t("contracts.tabs.readContract");
    case "write-contract":
      return t("contracts.tabs.writeContract");
    case "read-implementation":
      return t("contracts.tabs.readImplementation");
    case "write-implementation":
      return t("contracts.tabs.writeImplementation");
    case "management":
      return t("contracts.tabs.management");
    case "diamond-facets":
      return t("contracts.tabs.diamondFacets");
    case "diamond-cuts":
      return t("contracts.tabs.diamondCuts");
    case "upgrades":
      return t("contracts.tabs.upgrades");
    case "initializations":
      return t("contracts.tabs.initializations");
  }
}

interface CursorState {
  identity: string;
  cursors: string[];
}

export function ContractPage({ address }: { address: string }) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const validAddress = isAddress(address);
  const requestedTab = contractTabFromHash(location.hash);
  const activeTab = requestedTab ?? "code";
  const proxy = useContractProxy(address, validAddress);
  const artifact = useVerifiedContractArtifact(address, validAddress);
  const showDirectArtifact =
    validAddress &&
    !proxy.isPending &&
    (proxy.data === undefined ||
      (proxy.data.state !== "unavailable" &&
        proxy.data.state !== "failed" &&
        proxy.data.detail.mechanism !== "cwia"));
  const interactionTargets = useMemo<readonly ContractInteractionTarget[]>(() => {
    if (!validAddress) return [];
    try {
      return buildContractInteractionTargets(address, proxy.data?.detail);
    } catch {
      return [];
    }
  }, [address, proxy.data?.detail, validAddress]);
  const contractTargets = interactionTargets.filter((target) => target.kind === "contract");
  const implementationTargets = interactionTargets.filter(
    (target) =>
      target.kind === "implementation_as_proxy" || target.kind === "uups_implementation_direct",
  );
  const managementTargets = interactionTargets.filter(
    (target) => target.kind === "transparent_proxy_admin" || target.kind === "beacon_management",
  );
  const diamondTargets = interactionTargets.filter(
    (target): target is DiamondFacetInteractionTarget => target.kind === "diamond_facet",
  );
  const implementationTarget = implementationTargets[0];
  const managementTarget = managementTargets[0];
  const implementationAddress = implementationTarget?.abiAddress ?? "";
  const managementAddress = managementTarget?.abiAddress ?? "";
  const implementationArtifact = useVerifiedContractArtifact(
    implementationAddress,
    implementationAddress.length > 0,
    implementationTarget?.abiCodeHash,
  );
  const managementArtifact = useVerifiedContractArtifact(
    managementAddress,
    managementAddress.length > 0,
    managementTarget?.abiCodeHash,
  );
  const implementationArtifactMatches = verifiedArtifactMatchesIdentity(
    implementationArtifact.data,
    implementationAddress,
    implementationTarget?.abiCodeHash,
  );
  const managementArtifactMatches = verifiedArtifactMatchesIdentity(
    managementArtifact.data,
    managementAddress,
    managementTarget?.abiCodeHash,
  );
  const [upgradeState, setUpgradeState] = useState<CursorState>({
    identity: address,
    cursors: [""],
  });
  const [initializationState, setInitializationState] = useState<CursorState>({
    identity: address,
    cursors: [""],
  });
  const [diamondCutState, setDiamondCutState] = useState<CursorState>({
    identity: address,
    cursors: [""],
  });
  const upgradeCursors = upgradeState.identity === address ? upgradeState.cursors : [""];
  const initializationCursors =
    initializationState.identity === address ? initializationState.cursors : [""];
  const diamondCutCursors = diamondCutState.identity === address ? diamondCutState.cursors : [""];
  const proxyDetail = proxy.data?.detail;
  const isProxy = proxyDetail?.proxy !== undefined;
  const diamondOutcome = proxyDetail?.proxy_detection_v2?.outcomes.find(
    (outcome) =>
      outcome.family === "erc2535" &&
      outcome.status !== "not-detected" &&
      outcome.status !== "unknown",
  );
  const diamond = diamondOutcome?.diamond;
  const isDiamond = diamond !== undefined;
  const showProxySummary = isProxy || proxyDetail?.proxy_detection_v2 !== undefined;
  const clone = proxyDetail?.pattern === "clone";
  const detected = isProxy && proxyDetail?.status !== "not_detected";
  const upgrades = useContractProxyUpgrades(
    address,
    upgradeCursors.at(-1) || undefined,
    20,
    validAddress && isProxy && detected && !clone && activeTab === "upgrades",
  );
  const initializations = useContractProxyInitializations(
    address,
    initializationCursors.at(-1) || undefined,
    20,
    validAddress && isProxy && detected && activeTab === "initializations",
  );
  const diamondCuts = useContractDiamondCuts(
    address,
    diamondCutCursors.at(-1) || undefined,
    20,
    validAddress && isDiamond && activeTab === "diamond-cuts",
  );

  const contractQueriesPending =
    artifact.isPending ||
    proxy.isPending ||
    (implementationAddress.length > 0 && implementationArtifact.isPending) ||
    (managementAddress.length > 0 && managementArtifact.isPending);
  const contractQueryErrors = [
    artifact.error,
    proxy.error,
    implementationArtifact.error,
    managementArtifact.error,
  ];
  const contractQueryTemporarilyUnavailable = contractQueryErrors.some((error) => {
    if (error === undefined) return false;
    return (
      error instanceof TypeError ||
      (error instanceof ApiError && (error.status >= 500 || error.status === 429))
    );
  });
  const contractQueriesSettling = contractQueriesPending || contractQueryTemporarilyUnavailable;

  const tabs = useMemo(() => {
    const next: Array<{ id: ContractTab; label: string }> = [
      { id: "code", label: t("contracts.tabs.code") },
    ];
    if (showDirectArtifact && artifact.data?.abi) {
      next.push(
        { id: "read-contract", label: t("contracts.tabs.readContract") },
        { id: "write-contract", label: t("contracts.tabs.writeContract") },
      );
    }
    if (implementationArtifactMatches && implementationArtifact.data?.abi) {
      next.push(
        { id: "read-implementation", label: t("contracts.tabs.readImplementation") },
        { id: "write-implementation", label: t("contracts.tabs.writeImplementation") },
      );
    }
    if (managementArtifactMatches && managementArtifact.data?.abi) {
      next.push({ id: "management", label: t("contracts.tabs.management") });
    }
    if (isDiamond && diamond.facets.length > 0) {
      next.push({ id: "diamond-facets", label: t("contracts.tabs.diamondFacets") });
    }
    if (isDiamond) {
      next.push({ id: "diamond-cuts", label: t("contracts.tabs.diamondCuts") });
    }
    if (detected && !clone) {
      next.push({ id: "upgrades", label: t("contracts.tabs.upgrades") });
    }
    if (detected) {
      next.push({ id: "initializations", label: t("contracts.tabs.initializations") });
    }
    if (requestedTab && contractQueriesSettling && !next.some((tab) => tab.id === requestedTab)) {
      next.push({ id: requestedTab, label: contractTabLabel(requestedTab, t) });
    }
    return next;
  }, [
    artifact.data?.abi,
    clone,
    contractQueriesSettling,
    detected,
    diamond,
    implementationArtifact.data?.abi,
    implementationArtifactMatches,
    isDiamond,
    managementArtifact.data?.abi,
    managementArtifactMatches,
    requestedTab,
    showDirectArtifact,
    t,
  ]);

  useEffect(() => {
    if (!requestedTab || contractQueriesSettling || tabs.some((tab) => tab.id === requestedTab))
      return;
    void navigate({
      to: "/address/$address",
      params: { address },
      search: {},
      hash: "code",
      replace: true,
    });
  }, [address, contractQueriesSettling, navigate, requestedTab, tabs]);

  const selectTab = (tabID: ContractTab) => {
    void navigate({
      to: "/address/$address",
      params: { address },
      search: {},
      hash: tabID,
    });
  };

  const navigateTabs = (event: ReactKeyboardEvent<HTMLButtonElement>, tabID: ContractTab) => {
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
      params: { address },
      search: {},
      hash: selected.id,
    }).then(() => document.getElementById(`contract-tab-${selected.id}`)?.focus());
  };

  return (
    <>
      {!validAddress ? (
        <p className="form-error" role="alert">
          {t("contracts.invalidIdentity")}
        </p>
      ) : (
        <div className="contract-detail-stack">
          <nav
            aria-label={t("contracts.sections")}
            aria-orientation="horizontal"
            className="contract-tabs"
            role="tablist"
          >
            {tabs.map((tab) => (
              <button
                aria-controls={`contract-panel-${tab.id}`}
                aria-selected={activeTab === tab.id}
                className={activeTab === tab.id ? "contract-tab active" : "contract-tab"}
                id={`contract-tab-${tab.id}`}
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
              aria-labelledby={`contract-tab-${tab.id}`}
              className="panel contract-tab-panel"
              hidden={activeTab !== tab.id}
              id={`contract-panel-${tab.id}`}
              key={tab.id}
              role="tabpanel"
            >
              {activeTab === tab.id ? (
                <>
                  {activeTab === "code" && (
                    <>
                      {showProxySummary ? (
                        <ProxySummary
                          detail={proxyDetail}
                          loading={proxy.isPending}
                          error={proxy.error}
                        />
                      ) : null}
                      {showDirectArtifact ? (
                        <ArtifactPanel
                          address={address}
                          artifact={artifact.data}
                          error={artifact.error}
                          loading={artifact.isPending}
                        />
                      ) : null}
                    </>
                  )}
                  {(activeTab === "read-contract" || activeTab === "write-contract") && (
                    <AbiFunctionExplorer
                      abi={artifact.data?.abi ?? []}
                      mode={activeTab === "read-contract" ? "read" : "write"}
                      targets={contractTargets}
                    />
                  )}
                  {implementationArtifactMatches &&
                    (activeTab === "read-implementation" ||
                      activeTab === "write-implementation") && (
                      <AbiFunctionExplorer
                        abi={implementationArtifact.data?.abi ?? []}
                        mode={activeTab === "read-implementation" ? "read" : "write"}
                        onBindingChanged={() => void proxy.refetch()}
                        targets={implementationTargets}
                      />
                    )}
                  {managementArtifactMatches && activeTab === "management" && (
                    <AbiFunctionExplorer
                      abi={managementArtifact.data?.abi ?? []}
                      mode="all"
                      onBindingChanged={() => void proxy.refetch()}
                      targets={managementTargets}
                    />
                  )}
                  {activeTab === "diamond-facets" && diamond && (
                    <DiamondFacetsPanel
                      diamondAddress={address}
                      facets={diamond.facets}
                      targets={diamondTargets}
                      onBindingChanged={() => void proxy.refetch()}
                    />
                  )}
                  {activeTab === "diamond-cuts" && (
                    <DiamondCutHistory
                      data={diamondCuts.data}
                      error={diamondCuts.error}
                      loading={diamondCuts.isPending}
                      page={diamondCutCursors.length}
                      onNext={(cursor) =>
                        setDiamondCutState({
                          identity: address,
                          cursors: [...diamondCutCursors, cursor],
                        })
                      }
                      onPrevious={() =>
                        setDiamondCutState({
                          identity: address,
                          cursors: diamondCutCursors.slice(0, -1),
                        })
                      }
                      onReset={() => setDiamondCutState({ identity: address, cursors: [""] })}
                    />
                  )}
                  {activeTab === "upgrades" && (
                    <UpgradeHistory
                      data={upgrades.data}
                      error={upgrades.error}
                      loading={upgrades.isPending}
                      page={upgradeCursors.length}
                      onNext={(cursor) =>
                        setUpgradeState({
                          identity: address,
                          cursors: [...upgradeCursors, cursor],
                        })
                      }
                      onPrevious={() =>
                        setUpgradeState({
                          identity: address,
                          cursors: upgradeCursors.slice(0, -1),
                        })
                      }
                      onReset={() => setUpgradeState({ identity: address, cursors: [""] })}
                    />
                  )}
                  {activeTab === "initializations" && (
                    <InitializationHistory
                      data={initializations.data}
                      error={initializations.error}
                      loading={initializations.isPending}
                      page={initializationCursors.length}
                      onNext={(cursor) =>
                        setInitializationState({
                          identity: address,
                          cursors: [...initializationCursors, cursor],
                        })
                      }
                      onPrevious={() =>
                        setInitializationState({
                          identity: address,
                          cursors: initializationCursors.slice(0, -1),
                        })
                      }
                      onReset={() => setInitializationState({ identity: address, cursors: [""] })}
                    />
                  )}
                </>
              ) : null}
            </section>
          ))}
        </div>
      )}
    </>
  );
}

function DiamondFacetsPanel({
  diamondAddress,
  facets,
  targets,
  onBindingChanged,
}: {
  diamondAddress: string;
  facets: readonly PublicProxyTarget[];
  targets: readonly DiamondFacetInteractionTarget[];
  onBindingChanged: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="diamond-facets">
      <div className="panel-heading">
        <div>
          <span className="eyebrow">ERC-2535</span>
          <h2>{t("contracts.diamond.facetsTitle")}</h2>
        </div>
        <span className="availability yes">
          {t("contracts.diamond.facetCount", { count: facets.length })}
        </span>
      </div>
      <p className="context-note" role="note">
        {t("contracts.diamond.callTarget", { address: diamondAddress })}
      </p>
      <div className="history-list">
        {facets.map((facet) => {
          const target = targets.find(
            (candidate) => candidate.abiAddress.toLowerCase() === facet.address.toLowerCase(),
          );
          return (
            <DiamondFacetCard
              facet={facet}
              key={`${facet.role}:${facet.address}`}
              onBindingChanged={onBindingChanged}
              target={target}
            />
          );
        })}
      </div>
    </div>
  );
}

function DiamondFacetCard({
  facet,
  target,
  onBindingChanged,
}: {
  facet: PublicProxyTarget;
  target?: DiamondFacetInteractionTarget;
  onBindingChanged: () => void;
}) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const artifact = useVerifiedContractArtifact(
    facet.address,
    expanded && target !== undefined,
    target?.abiCodeHash,
  );
  const artifactMatches =
    target !== undefined &&
    verifiedArtifactMatchesIdentity(artifact.data, target.abiAddress, target.abiCodeHash);
  const unverified =
    expanded &&
    target !== undefined &&
    !artifact.isPending &&
    !artifact.data &&
    isUnverifiedArtifactError(artifact.error);
  return (
    <details
      className="history-card diamond-facet-card"
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <summary className="history-card-heading">
        <span>
          <strong>
            {facet.role === "immutable"
              ? t("contracts.diamond.immutable")
              : t("contracts.diamond.facet")}
          </strong>{" "}
          <code>{facet.address}</code>
        </span>
        <span>{t("contracts.diamond.selectorCount", { count: facet.selectors.length })}</span>
      </summary>
      <div className="diamond-selector-list" aria-label={t("contracts.diamond.selectors")}>
        {facet.selectors.map((selector) => (
          <code key={selector}>{selector}</code>
        ))}
      </div>
      {facet.role === "immutable" ? (
        <p className="context-note" role="note">
          {t("contracts.diamond.immutableNotice")}
        </p>
      ) : target === undefined ? (
        <p className="chain-warning" role="status">
          {t("contracts.diamond.partialInteraction")}
        </p>
      ) : (
        <>
          <QueryNotice
            loading={artifact.isPending}
            error={unverified ? undefined : artifact.error}
          />
          {unverified ? (
            <p className="chain-warning" role="status">
              {t("contracts.diamond.unverifiedFacet")}
            </p>
          ) : null}
          {artifactMatches && artifact.data?.abi ? (
            <AbiFunctionExplorer
              abi={artifact.data.abi}
              mode="all"
              onBindingChanged={onBindingChanged}
              targets={[target]}
            />
          ) : null}
        </>
      )}
    </details>
  );
}

function DiamondCutHistory({
  data,
  loading,
  error,
  page,
  onNext,
  onPrevious,
  onReset,
}: {
  data?: ContractDiamondCutPage;
  loading: boolean;
  error: unknown;
  page: number;
  onNext: (cursor: string) => void;
  onPrevious: () => void;
  onReset: () => void;
}) {
  const { t } = useTranslation();
  return (
    <HistoryLayout
      coverage={data?.coverage}
      error={error}
      loading={loading}
      nextCursor={data?.next_cursor}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={t("contracts.diamond.cutsTitle")}
    >
      {data?.items.length === 0 ? <p className="quiet">{t("contracts.diamond.noCuts")}</p> : null}
      {data?.items.map((item) => (
        <article className="history-card" key={`${item.block_hash}:${item.log_index}`}>
          <div className="history-card-heading">
            <strong>{t("contracts.diamond.cut")}</strong>
            <span>
              {t("contracts.history.logIndex")}: {item.log_index}
            </span>
          </div>
          <HistoryIdentity
            blockHash={item.block_hash}
            blockNumber={item.block_number}
            timestamp={item.block_timestamp}
            transactionHash={item.transaction_hash}
          />
          <ol className="diamond-cut-list">
            {item.cuts.map((cut) => (
              <li key={cut.cut_index}>
                <div className="history-card-heading">
                  <strong>{diamondCutActionLabel(cut.action, t)}</strong>
                  <AddressIdentity address={cut.facet_address} compact={false} contract />
                </div>
                <div className="diamond-selector-list">
                  {cut.selectors.map((selector) => (
                    <code key={selector}>{selector}</code>
                  ))}
                </div>
              </li>
            ))}
          </ol>
          <div className="history-evidence">
            <p>
              <small>{t("contracts.diamond.initTarget")}: </small>
              <AddressIdentity address={item.init_address} compact={false} contract />
            </p>
            <p>
              <small>{t("contracts.diamond.initCalldata")}: </small>
              <code>{item.init_calldata}</code>
            </p>
          </div>
        </article>
      ))}
    </HistoryLayout>
  );
}

function ArtifactPanel({
  address,
  artifact,
  loading,
  error,
}: {
  address: string;
  artifact?: VerifiedContractArtifact;
  loading: boolean;
  error: unknown;
}) {
  const { t } = useTranslation();
  const unverified = !loading && !artifact && isUnverifiedArtifactError(error);
  return (
    <div className="verified-artifacts">
      <div>
        <h2>{t("contracts.verifiedArtifact")}</h2>
        <p className="quiet">{t("contracts.readIndependent")}</p>
      </div>
      <QueryNotice loading={loading} error={unverified ? undefined : error} />
      {unverified ? (
        <div className="chain-warning unverified-contract">
          <p role="status">{t("contracts.unverifiedArtifact")}</p>
          <Link className="button secondary inline-button" search={{ address }} to="/verify">
            {t("contracts.submitVerification")}
          </Link>
        </div>
      ) : null}
      {artifact ? (
        <Suspense
          fallback={
            <p className="query-notice" role="status">
              {t("state.loading")}
            </p>
          }
        >
          <ContractArtifactPanel artifact={artifact} />
        </Suspense>
      ) : null}
    </div>
  );
}

function isUnverifiedArtifactError(error: unknown): boolean {
  return error instanceof ApiError && (error.code === "not_found" || error.status === 404);
}

function UpgradeHistory({
  data,
  loading,
  error,
  page,
  onNext,
  onPrevious,
  onReset,
}: {
  data?: ContractProxyUpgradePage;
  loading: boolean;
  error: unknown;
  page: number;
  onNext: (cursor: string) => void;
  onPrevious: () => void;
  onReset: () => void;
}) {
  const { t } = useTranslation();
  return (
    <HistoryLayout
      coverage={data?.coverage}
      error={error}
      loading={loading}
      nextCursor={data?.next_cursor}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={t("contracts.upgrades.title")}
    >
      {data?.items.length === 0 ? <p className="quiet">{t("contracts.upgrades.empty")}</p> : null}
      {data?.items.map((item) => (
        <article
          className="history-card"
          key={`${item.block_hash}:${item.log_index ?? "observation"}:${item.new_implementation.address}`}
        >
          <div className="history-card-heading">
            <strong>{upgradeChangeLabel(item.change_type, t)}</strong>
            <span>{upgradeEvidenceLabel(item.evidence_type, t)}</span>
          </div>
          <p className="history-transition">
            {item.old_implementation ? (
              <AddressIdentity address={item.old_implementation.address} compact={false} contract />
            ) : (
              <code>—</code>
            )}
            <span aria-hidden="true">→</span>
            <AddressIdentity address={item.new_implementation.address} compact={false} contract />
          </p>
          <HistoryIdentity
            blockHash={item.block_hash}
            blockNumber={item.block_number}
            timestamp={item.block_timestamp}
            transactionHash={item.transaction_hash}
          />
          <div className="history-evidence">
            {item.log_index !== undefined ? (
              <p>
                <small>
                  {t("contracts.history.logIndex")}: <code>{item.log_index}</code>
                </small>
              </p>
            ) : null}
            {item.emitter_address ? (
              <p>
                <small>{t("contracts.history.emitter")}: </small>
                <AddressIdentity address={item.emitter_address} compact={false} contract />
              </p>
            ) : null}
            {item.beacon ? (
              <HistoryAddressFact
                identity={item.beacon}
                label={t("contracts.history.beaconEvidence")}
              />
            ) : null}
          </div>
          {item.management ? (
            <HistoryAddressFact
              identity={item.management.target}
              label={t("contracts.history.managementEvidence", {
                kind: proxyManagementLabel(item.management.kind, t),
              })}
            />
          ) : null}
        </article>
      ))}
    </HistoryLayout>
  );
}

function InitializationHistory({
  data,
  loading,
  error,
  page,
  onNext,
  onPrevious,
  onReset,
}: {
  data?: ContractProxyInitializationPage;
  loading: boolean;
  error: unknown;
  page: number;
  onNext: (cursor: string) => void;
  onPrevious: () => void;
  onReset: () => void;
}) {
  const { t } = useTranslation();
  return (
    <HistoryLayout
      coverage={data?.coverage}
      error={error}
      loading={loading}
      nextCursor={data?.next_cursor}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={t("contracts.initializations.title")}
    >
      {data?.items.length === 0 ? (
        <p className="quiet">{t("contracts.initializations.empty")}</p>
      ) : null}
      {data?.items.map((item) => (
        <article className="history-card" key={`${item.transaction_hash}:${item.log_index}`}>
          <div className="history-card-heading">
            <strong>{t("contracts.initializations.version", { version: item.version })}</strong>
            <AddressIdentity address={item.implementation.address} compact={false} contract />
          </div>
          <HistoryIdentity
            blockHash={item.block_hash}
            blockNumber={item.block_number}
            timestamp={item.block_timestamp}
            transactionHash={item.transaction_hash}
          />
          <p>
            <small>
              {t("contracts.history.logIndex")}: <code>{item.log_index}</code>
            </small>
          </p>
        </article>
      ))}
    </HistoryLayout>
  );
}

function HistoryLayout({
  title,
  coverage,
  loading,
  error,
  page,
  nextCursor,
  onNext,
  onPrevious,
  onReset,
  children,
}: {
  title: string;
  coverage?: ContractProxyUpgradePage["coverage"];
  loading: boolean;
  error: unknown;
  page: number;
  nextCursor?: string;
  onNext: (cursor: string) => void;
  onPrevious: () => void;
  onReset: () => void;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="proxy-history">
      <div className="panel-heading">
        <h2>{title}</h2>
        {coverage ? (
          <span className={coverage.state === "complete" ? "availability yes" : "availability no"}>
            {historyCoverageLabel(coverage.state, t)}
          </span>
        ) : null}
      </div>
      {coverage && (coverage.from_block !== undefined || coverage.to_block !== undefined) ? (
        <p className="quiet">
          {t("contracts.history.coverageRange", {
            from: coverage.from_block ?? "—",
            to: coverage.to_block ?? "—",
          })}
        </p>
      ) : null}
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      <div className="history-list">{children}</div>
      <div className="pagination-controls">
        <button
          className="button secondary"
          disabled={page <= 1 || loading}
          onClick={onPrevious}
          type="button"
        >
          {t("pagination.previous")}
        </button>
        <span>{t("pagination.page", { page })}</span>
        <button
          className="button secondary"
          disabled={!nextCursor || loading}
          onClick={() => nextCursor && onNext(nextCursor)}
          type="button"
        >
          {t("pagination.next")}
        </button>
      </div>
    </div>
  );
}

function HistoryIdentity({
  blockHash,
  blockNumber,
  timestamp,
  transactionHash,
}: {
  blockHash: string;
  blockNumber: string;
  timestamp: string;
  transactionHash?: string;
}) {
  return (
    <p className="history-identity">
      <Link to="/blocks/$blockID" params={{ blockID: blockHash }}>
        #{blockNumber}
      </Link>
      <time dateTime={timestamp}>{timestamp}</time>
      {transactionHash ? (
        <Link to="/tx/$hash" params={{ hash: transactionHash }} search={{ tab: "overview" }}>
          <code>{transactionHash}</code>
        </Link>
      ) : null}
    </p>
  );
}

function HistoryAddressFact({
  identity,
  label,
}: {
  identity: {
    address: string;
    verification_state?: "unverified" | "verified";
  };
  label: string;
}) {
  const { t } = useTranslation();
  return (
    <p>
      <small>{label}: </small>
      <AddressIdentity address={identity.address} compact={false} contract />
      {identity.verification_state ? (
        <>
          {" "}
          <small>{proxyVerificationLabel(identity.verification_state, t)}</small>
        </>
      ) : null}
    </p>
  );
}

function historyCoverageLabel(state: "complete" | "partial", t: Translate): string {
  switch (state) {
    case "complete":
      return t("contracts.history.coverageComplete");
    case "partial":
      return t("contracts.history.coveragePartial");
  }
}

function upgradeEvidenceLabel(evidence: "event" | "observation", t: Translate): string {
  switch (evidence) {
    case "event":
      return t("contracts.history.evidenceEvent");
    case "observation":
      return t("contracts.history.evidenceObservation");
  }
}

function upgradeChangeLabel(
  change: "implementation" | "beacon" | "beacon_implementation",
  t: Translate,
): string {
  switch (change) {
    case "implementation":
      return t("contracts.upgrades.implementation");
    case "beacon":
      return t("contracts.upgrades.beacon");
    case "beacon_implementation":
      return t("contracts.upgrades.beaconImplementation");
  }
}
