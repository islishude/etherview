import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { isAddress } from "viem";

import { useAddressERC20Balances, useAddressNFTBalances } from "@/api/hooks";
import { useBrowsingCursorHistory } from "@/api/pagination";
import type { ERC20Balance, NFTBalance } from "@/api/types";
import { BrowsingEmpty, BrowsingScope } from "@/components/BrowsingScope";
import { formatInteger, formatTokenAmount, shorten } from "@/components/format";
import { QueryNotice } from "@/components/QueryNotice";
import { CORE_PAGE_SIZE, CursorPagination, NFTTokenIDLink, confidenceLabel } from "./pages";
import "./AddressAssets.css";

export type AddressAssetTab = "tokens" | "nfts";

export function AddressAssets({ address, asset }: { address: string; asset: AddressAssetTab }) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const tokenPager = useBrowsingCursorHistory(`address-erc20-balances:${address}`);
  const nftPager = useBrowsingCursorHistory(`address-nfts:${address}`);
  const tokens = useAddressERC20Balances(
    address,
    tokenPager.request,
    asset === "tokens" && isAddress(address),
    CORE_PAGE_SIZE,
  );
  const nfts = useAddressNFTBalances(
    address,
    nftPager.request,
    asset === "nfts" && isAddress(address),
    CORE_PAGE_SIZE,
  );
  const query = asset === "tokens" ? tokens : nfts;
  const pager = asset === "tokens" ? tokenPager : nftPager;
  const data = query.error ? undefined : query.data;
  const title = t(asset === "tokens" ? "detail.erc20Balances" : "detail.nftBalances");

  return (
    <div className="address-assets">
      <nav className="address-asset-tabs" aria-label={t("addressAssets.navigation")}>
        {(["tokens", "nfts"] as const).map((kind) => (
          <Link
            key={kind}
            to="/address/$address"
            params={{ address }}
            search={{ tab: "assets", asset: kind }}
            hash=""
            activeProps={{ className: "" }}
            className="address-asset-tab"
            aria-current={asset === kind ? "page" : undefined}
          >
            {t(`addressAssets.${kind}`)}
          </Link>
        ))}
      </nav>
      <section className="address-asset-content" aria-label={title}>
        <h2 className="sr-only">{title}</h2>
        <BrowsingScope status={data?.meta.browsing}>
          {data?.meta.coverage_end && (
            <p className="context-note address-asset-snapshot" role="note">
              {t(asset === "tokens" ? "detail.assetSnapshot" : "detail.nftSnapshot", {
                block: formatInteger(data.meta.coverage_end, locale),
              })}
            </p>
          )}
          <QueryNotice loading={query.isPending} error={query.error} onReset={pager.reset} />
          {data?.items.length === 0 && (
            <p className="empty-result address-asset-empty" role="status">
              <BrowsingEmpty>
                {t(asset === "tokens" ? "state.noERC20Balances" : "state.noNFTBalances")}
              </BrowsingEmpty>
            </p>
          )}
          {asset === "tokens" && !tokens.error && tokens.data && tokens.data.items.length > 0 && (
            <TokenHoldings balances={tokens.data.items} locale={locale} />
          )}
          {asset === "nfts" && !nfts.error && nfts.data && nfts.data.items.length > 0 && (
            <NFTHoldings balances={nfts.data.items} locale={locale} />
          )}
          {data && (data.items.length > 0 || pager.hasPrevious || data.next_cursor) && (
            <CursorPagination
              busy={query.isFetching}
              hasNext={Boolean(data.next_cursor)}
              hasPrevious={pager.hasPrevious}
              label={t(asset === "tokens" ? "pagination.erc20Balances" : "pagination.nfts")}
              onNext={() => pager.next(data.next_cursor)}
              onPrevious={pager.previous}
              page={pager.page}
            />
          )}
        </BrowsingScope>
      </section>
    </div>
  );
}

function TokenHoldings({ balances, locale }: { balances: ERC20Balance[]; locale: string }) {
  const { t } = useTranslation();
  return (
    <div className="address-token-holdings">
      <div className="address-token-columns" aria-hidden="true">
        <span>{t("table.token")}</span>
        <span>{t("addressAssets.balance")}</span>
      </div>
      <ul className="address-token-list" aria-label={t("detail.erc20Balances")}>
        {balances.map((balance) => (
          <li className="address-token-row" key={balance.token_address}>
            <div className="address-token-identity">
              <Link
                to="/token/$address"
                params={{ address: balance.token_address }}
                title={balance.name || balance.symbol || balance.token_address}
                className="address-token-name"
              >
                {balance.name || balance.symbol || shorten(balance.token_address)}
              </Link>
              <div className="address-token-meta">
                {balance.symbol && <span title={balance.symbol}>{balance.symbol}</span>}
                <code title={balance.token_address}>{shorten(balance.token_address)}</code>
              </div>
            </div>
            <div className="address-token-amount">
              <span
                className="address-asset-quantity"
                title={formatTokenAmount(balance.balance, balance.decimals, locale)}
              >
                {formatTokenAmount(balance.balance, balance.decimals, locale)}
              </span>
              {balance.decimals === undefined && <small>{t("addressAssets.rawAmount")}</small>}
              <small>{confidenceLabel(balance.confidence, t)}</small>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

function NFTHoldings({ balances, locale }: { balances: NFTBalance[]; locale: string }) {
  const { t } = useTranslation();
  return (
    <ul className="address-nft-grid" aria-label={t("detail.nftBalances")}>
      {balances.map((balance) => (
        <li className="address-nft-card" key={`${balance.token_address}:${balance.token_id}`}>
          <h3>
            <NFTTokenIDLink
              address={balance.token_address}
              tokenID={balance.token_id}
              compact
              prefix
            />
          </h3>
          <Link
            className="address-nft-contract"
            to="/token/$address"
            params={{ address: balance.token_address }}
            title={balance.token_address}
          >
            <code>{shorten(balance.token_address)}</code>
          </Link>
          <div className="address-nft-details">
            {BigInt(balance.balance) > 1n && (
              <span>
                {t("addressAssets.quantity", { quantity: formatInteger(balance.balance, locale) })}
              </span>
            )}
            <small>{confidenceLabel(balance.confidence, t)}</small>
          </div>
        </li>
      ))}
    </ul>
  );
}
