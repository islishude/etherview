import { useTranslation } from "react-i18next";
import { CopyableField } from "@/components/CopyButton";
import { QueryNotice } from "@/components/QueryNotice";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { type ContractProxyDetails } from "@/contracts/proxy";

export type PublicProxyTarget = NonNullable<
  NonNullable<ContractProxyDetails["proxy_detection_v2"]>["primary"]
>["targets"][number];

export function ProxySummary({
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
  const diamondOutcome = detectionV2?.outcomes.find(
    (outcome) =>
      outcome.family === "erc2535" &&
      outcome.diamond !== undefined &&
      outcome.status !== "not-detected",
  );
  const diamond = diamondOutcome?.diamond;
  const layers = detectionV2?.outcomes
    .filter((outcome) => outcome.family && outcome.status !== "not-detected")
    .map((outcome) => `${outcome.family}${outcome.variant ? ` (${outcome.variant})` : ""}`)
    .join(" → ");
  const eyebrow = proxyEyebrow(detail, v2Primary?.family, diamond !== undefined);
  return (
    <details className="panel proxy-summary">
      <summary className="panel-heading">
        <div>
          <span className="eyebrow">{eyebrow}</span>
          <h2 id="proxy-summary-title">{t("contracts.proxy.title")}</h2>
        </div>
        {detail ? (
          <span
            className={
              detail.status === "verified" || detectionV2?.status === "confirmed"
                ? "availability yes"
                : "availability no"
            }
          >
            {detectionV2
              ? proxyDetectionV2StatusLabel(detectionV2.status, t)
              : proxyStatusLabel(detail.status, t)}
          </span>
        ) : null}
      </summary>
      <QueryNotice loading={loading} error={error} />
      {detail?.status === "not_detected" && !v2Detected ? (
        <p className="quiet">{t("contracts.proxy.notDetected")}</p>
      ) : null}
      {detail && (detail.status !== "not_detected" || v2Detected) ? (
        <dl className="proxy-facts">
          <ProxyDetectionFacts
            detectionV2={detectionV2}
            diamond={diamond}
            layers={layers}
            primary={v2Primary}
            t={t}
          />
          {detail.pattern ? (
            <Fact
              label={t("contracts.proxy.pattern")}
              value={proxyPatternLabel(detail.pattern, t)}
            />
          ) : null}
          {detail.mechanism ? (
            <Fact
              label={t("contracts.proxy.mechanism")}
              value={proxyMechanismLabel(detail.mechanism, t)}
            />
          ) : null}
          {detail.evidence_state ? (
            <Fact
              label={t("contracts.proxy.evidenceState")}
              value={proxyEvidenceStateLabel(detail.evidence_state, t)}
            />
          ) : null}
          {detail.confidence ? (
            <Fact
              label={t("contracts.proxy.confidence")}
              value={proxyConfidenceLabel(detail.confidence, t)}
            />
          ) : null}
          {detail.standard_version ? (
            <Fact label={t("contracts.proxy.standardVersion")} value={detail.standard_version} />
          ) : null}
          {detail.implementation ? (
            <IdentityFact
              label={t("contracts.proxy.implementation")}
              identity={detail.implementation}
            />
          ) : null}
          {detail.admin ? (
            <IdentityFact label={t("contracts.proxy.admin")} identity={detail.admin} />
          ) : null}
          {detail.beacon ? (
            <IdentityFact label={t("contracts.proxy.beacon")} identity={detail.beacon} />
          ) : null}
          {detail.management?.target ? (
            <IdentityFact
              label={t("contracts.proxy.managementTarget")}
              identity={detail.management.target}
            />
          ) : null}
          {detail.management?.affected_proxy_count ? (
            <Fact
              label={t("contracts.proxy.affectedProxies")}
              value={detail.management.affected_proxy_count}
            />
          ) : null}
          {detail.binding_id ? (
            <Fact label={t("contracts.proxy.binding")} value={detail.binding_id} mono />
          ) : null}
          <Fact
            label={t("contracts.proxy.snapshot")}
            value={`${detail.snapshot.block_number} · ${detail.snapshot.block_hash}`}
            mono
          />
          <ImmutableArgsFacts detail={detail} t={t} />
        </dl>
      ) : null}
      <CWIAImmutableArgsDetails detail={detail} t={t} />
      {detail && detail.evidence.length > 0 ? (
        <details className="proxy-evidence">
          <summary>{t("contracts.proxy.evidence", { count: detail.evidence.length })}</summary>
          <ul>
            {detail.evidence.map((item, index) => (
              <li key={`${item.source}:${item.subject}:${item.block_hash ?? "snapshot"}:${index}`}>
                {proxyEvidenceSourceLabel(item.source, t)} ·{" "}
                {proxyEvidenceSubjectLabel(item.subject, t)} ·{" "}
                {proxyEvidenceResultLabel(item.result, t)}
                {item.address ? (
                  <>
                    {" "}
                    · <code>{item.address}</code>
                  </>
                ) : null}
                {item.block_number ? <> · #{item.block_number}</> : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {diamond ? (
        <p className="context-note" role="note">
          {t("contracts.diamond.interactionSafety")}
        </p>
      ) : detail?.mechanism === "cwia" ? null : detail &&
        detail.status !== "verified" &&
        detail.status !== "not_detected" ? (
        <p className="chain-warning" role="status">
          {t("contracts.proxy.writeDisabled")}
        </p>
      ) : null}
      <CloneImmutabilityNote detail={detail} t={t} />
    </details>
  );
}

type ProxyDetectionV2 = NonNullable<ContractProxyDetails["proxy_detection_v2"]>;

type ProxyDiamond = NonNullable<ProxyDetectionV2["outcomes"][number]["diamond"]>;

function ProxyDetectionFacts({
  detectionV2,
  diamond,
  layers,
  primary,
  t,
}: {
  detectionV2?: ProxyDetectionV2;
  diamond?: ProxyDiamond;
  layers?: string;
  primary?: ProxyDetectionV2["primary"];
  t: Translate;
}) {
  if (!detectionV2) return null;
  const v2Primary = primary;
  return (
    <>
      <Fact
        label={t("contracts.proxy.detectorStatus")}
        value={proxyDetectionV2StatusLabel(detectionV2.status, t)}
      />
      <Fact label={t("contracts.proxy.family")} value={v2Primary?.family ?? "—"} />
      <Fact label={t("contracts.proxy.variant")} value={v2Primary?.variant ?? "—"} />
      {layers ? <Fact label={t("contracts.proxy.layers")} value={layers} /> : null}
      {v2Primary?.implementation ? (
        <Fact
          label={
            v2Primary.implementation_role === "singleton"
              ? t("contracts.proxy.singleton")
              : t("contracts.proxy.implementation")
          }
          value={v2Primary.implementation}
          mono
        />
      ) : null}
      {v2Primary?.family === "safe" ? (
        <Fact
          label={t("contracts.proxy.officialSingleton")}
          value={v2Primary.official_singleton ? t("common.yes") : t("common.no")}
        />
      ) : null}
      <Fact
        label={t("contracts.proxy.detectorVersion")}
        value={v2Primary?.detector_version ?? "—"}
      />
      {v2Primary && v2Primary.warnings.length > 0 ? (
        <Fact label={t("contracts.proxy.warnings")} value={v2Primary.warnings.join(" · ")} />
      ) : null}
      {detectionV2.conflicts.length > 0 ? (
        <Fact label={t("contracts.proxy.conflicts")} value={detectionV2.conflicts.join(" · ")} />
      ) : null}
      {diamond ? (
        <>
          <Fact label={t("contracts.diamond.facets")} value={String(diamond.facets.length)} />
          <Fact
            label={t("contracts.diamond.selectors")}
            value={String(Object.keys(diamond.selector_to_facet).length)}
          />
          <Fact
            label={t("contracts.diamond.completeness")}
            value={diamondCompletenessLabel(diamond.completeness, t)}
          />
          <Fact
            label={t("contracts.diamond.validation")}
            value={diamondValidationLabel(diamond.validation, t)}
          />
          <Fact
            label={t("contracts.diamond.standardCut")}
            value={`${diamondCutPresenceLabel(diamond.standard_diamond_cut.status, t)}${diamond.standard_diamond_cut.facet ? ` · ${diamond.standard_diamond_cut.facet}` : ""}`}
            mono={diamond.standard_diamond_cut.facet !== undefined}
          />
          <Fact
            label={t("contracts.diamond.loupeInterface")}
            value={
              diamond.loupe_interface_reported === undefined
                ? t("contracts.diamond.unknown")
                : diamond.loupe_interface_reported
                  ? t("common.yes")
                  : t("common.no")
            }
          />
          <Fact
            label={t("contracts.diamond.externalFacets")}
            value={diamond.implementation_addresses.join(" · ") || "—"}
            mono
          />
          {diamond.truncated ? (
            <Fact
              label={t("contracts.diamond.truncated")}
              value={diamond.truncation_reason ?? t("contracts.diamond.unknown")}
            />
          ) : null}
        </>
      ) : null}
    </>
  );
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd className={mono ? "mono-wrap" : undefined}>{value}</dd>
    </div>
  );
}

function proxyEyebrow(
  detail: ContractProxyDetails | undefined,
  family: string | undefined,
  diamond: boolean,
): string {
  if (diamond) return "ERC-2535 Diamond";
  if (detail?.mechanism === "cwia" || family === "cwia") return "Solady legacy CWIA";
  if (family === "safe") return "Safe Proxy";
  return "OpenZeppelin 5.x";
}

function ImmutableArgsFacts({ detail, t }: { detail: ContractProxyDetails; t: Translate }) {
  return (
    <>
      {detail.immutable_args ? (
        <Fact label={t("contracts.proxy.immutableArgs")} value={detail.immutable_args} mono />
      ) : null}
      {detail.immutable_args_decoding ? (
        <Fact
          label={t("contracts.proxy.cwia.schemaStatus")}
          value={cwiaDecodingStatusLabel(detail.immutable_args_decoding, t)}
        />
      ) : null}
      {detail.immutable_args_decoding?.schema ? (
        <>
          <Fact
            label={t("contracts.proxy.cwia.schemaSource")}
            value={t("contracts.proxy.cwia.source.solidityAst")}
          />
          {detail.immutable_args_decoding.schema_resolution ? (
            <Fact
              label={t("contracts.proxy.cwia.schemaResolution")}
              value={t(
                `contracts.proxy.cwia.resolution.${detail.immutable_args_decoding.schema_resolution}`,
              )}
            />
          ) : null}
          <Fact
            label={t("contracts.proxy.cwia.helperDigest")}
            value={detail.immutable_args_decoding.schema.helper_sha256}
            mono
          />
          <Fact
            label={t("contracts.proxy.cwia.schemaDigest")}
            value={detail.immutable_args_decoding.schema.sha256}
            mono
          />
        </>
      ) : null}
    </>
  );
}

function CloneImmutabilityNote({
  detail,
  t,
}: {
  detail: ContractProxyDetails | undefined;
  t: Translate;
}) {
  if (detail?.pattern !== "clone") return null;
  return (
    <p className="context-note" role="note">
      {t(
        detail.mechanism === "cwia"
          ? "contracts.proxy.cwia.immutable"
          : "contracts.proxy.cloneImmutable",
      )}
    </p>
  );
}

function CWIAImmutableArgsDetails({
  detail,
  t,
}: {
  detail: ContractProxyDetails | undefined;
  t: Translate;
}) {
  const decoding = detail?.immutable_args_decoding;
  if (!decoding) return null;
  if (decoding.status !== "decoded") {
    return (
      <p className="chain-warning" role="status">
        {t("contracts.proxy.cwia.readOnly", {
          reason: cwiaDecodingReasonLabel(decoding.reason, t),
        })}
      </p>
    );
  }
  return (
    <>
      {detail?.status !== "verified" ? (
        <p className="chain-warning" role="status">
          {t("contracts.proxy.cwia.readOnly", {
            reason: t("contracts.proxy.cwia.reason.proxyNotVerified"),
          })}
        </p>
      ) : null}
      <CWIAImmutableArgumentsTable arguments={decoding.arguments} t={t} />
    </>
  );
}

export function formatCWIAArgumentValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value) && value.every((item) => typeof item === "string")) {
    return JSON.stringify(value);
  }
  return "—";
}

type CWIAArgument = NonNullable<
  ContractProxyDetails["immutable_args_decoding"]
>["arguments"][number];

export type CWIAArgumentRow = Readonly<{
  key: string;
  name: string;
  type: string;
  offset: number;
  data: string;
  depth: 0 | 1;
  composite: boolean;
}>;

export type CWIAArgumentOmission = Readonly<{
  name: string;
  count: number;
}>;

const MAX_CWIA_ARRAY_ROWS = 64;

export function flattenCWIAArgumentRows(arguments_: readonly CWIAArgument[]): Readonly<{
  rows: readonly CWIAArgumentRow[];
  omissions: readonly CWIAArgumentOmission[];
}> {
  const rows: CWIAArgumentRow[] = [];
  const omissions: CWIAArgumentOmission[] = [];
  for (const argument of arguments_) {
    const data = formatCWIAArgumentValue(argument.value);
    const values =
      Array.isArray(argument.value) && argument.value.every((item) => typeof item === "string")
        ? argument.value
        : undefined;
    rows.push({
      key: `${argument.offset}:${argument.name}`,
      name: argument.name,
      type: argument.type,
      offset: argument.offset,
      data,
      depth: 0,
      composite: values !== undefined,
    });
    if (!values) continue;
    const elementType = argument.type.replace(/\[\]$/u, "");
    for (const [index, value] of values.slice(0, MAX_CWIA_ARRAY_ROWS).entries()) {
      rows.push({
        key: `${argument.offset}:${argument.name}:${index}`,
        name: `${argument.name}[${index}]`,
        type: elementType,
        offset: argument.offset + index * 32,
        data: value,
        depth: 1,
        composite: false,
      });
    }
    const omitted = values.length - MAX_CWIA_ARRAY_ROWS;
    if (omitted > 0) omissions.push({ name: argument.name, count: omitted });
  }
  return { rows, omissions };
}

function CWIAImmutableArgumentsTable({
  arguments: arguments_,
  t,
}: {
  arguments: readonly CWIAArgument[];
  t: Translate;
}) {
  const flattened = flattenCWIAArgumentRows(arguments_);
  return (
    <section aria-label={t("contracts.proxy.cwia.arguments")} className="proxy-evidence">
      <h3>{t("contracts.proxy.cwia.arguments")}</h3>
      <div className="cwia-arguments-scroll" tabIndex={0}>
        <div
          className="cwia-arguments-table"
          role="table"
          aria-label={t("contracts.proxy.cwia.arguments")}
        >
          <div className="cwia-arguments-row cwia-arguments-header" role="row">
            <span role="columnheader">{t("contracts.proxy.cwia.columns.name")}</span>
            <span role="columnheader">{t("contracts.proxy.cwia.columns.type")}</span>
            <span role="columnheader">{t("contracts.proxy.cwia.columns.offset")}</span>
            <span role="columnheader">{t("contracts.proxy.cwia.columns.data")}</span>
          </div>
          {flattened.rows.map((row) => (
            <div
              className={`cwia-arguments-row cwia-argument-depth-${row.depth}${row.composite ? " cwia-argument-composite" : ""}`}
              key={row.key}
              role="row"
            >
              <span className="cwia-argument-name" role="cell">
                {row.name}
              </span>
              <code className="cwia-argument-type" role="cell">
                {row.type}
              </code>
              <code className="cwia-argument-offset" role="cell">
                {row.offset}
              </code>
              <span className="cwia-argument-data" role="cell">
                <CopyableField value={row.data}>
                  <code>{row.data}</code>
                </CopyableField>
              </span>
            </div>
          ))}
        </div>
      </div>
      {flattened.omissions.map((omission) => (
        <p className="quiet" key={omission.name} role="note">
          {t("contracts.proxy.cwia.arrayRowsOmitted", omission)}
        </p>
      ))}
    </section>
  );
}

function cwiaDecodingStatusLabel(
  decoding: NonNullable<ContractProxyDetails["immutable_args_decoding"]>,
  t: Translate,
): string {
  switch (decoding.status) {
    case "decoded":
      return t("contracts.proxy.cwia.status.decoded");
    case "schema_unavailable":
      return t("contracts.proxy.cwia.status.schemaUnavailable");
    case "schema_invalid":
      return t("contracts.proxy.cwia.status.schemaInvalid");
    case "data_invalid":
      return t("contracts.proxy.cwia.status.dataInvalid");
  }
}

function cwiaDecodingReasonLabel(
  reason: NonNullable<ContractProxyDetails["immutable_args_decoding"]>["reason"],
  t: Translate,
): string {
  switch (reason) {
    case "ast_unavailable":
      return t("contracts.proxy.cwia.reason.astUnavailable");
    case "malformed_analysis":
      return t("contracts.proxy.cwia.reason.malformedAnalysis");
    case "unsupported_access":
      return t("contracts.proxy.cwia.reason.unsupportedAccess");
    case "ambiguous_layout":
      return t("contracts.proxy.cwia.reason.ambiguousLayout");
    case "incomplete_layout":
      return t("contracts.proxy.cwia.reason.incompleteLayout");
    case "schema_conflict":
      return t("contracts.proxy.cwia.reason.schemaConflict");
    case "limit_exceeded":
      return t("contracts.proxy.cwia.reason.limitExceeded");
    case "length_mismatch":
      return t("contracts.proxy.cwia.reason.lengthMismatch");
    case "noncanonical_value":
      return t("contracts.proxy.cwia.reason.noncanonicalValue");
    case undefined:
      return t("contracts.proxy.cwia.reason.unknown");
  }
}

function IdentityFact({
  label,
  identity,
}: {
  label: string;
  identity?: {
    address: string;
    verification_state: "unverified" | "verified";
    artifact_resolution?: "exact_address" | "code_hash";
  };
}) {
  const { t } = useTranslation();
  return (
    <div>
      <dt>{label}</dt>
      <dd>
        {identity ? (
          <>
            <AddressIdentity address={identity.address} compact={false} contract />{" "}
            <small>{proxyIdentityVerificationLabel(identity, t)}</small>
          </>
        ) : (
          "—"
        )}
      </dd>
    </div>
  );
}

export type Translate = ReturnType<typeof useTranslation>["t"];

function proxyStatusLabel(status: ContractProxyDetails["status"], t: Translate): string {
  switch (status) {
    case "not_detected":
      return t("contracts.proxy.status.notDetected");
    case "detected_unverified":
      return t("contracts.proxy.status.detectedUnverified");
    case "verified":
      return t("contracts.proxy.status.verified");
    case "unavailable":
      return t("contracts.proxy.status.unavailable");
    case "failed":
      return t("contracts.proxy.status.failed");
  }
}

function proxyDetectionV2StatusLabel(
  status: NonNullable<ContractProxyDetails["proxy_detection_v2"]>["status"],
  t: Translate,
): string {
  switch (status) {
    case "confirmed":
      return t("contracts.proxy.v2Status.confirmed");
    case "candidate":
      return t("contracts.proxy.v2Status.candidate");
    case "inconsistent":
      return t("contracts.proxy.v2Status.inconsistent");
    case "not-detected":
      return t("contracts.proxy.v2Status.notDetected");
    case "unknown":
      return t("contracts.proxy.v2Status.unknown");
  }
}

function diamondCompletenessLabel(state: "complete" | "partial" | "unknown", t: Translate): string {
  switch (state) {
    case "complete":
      return t("contracts.diamond.complete");
    case "partial":
      return t("contracts.diamond.partial");
    case "unknown":
      return t("contracts.diamond.unknown");
  }
}

function diamondValidationLabel(
  state: "full" | "sampled" | "interface-only",
  t: Translate,
): string {
  switch (state) {
    case "full":
      return t("contracts.diamond.fullValidation");
    case "sampled":
      return t("contracts.diamond.sampledValidation");
    case "interface-only":
      return t("contracts.diamond.interfaceOnlyValidation");
  }
}

function diamondCutPresenceLabel(state: "present" | "absent" | "unknown", t: Translate): string {
  switch (state) {
    case "present":
      return t("contracts.diamond.present");
    case "absent":
      return t("contracts.diamond.absent");
    case "unknown":
      return t("contracts.diamond.unknown");
  }
}

export function diamondCutActionLabel(action: "add" | "replace" | "remove", t: Translate): string {
  switch (action) {
    case "add":
      return t("contracts.diamond.add");
    case "replace":
      return t("contracts.diamond.replace");
    case "remove":
      return t("contracts.diamond.remove");
  }
}

function proxyPatternLabel(
  pattern: NonNullable<ContractProxyDetails["pattern"]>,
  t: Translate,
): string {
  switch (pattern) {
    case "clone":
      return t("contracts.proxy.enums.pattern.clone");
    case "erc1967":
      return t("contracts.proxy.enums.pattern.erc1967");
    case "transparent":
      return t("contracts.proxy.enums.pattern.transparent");
    case "uups":
      return t("contracts.proxy.enums.pattern.uups");
    case "beacon":
      return t("contracts.proxy.enums.pattern.beacon");
    case "unknown":
      return t("contracts.proxy.enums.pattern.unknown");
  }
}

function proxyMechanismLabel(
  mechanism: NonNullable<ContractProxyDetails["mechanism"]>,
  t: Translate,
): string {
  switch (mechanism) {
    case "eip1167":
      return t("contracts.proxy.enums.mechanism.eip1167");
    case "cwia":
      return t("contracts.proxy.enums.mechanism.cwia");
    case "eip1967":
      return t("contracts.proxy.enums.mechanism.eip1967");
    case "beacon":
      return t("contracts.proxy.enums.mechanism.beacon");
  }
}

function proxyEvidenceStateLabel(
  state: NonNullable<ContractProxyDetails["evidence_state"]>,
  t: Translate,
): string {
  switch (state) {
    case "exact":
      return t("contracts.proxy.enums.evidenceState.exact");
    case "partial":
      return t("contracts.proxy.enums.evidenceState.partial");
    case "generic":
      return t("contracts.proxy.enums.evidenceState.generic");
  }
}

function proxyConfidenceLabel(
  confidence: NonNullable<ContractProxyDetails["confidence"]>,
  t: Translate,
): string {
  switch (confidence) {
    case "verified":
      return t("contracts.proxy.enums.confidence.verified");
    case "high":
      return t("contracts.proxy.enums.confidence.high");
    case "inferred":
      return t("contracts.proxy.enums.confidence.inferred");
    case "guess":
      return t("contracts.proxy.enums.confidence.guess");
  }
}

export function proxyVerificationLabel(state: "unverified" | "verified", t: Translate): string {
  switch (state) {
    case "unverified":
      return t("contracts.proxy.enums.verification.unverified");
    case "verified":
      return t("contracts.proxy.enums.verification.verified");
  }
}

function proxyIdentityVerificationLabel(
  identity: {
    verification_state: "unverified" | "verified";
    artifact_resolution?: "exact_address" | "code_hash";
  },
  t: Translate,
): string {
  if (
    identity.verification_state === "verified" &&
    identity.artifact_resolution === "exact_address"
  ) {
    return t("contracts.proxy.enums.verification.verifiedAtAddress");
  }
  if (
    identity.verification_state === "unverified" &&
    identity.artifact_resolution === "code_hash"
  ) {
    return t("contracts.proxy.enums.verification.verifiedByCodeHash");
  }
  return t("contracts.proxy.enums.verification.unverified");
}

function proxyEvidenceSourceLabel(
  source: ContractProxyDetails["evidence"][number]["source"],
  t: Translate,
): string {
  switch (source) {
    case "runtime_code":
      return t("contracts.proxy.enums.evidenceSource.runtimeCode");
    case "runtime_immutable":
      return t("contracts.proxy.enums.evidenceSource.runtimeImmutable");
    case "implementation_slot":
      return t("contracts.proxy.enums.evidenceSource.implementationSlot");
    case "admin_slot":
      return t("contracts.proxy.enums.evidenceSource.adminSlot");
    case "beacon_slot":
      return t("contracts.proxy.enums.evidenceSource.beaconSlot");
    case "direct_call":
      return t("contracts.proxy.enums.evidenceSource.directCall");
    case "verified_artifact":
      return t("contracts.proxy.enums.evidenceSource.verifiedArtifact");
    case "event":
      return t("contracts.proxy.enums.evidenceSource.event");
  }
}

function proxyEvidenceSubjectLabel(
  subject: ContractProxyDetails["evidence"][number]["subject"],
  t: Translate,
): string {
  switch (subject) {
    case "proxy":
      return t("contracts.proxy.enums.evidenceSubject.proxy");
    case "implementation":
      return t("contracts.proxy.enums.evidenceSubject.implementation");
    case "admin":
      return t("contracts.proxy.enums.evidenceSubject.admin");
    case "beacon":
      return t("contracts.proxy.enums.evidenceSubject.beacon");
    case "management":
      return t("contracts.proxy.enums.evidenceSubject.management");
  }
}

function proxyEvidenceResultLabel(
  result: ContractProxyDetails["evidence"][number]["result"],
  t: Translate,
): string {
  switch (result) {
    case "authoritative":
      return t("contracts.proxy.enums.evidenceResult.authoritative");
    case "corroborating":
      return t("contracts.proxy.enums.evidenceResult.corroborating");
    case "conflicting":
      return t("contracts.proxy.enums.evidenceResult.conflicting");
    case "rejected":
      return t("contracts.proxy.enums.evidenceResult.rejected");
  }
}

export function proxyManagementLabel(
  kind: "proxy_admin" | "upgradeable_beacon",
  t: Translate,
): string {
  switch (kind) {
    case "proxy_admin":
      return t("contracts.proxy.enums.management.proxyAdmin");
    case "upgradeable_beacon":
      return t("contracts.proxy.enums.management.upgradeableBeacon");
  }
}
