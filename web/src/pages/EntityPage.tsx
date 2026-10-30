import type { AddressAssetTab } from "./AddressAssets";
import { AddressDetailPage } from "./AddressPage";
import { BlockDetailPage } from "./BlockPage";
import { TokenDetailPage, NFTDetailPage } from "./TokenNFTPage";
import { TransactionDetailPage } from "./TransactionPage";

export type EntityKind = "block" | "transaction" | "address" | "token" | "nft";

export function EntityPage({
  kind,
  identifier,
  secondary,
  transactionTab,
  addressTab,
  addressAsset,
  blockTab,
}: {
  kind: EntityKind;
  identifier: string;
  secondary?: string;
  transactionTab?: string;
  addressTab?: string;
  addressAsset?: AddressAssetTab;
  blockTab?: string;
}) {
  switch (kind) {
    case "block":
      return <BlockDetailPage identifier={identifier} tab={blockTab ?? "overview"} />;
    case "transaction":
      return <TransactionDetailPage hash={identifier} tab={transactionTab ?? "overview"} />;
    case "address":
      return (
        <AddressDetailPage
          address={identifier}
          tab={addressTab ?? "transactions"}
          asset={addressAsset}
        />
      );
    case "token":
      return <TokenDetailPage address={identifier} />;
    case "nft":
      return <NFTDetailPage address={identifier} tokenID={secondary ?? ""} />;
  }
}
