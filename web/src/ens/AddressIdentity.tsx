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
  const content = (
    <span className={`address-identity${primary ? " has-primary-name" : ""}`}>
      {primary ? (
        <span className="address-primary-name-row">
          <bdi className="address-primary-name" title={primary.name}>{primary.name}</bdi>
          {primary.source === "custom_ens" ? <small className="custom-ens-badge">Custom ENS</small> : null}
        </span>
      ) : null}
      <code className="address-identity-value" title={address}>
        {compact ? shortenAddress(address) : address}
      </code>
      {suffix}
    </span>
  );
  const linked = link ? (
    <Link
      aria-label={primary ? `${primary.name}, ${address}` : compact ? shortenAddress(address) : address}
      hash={contract ? "code" : undefined}
      params={{ address }}
      search={contract ? {} : activity ? { tab: "transactions" } : {}}
      to="/address/$address"
    >
      {content}
    </Link>
  ) : content;
  return copy ? <CopyableField value={address}>{linked}</CopyableField> : linked;
}

export function shortenAddress(value: string): string {
  return value.length <= 14 ? value : `${value.slice(0, 8)}…${value.slice(-6)}`;
}
