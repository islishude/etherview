import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { CopyableField } from "@/components/CopyButton";
import { usePrimaryName } from "./AddressNamesProvider";

export function AddressIdentity({
  address,
  activity = false,
  compact = true,
  copy = false,
  contract = false,
  link = true,
  suffix,
}: {
  address: string;
  activity?: boolean;
  compact?: boolean;
  copy?: boolean;
  contract?: boolean;
  link?: boolean;
  suffix?: ReactNode;
}) {
  const primary = usePrimaryName(address);
  const { t } = useTranslation();
  const system = address.toLowerCase() === "0xfffffffffffffffffffffffffffffffffffffffe";
  const content = (
    <span className={`address-identity${primary ? " has-primary-name" : ""}`}>
      {primary ? (
        <span className="address-primary-name-row">
          <bdi className="address-primary-name" title={primary.name}>
            {primary.name}
          </bdi>
          {primary.source === "custom_ens" ? (
            <small className="custom-ens-badge">Custom ENS</small>
          ) : null}
        </span>
      ) : null}
      <code className="address-identity-value" title={address}>
        {compact ? shortenAddress(address) : address}
      </code>
      {system && <small className="system-address-badge">{t("detail.systemAddress")}</small>}
      {suffix}
    </span>
  );
  const linked = link ? (
    <Link
      aria-label={[
        primary?.name,
        system ? t("detail.systemAddress") : undefined,
        primary || !compact ? address : shortenAddress(address),
      ]
        .filter(Boolean)
        .join(", ")}
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
  return copy ? <CopyableField value={address}>{linked}</CopyableField> : linked;
}

export function shortenAddress(value: string): string {
  return value.length <= 14 ? value : `${value.slice(0, 8)}…${value.slice(-6)}`;
}
