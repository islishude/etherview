import "./AddressIdentity.css";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { CopyableField } from "@/components/CopyButton";
import { usePrimaryName } from "./AddressNamesProvider";
import { SystemAddressMarker } from "./SystemAddressMarker";

export function AddressIdentity({
  address,
  activity = false,
  compact = true,
  nameOnly = false,
  copy = false,
  contract = false,
  link = true,
  suffix,
  actions,
}: {
  address: string;
  activity?: boolean;
  compact?: boolean;
  nameOnly?: boolean;
  copy?: boolean;
  contract?: boolean;
  link?: boolean;
  suffix?: ReactNode;
  actions?: ReactNode;
}) {
  const primary = usePrimaryName(address);
  const { t } = useTranslation();
  const system = address.toLowerCase() === "0xfffffffffffffffffffffffffffffffffffffffe";
  const content = (
    <span
      className={`address-identity${primary && !nameOnly ? " has-primary-name" : ""}${compact ? " is-compact" : ""}`}
    >
      {primary && nameOnly ? (
        <bdi className="address-name-link" title={primary.name}>
          {primary.name}
        </bdi>
      ) : (
        <code className="address-identity-value" title={address}>
          {compact ? shortenAddress(address) : address}
        </code>
      )}
      {suffix}
    </span>
  );
  const linked = link ? (
    <Link
      aria-label={
        primary && nameOnly
          ? primary.name
          : [
              primary?.name,
              system ? t("detail.systemAddress") : undefined,
              primary || !compact ? address : shortenAddress(address),
            ]
              .filter(Boolean)
              .join(", ")
      }
      hash={contract ? "code" : undefined}
      params={{ address }}
      search={contract ? {} : activity ? { tab: "transactions" } : {}}
      to="/address/$address"
    >
      {content}
    </Link>
  ) : (
    content
  );
  const identity = system ? (
    <span className={`address-system-identity${!compact && !primary ? " is-full" : ""}`}>
      {linked}
      <SystemAddressMarker />
    </span>
  ) : (
    linked
  );
  const addressContent = copy ? (
    <CopyableField value={address}>{identity}</CopyableField>
  ) : (
    identity
  );
  const addressRow = actions ? (
    <span className="address-identity-address-row">
      {addressContent}
      {actions}
    </span>
  ) : (
    addressContent
  );
  return primary && !nameOnly ? (
    <span className="address-identity has-primary-name">
      <Link className="address-primary-name-row" to="/name/$name" params={{ name: primary.name }}>
        <bdi className="address-primary-name" title={primary.name}>
          {primary.name}
        </bdi>
      </Link>
      {addressRow}
    </span>
  ) : (
    addressRow
  );
}

export function shortenAddress(value: string): string {
  return value.length <= 14 ? value : `${value.slice(0, 8)}…${value.slice(-6)}`;
}
