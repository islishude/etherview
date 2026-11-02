import { Link } from "@tanstack/react-router";
import { usePublicConfig } from "@/api/publicConfig";
import { CopyableField } from "@/components/CopyButton";
import { shorten } from "@/components/format";
import { chainsMatch } from "@/wallet/eip6963";

export function BillingTransactionHash({ hash, network }: { hash?: string; network?: string }) {
  const config = usePublicConfig();
  if (!hash) return <>—</>;
  const local =
    network?.startsWith("eip155:") &&
    chainsMatch(network.slice("eip155:".length), config.data?.chain_id);
  const value = <code title={hash}>{shorten(hash)}</code>;
  return (
    <CopyableField value={hash}>
      {local ? (
        <Link to="/tx/$hash" params={{ hash }} search={{ tab: "overview" }}>
          {value}
        </Link>
      ) : (
        value
      )}
    </CopyableField>
  );
}
