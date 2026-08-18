import {
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
import { ContractArtifactPanel } from "@/contracts/ContractArtifactPanel";
import {
  useContractProxy,
  useContractDiamondCuts,
  useContractProxyInitializations,
  useContractProxyUpgrades,
  useVerifiedContractArtifact,
  verifiedArtifactMatchesIdentity,
  type ContractProxyDetails,
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
    case "code": return t("contracts.tabs.code");
    case "read-contract": return t("contracts.tabs.readContract");
    case "write-contract": return t("contracts.tabs.writeContract");
    case "read-implementation": return t("contracts.tabs.readImplementation");
    case "write-implementation": return t("contracts.tabs.writeImplementation");
    case "management": return t("contracts.tabs.management");
    case "diamond-facets": return t("contracts.tabs.diamondFacets");
    case "diamond-cuts": return t("contracts.tabs.diamondCuts");
    case "upgrades": return t("contracts.tabs.upgrades");
    case "initializations": return t("contracts.tabs.initializations");
  }
}

interface CursorState {
  identity: string;
  cursors: string[];
}

type PublicProxyTarget = NonNullable<
  NonNullable<ContractProxyDetails["proxy_detection_v2"]>["primary"]
>["targets"][number];

export function ContractPage({ address }: { address: string }) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const validAddress = isAddress(address);
  const requestedTab = contractTabFromHash(location.hash);
  const activeTab = requestedTab ?? "code";
  const artifact = useVerifiedContractArtifact(address, validAddress);
  const proxy = useContractProxy(address, validAddress);
  const interactionTargets = useMemo<readonly ContractInteractionTarget[]>(() => {
    if (!validAddress) return [];
    try {
      return buildContractInteractionTargets(address, proxy.data?.detail);
    } catch {
      return [];
    }
  }, [address, proxy.data?.detail, validAddress]);
  const contractTargets = interactionTargets.filter((target) => target.kind === "contract");
  const implementationTargets = interactionTargets.filter((target) =>
    target.kind === "implementation_as_proxy" || target.kind === "uups_implementation_direct"
  );
  const managementTargets = interactionTargets.filter((target) =>
    target.kind === "transparent_proxy_admin" || target.kind === "beacon_management"
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
  const diamondCutCursors =
    diamondCutState.identity === address ? diamondCutState.cursors : [""];
  const proxyDetail = proxy.data?.detail;
  const isProxy = proxyDetail?.proxy !== undefined;
  const diamondOutcome = proxyDetail?.proxy_detection_v2?.outcomes.find((outcome) =>
    outcome.family === "erc2535" && outcome.status !== "not-detected" && outcome.status !== "unknown"
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

  const contractQueriesPending = artifact.isPending || proxy.isPending
    || (implementationAddress.length > 0 && implementationArtifact.isPending)
    || (managementAddress.length > 0 && managementArtifact.isPending);
  const contractQueryErrors = [
    artifact.error,
    proxy.error,
    implementationArtifact.error,
    managementArtifact.error,
  ];
  const contractQueryTemporarilyUnavailable = contractQueryErrors.some((error) => {
    if (error === undefined) return false;
    return error instanceof TypeError || (error instanceof ApiError && (error.status >= 500 || error.status === 429));
  });
  const contractQueriesSettling = contractQueriesPending || contractQueryTemporarilyUnavailable;

  const tabs = useMemo(() => {
    const next: Array<{ id: ContractTab; label: string }> = [
      { id: "code", label: t("contracts.tabs.code") },
    ];
    if (artifact.data?.abi) {
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
  }, [artifact.data?.abi, clone, contractQueriesSettling, detected, diamond, implementationArtifact.data?.abi, implementationArtifactMatches, isDiamond, managementArtifact.data?.abi, managementArtifactMatches, requestedTab, t]);

  useEffect(() => {
    if (!requestedTab || contractQueriesSettling || tabs.some((tab) => tab.id === requestedTab)) return;
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
        <p className="form-error" role="alert">{t("contracts.invalidIdentity")}</p>
      ) : (
        <div className="contract-detail-stack">
          <nav aria-label={t("contracts.sections")} aria-orientation="horizontal" className="contract-tabs" role="tablist">
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
                        <ProxySummary detail={proxyDetail} loading={proxy.isPending} error={proxy.error} />
                      ) : null}
                      <ArtifactPanel
                        address={address}
                        artifact={artifact.data}
                        error={artifact.error}
                        loading={artifact.isPending}
                      />
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
                    (activeTab === "read-implementation" || activeTab === "write-implementation") && (
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
                      onNext={(cursor) => setDiamondCutState({
                        identity: address,
                        cursors: [...diamondCutCursors, cursor],
                      })}
                      onPrevious={() => setDiamondCutState({
                        identity: address,
                        cursors: diamondCutCursors.slice(0, -1),
                      })}
                      onReset={() => setDiamondCutState({ identity: address, cursors: [""] })}
                    />
                  )}
                  {activeTab === "upgrades" && (
                    <UpgradeHistory
                      data={upgrades.data}
                      error={upgrades.error}
                      loading={upgrades.isPending}
                      page={upgradeCursors.length}
                      onNext={(cursor) => setUpgradeState({
                        identity: address,
                        cursors: [...upgradeCursors, cursor],
                      })}
                      onPrevious={() => setUpgradeState({
                        identity: address,
                        cursors: upgradeCursors.slice(0, -1),
                      })}
                      onReset={() => setUpgradeState({ identity: address, cursors: [""] })}
                    />
                  )}
                  {activeTab === "initializations" && (
                    <InitializationHistory
                      data={initializations.data}
                      error={initializations.error}
                      loading={initializations.isPending}
                      page={initializationCursors.length}
                      onNext={(cursor) => setInitializationState({
                        identity: address,
                        cursors: [...initializationCursors, cursor],
                      })}
                      onPrevious={() => setInitializationState({
                        identity: address,
                        cursors: initializationCursors.slice(0, -1),
                      })}
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
          const target = targets.find((candidate) =>
            candidate.abiAddress.toLowerCase() === facet.address.toLowerCase()
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
  const artifactMatches = target !== undefined && verifiedArtifactMatchesIdentity(
    artifact.data,
    target.abiAddress,
    target.abiCodeHash,
  );
  const unverified = expanded && target !== undefined && !artifact.isPending &&
    !artifact.data && isUnverifiedArtifactError(artifact.error);
  return (
    <details
      className="history-card diamond-facet-card"
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <summary className="history-card-heading">
        <span>
          <strong>{facet.role === "immutable"
            ? t("contracts.diamond.immutable")
            : t("contracts.diamond.facet")}</strong>{" "}
          <code>{facet.address}</code>
        </span>
        <span>{t("contracts.diamond.selectorCount", { count: facet.selectors.length })}</span>
      </summary>
      <div className="diamond-selector-list" aria-label={t("contracts.diamond.selectors")}>
        {facet.selectors.map((selector) => <code key={selector}>{selector}</code>)}
      </div>
      {facet.role === "immutable" ? (
        <p className="context-note" role="note">{t("contracts.diamond.immutableNotice")}</p>
      ) : target === undefined ? (
        <p className="chain-warning" role="status">{t("contracts.diamond.partialInteraction")}</p>
      ) : (
        <>
          <QueryNotice loading={artifact.isPending} error={unverified ? undefined : artifact.error} />
          {unverified ? (
            <p className="chain-warning" role="status">{t("contracts.diamond.unverifiedFacet")}</p>
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
            <span>{t("contracts.history.logIndex")}: {item.log_index}</span>
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
                  {cut.selectors.map((selector) => <code key={selector}>{selector}</code>)}
                </div>
              </li>
            ))}
          </ol>
          <div className="history-evidence">
            <p>
              <small>{t("contracts.diamond.initTarget")}: </small>
              <AddressIdentity address={item.init_address} compact={false} contract />
            </p>
            <p><small>{t("contracts.diamond.initCalldata")}: </small><code>{item.init_calldata}</code></p>
          </div>
        </article>
      ))}
    </HistoryLayout>
  );
}

function ProxySummary({
  detail,
  loading,
  error,
}: {
  detail?: ContractProxyDetails;
  loading: boolean;
  error: unknown;
}) {
  const { t } = useTranslation();
  const detectionV2 = detail?.proxy_detection_v2;
  const v2Primary = detectionV2?.primary;
  const v2Detected = detectionV2 !== undefined && detectionV2.status !== "not-detected";
  const diamondOutcome = detectionV2?.outcomes.find((outcome) =>
    outcome.family === "erc2535" && outcome.diamond !== undefined && outcome.status !== "not-detected"
  );
  const diamond = diamondOutcome?.diamond;
  const layers = detectionV2?.outcomes
    .filter((outcome) => outcome.family && outcome.status !== "not-detected")
    .map((outcome) => `${outcome.family}${outcome.variant ? ` (${outcome.variant})` : ""}`)
    .join(" → ");
  const eyebrow = diamond
    ? "ERC-2535 Diamond"
    : v2Primary?.family === "safe" ? "Safe Proxy" : "OpenZeppelin 5.x";
  return (
    <details className="panel proxy-summary">
      <summary className="panel-heading">
        <div>
          <span className="eyebrow">{eyebrow}</span>
          <h2 id="proxy-summary-title">{t("contracts.proxy.title")}</h2>
        </div>
        {detail ? (
          <span className={detail.status === "verified" || detectionV2?.status === "confirmed" ? "availability yes" : "availability no"}>
            {detectionV2 ? proxyDetectionV2StatusLabel(detectionV2.status, t) : proxyStatusLabel(detail.status, t)}
          </span>
        ) : null}
      </summary>
      <QueryNotice loading={loading} error={error} />
      {detail?.status === "not_detected" && !v2Detected ? (
        <p className="quiet">{t("contracts.proxy.notDetected")}</p>
      ) : null}
      {detail && (detail.status !== "not_detected" || v2Detected) ? (
        <dl className="proxy-facts">
          {detectionV2 ? (
            <>
              <Fact label={t("contracts.proxy.detectorStatus")} value={proxyDetectionV2StatusLabel(detectionV2.status, t)} />
              <Fact label={t("contracts.proxy.family")} value={v2Primary?.family ?? "—"} />
              <Fact label={t("contracts.proxy.variant")} value={v2Primary?.variant ?? "—"} />
              {layers ? <Fact label={t("contracts.proxy.layers")} value={layers} /> : null}
              {v2Primary?.implementation ? (
                <Fact
                  label={v2Primary.implementation_role === "singleton" ? t("contracts.proxy.singleton") : t("contracts.proxy.implementation")}
                  value={v2Primary.implementation}
                  mono
                />
              ) : null}
              {v2Primary?.family === "safe" ? (
                <Fact label={t("contracts.proxy.officialSingleton")} value={v2Primary.official_singleton ? t("common.yes") : t("common.no")} />
              ) : null}
              <Fact label={t("contracts.proxy.detectorVersion")} value={v2Primary?.detector_version ?? "—"} />
              {v2Primary && v2Primary.warnings.length > 0 ? (
                <Fact label={t("contracts.proxy.warnings")} value={v2Primary.warnings.join(" · ")} />
              ) : null}
              {detectionV2.conflicts.length > 0 ? (
                <Fact label={t("contracts.proxy.conflicts")} value={detectionV2.conflicts.join(" · ")} />
              ) : null}
              {diamond ? (
                <>
                  <Fact label={t("contracts.diamond.facets")} value={String(diamond.facets.length)} />
                  <Fact label={t("contracts.diamond.selectors")} value={String(Object.keys(diamond.selector_to_facet).length)} />
                  <Fact label={t("contracts.diamond.completeness")} value={diamondCompletenessLabel(diamond.completeness, t)} />
                  <Fact label={t("contracts.diamond.validation")} value={diamondValidationLabel(diamond.validation, t)} />
                  <Fact
                    label={t("contracts.diamond.standardCut")}
                    value={`${diamondCutPresenceLabel(diamond.standard_diamond_cut.status, t)}${diamond.standard_diamond_cut.facet ? ` · ${diamond.standard_diamond_cut.facet}` : ""}`}
                    mono={diamond.standard_diamond_cut.facet !== undefined}
                  />
                  <Fact
                    label={t("contracts.diamond.loupeInterface")}
                    value={diamond.loupe_interface_reported === undefined
                      ? t("contracts.diamond.unknown")
                      : diamond.loupe_interface_reported ? t("common.yes") : t("common.no")}
                  />
                  <Fact
                    label={t("contracts.diamond.externalFacets")}
                    value={diamond.implementation_addresses.join(" · ") || "—"}
                    mono
                  />
                  {diamond.truncated ? (
                    <Fact label={t("contracts.diamond.truncated")} value={diamond.truncation_reason ?? t("contracts.diamond.unknown")} />
                  ) : null}
                </>
              ) : null}
            </>
          ) : null}
          {detail.pattern ? <Fact label={t("contracts.proxy.pattern")} value={proxyPatternLabel(detail.pattern, t)} /> : null}
          {detail.mechanism ? <Fact label={t("contracts.proxy.mechanism")} value={proxyMechanismLabel(detail.mechanism, t)} /> : null}
          {detail.evidence_state ? <Fact label={t("contracts.proxy.evidenceState")} value={proxyEvidenceStateLabel(detail.evidence_state, t)} /> : null}
          {detail.confidence ? <Fact label={t("contracts.proxy.confidence")} value={proxyConfidenceLabel(detail.confidence, t)} /> : null}
          {detail.standard_version ? <Fact label={t("contracts.proxy.standardVersion")} value={detail.standard_version} /> : null}
          {detail.implementation ? <IdentityFact label={t("contracts.proxy.implementation")} identity={detail.implementation} /> : null}
          {detail.admin ? <IdentityFact label={t("contracts.proxy.admin")} identity={detail.admin} /> : null}
          {detail.beacon ? <IdentityFact label={t("contracts.proxy.beacon")} identity={detail.beacon} /> : null}
          {detail.management?.target ? <IdentityFact label={t("contracts.proxy.managementTarget")} identity={detail.management.target} /> : null}
          {detail.management?.affected_proxy_count ? <Fact label={t("contracts.proxy.affectedProxies")} value={detail.management.affected_proxy_count} /> : null}
          {detail.binding_id ? <Fact label={t("contracts.proxy.binding")} value={detail.binding_id} mono /> : null}
          <Fact
            label={t("contracts.proxy.snapshot")}
            value={`${detail.snapshot.block_number} · ${detail.snapshot.block_hash}`}
            mono
          />
          {detail.immutable_args ? (
            <Fact label={t("contracts.proxy.immutableArgs")} value={detail.immutable_args} mono />
          ) : null}
        </dl>
      ) : null}
      {detail && detail.evidence.length > 0 ? (
        <details className="proxy-evidence">
          <summary>{t("contracts.proxy.evidence", { count: detail.evidence.length })}</summary>
          <ul>
            {detail.evidence.map((item, index) => (
              <li key={`${item.source}:${item.subject}:${item.block_hash ?? "snapshot"}:${index}`}>
                {proxyEvidenceSourceLabel(item.source, t)} · {proxyEvidenceSubjectLabel(item.subject, t)} · {proxyEvidenceResultLabel(item.result, t)}
                {item.address ? <> · <code>{item.address}</code></> : null}
                {item.block_number ? <> · #{item.block_number}</> : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {diamond ? (
        <p className="context-note" role="note">{t("contracts.diamond.interactionSafety")}</p>
      ) : detail && detail.status !== "verified" && detail.status !== "not_detected" ? (
        <p className="chain-warning" role="status">{t("contracts.proxy.writeDisabled")}</p>
      ) : null}
      {detail?.pattern === "clone" ? (
        <p className="context-note" role="note">{t("contracts.proxy.cloneImmutable")}</p>
      ) : null}
    </details>
  );
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return <div><dt>{label}</dt><dd className={mono ? "mono-wrap" : undefined}>{value}</dd></div>;
}

function IdentityFact({
  label,
  identity,
}: {
  label: string;
  identity?: { address: string; verification_state: "unverified" | "verified" };
}) {
  const { t } = useTranslation();
  return (
    <div>
      <dt>{label}</dt>
      <dd>
        {identity ? (
          <>
            <AddressIdentity address={identity.address} compact={false} contract />{" "}
            <small>{proxyVerificationLabel(identity.verification_state, t)}</small>
          </>
        ) : "—"}
      </dd>
    </div>
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
          <Link
            className="button secondary inline-button"
            search={{ address }}
            to="/verify"
          >
            {t("contracts.submitVerification")}
          </Link>
        </div>
      ) : null}
      {artifact ? <ContractArtifactPanel artifact={artifact} /> : null}
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
        <article className="history-card" key={`${item.block_hash}:${item.log_index ?? "observation"}:${item.new_implementation.address}`}>
          <div className="history-card-heading">
            <strong>{upgradeChangeLabel(item.change_type, t)}</strong>
            <span>{upgradeEvidenceLabel(item.evidence_type, t)}</span>
          </div>
          <p className="history-transition">
            {item.old_implementation ? <AddressIdentity address={item.old_implementation.address} compact={false} contract /> : <code>—</code>}
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
              <p><small>{t("contracts.history.logIndex")}: <code>{item.log_index}</code></small></p>
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
      {data?.items.length === 0 ? <p className="quiet">{t("contracts.initializations.empty")}</p> : null}
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
          <p><small>{t("contracts.history.logIndex")}: <code>{item.log_index}</code></small></p>
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
        <button className="button secondary" disabled={page <= 1 || loading} onClick={onPrevious} type="button">
          {t("pagination.previous")}
        </button>
        <span>{t("pagination.page", { page })}</span>
        <button className="button secondary" disabled={!nextCursor || loading} onClick={() => nextCursor && onNext(nextCursor)} type="button">
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
      <Link to="/blocks/$blockID" params={{ blockID: blockHash }}>#{blockNumber}</Link>
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
        <> <small>{proxyVerificationLabel(identity.verification_state, t)}</small></>
      ) : null}
    </p>
  );
}

type Translate = ReturnType<typeof useTranslation>["t"];

function proxyStatusLabel(status: ContractProxyDetails["status"], t: Translate): string {
  switch (status) {
    case "not_detected": return t("contracts.proxy.status.notDetected");
    case "detected_unverified": return t("contracts.proxy.status.detectedUnverified");
    case "verified": return t("contracts.proxy.status.verified");
    case "unavailable": return t("contracts.proxy.status.unavailable");
    case "failed": return t("contracts.proxy.status.failed");
  }
}

function proxyDetectionV2StatusLabel(
  status: NonNullable<ContractProxyDetails["proxy_detection_v2"]>["status"],
  t: Translate,
): string {
  switch (status) {
    case "confirmed": return t("contracts.proxy.v2Status.confirmed");
    case "candidate": return t("contracts.proxy.v2Status.candidate");
    case "inconsistent": return t("contracts.proxy.v2Status.inconsistent");
    case "not-detected": return t("contracts.proxy.v2Status.notDetected");
    case "unknown": return t("contracts.proxy.v2Status.unknown");
  }
}

function diamondCompletenessLabel(
  state: "complete" | "partial" | "unknown",
  t: Translate,
): string {
  switch (state) {
    case "complete": return t("contracts.diamond.complete");
    case "partial": return t("contracts.diamond.partial");
    case "unknown": return t("contracts.diamond.unknown");
  }
}

function diamondValidationLabel(
  state: "full" | "sampled" | "interface-only",
  t: Translate,
): string {
  switch (state) {
    case "full": return t("contracts.diamond.fullValidation");
    case "sampled": return t("contracts.diamond.sampledValidation");
    case "interface-only": return t("contracts.diamond.interfaceOnlyValidation");
  }
}

function diamondCutPresenceLabel(
  state: "present" | "absent" | "unknown",
  t: Translate,
): string {
  switch (state) {
    case "present": return t("contracts.diamond.present");
    case "absent": return t("contracts.diamond.absent");
    case "unknown": return t("contracts.diamond.unknown");
  }
}

function diamondCutActionLabel(
  action: "add" | "replace" | "remove",
  t: Translate,
): string {
  switch (action) {
    case "add": return t("contracts.diamond.add");
    case "replace": return t("contracts.diamond.replace");
    case "remove": return t("contracts.diamond.remove");
  }
}

function proxyPatternLabel(
  pattern: NonNullable<ContractProxyDetails["pattern"]>,
  t: Translate,
): string {
  switch (pattern) {
    case "clone": return t("contracts.proxy.enums.pattern.clone");
    case "erc1967": return t("contracts.proxy.enums.pattern.erc1967");
    case "transparent": return t("contracts.proxy.enums.pattern.transparent");
    case "uups": return t("contracts.proxy.enums.pattern.uups");
    case "beacon": return t("contracts.proxy.enums.pattern.beacon");
    case "unknown": return t("contracts.proxy.enums.pattern.unknown");
  }
}

function proxyMechanismLabel(
  mechanism: NonNullable<ContractProxyDetails["mechanism"]>,
  t: Translate,
): string {
  switch (mechanism) {
    case "eip1167": return t("contracts.proxy.enums.mechanism.eip1167");
    case "eip1967": return t("contracts.proxy.enums.mechanism.eip1967");
    case "beacon": return t("contracts.proxy.enums.mechanism.beacon");
  }
}

function proxyEvidenceStateLabel(
  state: NonNullable<ContractProxyDetails["evidence_state"]>,
  t: Translate,
): string {
  switch (state) {
    case "exact": return t("contracts.proxy.enums.evidenceState.exact");
    case "partial": return t("contracts.proxy.enums.evidenceState.partial");
    case "generic": return t("contracts.proxy.enums.evidenceState.generic");
  }
}

function proxyConfidenceLabel(
  confidence: NonNullable<ContractProxyDetails["confidence"]>,
  t: Translate,
): string {
  switch (confidence) {
    case "verified": return t("contracts.proxy.enums.confidence.verified");
    case "high": return t("contracts.proxy.enums.confidence.high");
    case "inferred": return t("contracts.proxy.enums.confidence.inferred");
    case "guess": return t("contracts.proxy.enums.confidence.guess");
  }
}

function proxyVerificationLabel(
  state: "unverified" | "verified",
  t: Translate,
): string {
  switch (state) {
    case "unverified": return t("contracts.proxy.enums.verification.unverified");
    case "verified": return t("contracts.proxy.enums.verification.verified");
  }
}

function proxyEvidenceSourceLabel(
  source: ContractProxyDetails["evidence"][number]["source"],
  t: Translate,
): string {
  switch (source) {
    case "runtime_code": return t("contracts.proxy.enums.evidenceSource.runtimeCode");
    case "runtime_immutable": return t("contracts.proxy.enums.evidenceSource.runtimeImmutable");
    case "implementation_slot": return t("contracts.proxy.enums.evidenceSource.implementationSlot");
    case "admin_slot": return t("contracts.proxy.enums.evidenceSource.adminSlot");
    case "beacon_slot": return t("contracts.proxy.enums.evidenceSource.beaconSlot");
    case "direct_call": return t("contracts.proxy.enums.evidenceSource.directCall");
    case "verified_artifact": return t("contracts.proxy.enums.evidenceSource.verifiedArtifact");
    case "event": return t("contracts.proxy.enums.evidenceSource.event");
  }
}

function proxyEvidenceSubjectLabel(
  subject: ContractProxyDetails["evidence"][number]["subject"],
  t: Translate,
): string {
  switch (subject) {
    case "proxy": return t("contracts.proxy.enums.evidenceSubject.proxy");
    case "implementation": return t("contracts.proxy.enums.evidenceSubject.implementation");
    case "admin": return t("contracts.proxy.enums.evidenceSubject.admin");
    case "beacon": return t("contracts.proxy.enums.evidenceSubject.beacon");
    case "management": return t("contracts.proxy.enums.evidenceSubject.management");
  }
}

function proxyEvidenceResultLabel(
  result: ContractProxyDetails["evidence"][number]["result"],
  t: Translate,
): string {
  switch (result) {
    case "authoritative": return t("contracts.proxy.enums.evidenceResult.authoritative");
    case "corroborating": return t("contracts.proxy.enums.evidenceResult.corroborating");
    case "conflicting": return t("contracts.proxy.enums.evidenceResult.conflicting");
    case "rejected": return t("contracts.proxy.enums.evidenceResult.rejected");
  }
}

function proxyManagementLabel(
  kind: "proxy_admin" | "upgradeable_beacon",
  t: Translate,
): string {
  switch (kind) {
    case "proxy_admin": return t("contracts.proxy.enums.management.proxyAdmin");
    case "upgradeable_beacon": return t("contracts.proxy.enums.management.upgradeableBeacon");
  }
}

function historyCoverageLabel(
  state: "complete" | "partial",
  t: Translate,
): string {
  switch (state) {
    case "complete": return t("contracts.history.coverageComplete");
    case "partial": return t("contracts.history.coveragePartial");
  }
}

function upgradeEvidenceLabel(
  evidence: "event" | "observation",
  t: Translate,
): string {
  switch (evidence) {
    case "event": return t("contracts.history.evidenceEvent");
    case "observation": return t("contracts.history.evidenceObservation");
  }
}

function upgradeChangeLabel(
  change: "implementation" | "beacon" | "beacon_implementation",
  t: Translate,
): string {
  switch (change) {
    case "implementation": return t("contracts.upgrades.implementation");
    case "beacon": return t("contracts.upgrades.beacon");
    case "beacon_implementation": return t("contracts.upgrades.beaconImplementation");
  }
}
