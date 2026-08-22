
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
  blockTab,
}: {
  kind: EntityKind;
  identifier: string;
  secondary?: string;
  transactionTab?: string;
  addressTab?: string;
  blockTab?: string;
}) {
  switch (kind) {
    case "block":
      return <BlockDetailPage identifier={identifier} tab={blockTab ?? "overview"} />;
    case "transaction":
      return <TransactionDetailPage hash={identifier} tab={transactionTab ?? "overview"} />;
    case "address":
      return <AddressDetailPage address={identifier} tab={addressTab ?? "transactions"} />;
    case "token":
      return <TokenDetailPage address={identifier} />;
    case "nft":
      return <NFTDetailPage address={identifier} tokenID={secondary ?? ""} />;
  }
}
