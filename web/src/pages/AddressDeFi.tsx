import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { isAddress } from "viem";
import { useAddressDeFiPositions } from "@/api/hooks";
import { useQueryWithdrawn } from "@/api/browsingHooks";
import { useBrowsingCursorHistory } from "@/api/pagination";
import type { DeFiAsset, DeFiPosition, DeFiProtocol } from "@/api/heyapi/types.gen";
import { NativeSelect } from "@/components/ui/native-select";
import { QueryNotice } from "@/components/QueryNotice";
import { formatInteger, formatTokenAmount, shorten } from "@/components/format";
import { CORE_PAGE_SIZE, CursorPagination } from "./pages";
import "./AddressDeFi.css";
import "@/i18n/resources/defi";

const protocols: Record<DeFiProtocol, string> = {
  "uniswap-v2": "Uniswap V2",
  "uniswap-v3": "Uniswap V3",
  "uniswap-v4": "Uniswap V4",
  "aave-v3": "Aave V3",
  curve: "Curve",
  lido: "Lido",
};
export function AddressDeFi({ address }: { address: string }) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const [protocol, setProtocol] = useState<DeFiProtocol | "">("");
  const pager = useBrowsingCursorHistory(`address-defi:${address}:${protocol}`);
  const query = useAddressDeFiPositions(
    address,
    pager.request,
    protocol || undefined,
    isAddress(address),
    CORE_PAGE_SIZE,
  );
  const withdrawn = useQueryWithdrawn([
    "address",
    address,
    "defi-positions",
    protocol || null,
    pager.request.cursor ?? null,
    CORE_PAGE_SIZE,
    pager.request.refreshGeneration,
  ]);
  const data = query.error || withdrawn ? undefined : query.data?.data;
  return (
    <section className="address-defi" aria-label={t("defi.title")}>
      <div className="defi-toolbar">
        <label htmlFor="defi-protocol">{t("defi.protocol")}</label>
        <NativeSelect
          id="defi-protocol"
          value={protocol}
          onChange={(event) => setProtocol(event.target.value as DeFiProtocol | "")}
        >
          <option value="">{t("defi.all")}</option>
          {Object.entries(protocols).map(([id, label]) => (
            <option key={id} value={id}>
              {label}
            </option>
          ))}
        </NativeSelect>
      </div>
      <p className="context-note">{t("defi.scope")}</p>
      <QueryNotice loading={query.isLoading} error={query.error} onReset={pager.reset} />
      {withdrawn && <p role="status">{t("state.cursorInvalidDetail")}</p>}
      {data?.deployments.map((deployment) => {
        const positions = data.positions.filter(
          (position) =>
            position.protocol === deployment.protocol &&
            position.deployment === deployment.deployment,
        );
        return (
          <section
            key={`${deployment.protocol}:${deployment.deployment}`}
            className="defi-protocol-group"
            aria-label={`${protocols[deployment.protocol]} · ${deployment.deployment}`}
          >
            <div className="defi-group-heading">
              <h3>{protocols[deployment.protocol]}</h3>
              <span>{deployment.deployment}</span>
            </div>
            <p className="context-note" role="status">
              {t(`defi.states.${deployment.state}`)}
              {deployment.block_number && (
                <>
                  {" "}
                  ·{" "}
                  <Link
                    to="/block/$blockID"
                    params={{ blockID: deployment.block_hash ?? deployment.block_number }}
                  >
                    {t("defi.snapshot", { block: formatInteger(deployment.block_number, locale) })}
                  </Link>
                </>
              )}
              {deployment.updated_at && (
                <>
                  {" "}
                  ·{" "}
                  <time dateTime={deployment.updated_at}>
                    {new Date(deployment.updated_at).toLocaleString(locale)}
                  </time>
                </>
              )}
            </p>
            {positions.map((position) => (
              <Position key={position.id} position={position} locale={locale} />
            ))}
          </section>
        );
      })}
      {data &&
        data.positions.length === 0 &&
        data.deployments.some((d) => d.state === "complete") &&
        data.deployments
          .filter((d) => d.state !== "not_configured")
          .every((d) => d.state === "complete") && <p role="status">{t("defi.empty")}</p>}
      {data && (data.positions.length > 0 || pager.hasPrevious) && (
        <CursorPagination
          itemCount={data.positions.length}
          busy={query.isFetching}
          onReset={pager.reset}
          hasNext={Boolean(query.data?.meta.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("defi.pagination")}
          onNext={() => pager.next(query.data?.meta.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </section>
  );
}
function Position({ position: p, locale }: { position: DeFiPosition; locale: string }) {
  const { t } = useTranslation();
  return (
    <article className="defi-position">
      <div className="defi-position-heading">
        <Link to="/address/$address" params={{ address: p.contract }} title={p.contract}>
          {shorten(p.contract)}
        </Link>
        {p.token_id !== undefined && <span>#{formatInteger(p.token_id, locale)}</span>}
        <span>{t(`defi.kinds.${p.kind}`)}</span>
        <span>{t(`defi.states.${p.state}`)}</span>
      </div>
      {(["assets", "debts", "claimable", "unsettled_fees"] as const).map(
        (kind) =>
          p[kind].length > 0 && (
            <div className="defi-amount-group" key={kind}>
              <h4>{t(`defi.${kind}`)}</h4>
              <ul>
                {p[kind].map((asset, index) => (
                  <li key={`${asset.address ?? "native"}:${index}`}>
                    <Asset asset={asset} locale={locale} />
                  </li>
                ))}
              </ul>
            </div>
          ),
      )}
      <details>
        <summary>{t("defi.details")}</summary>
        <dl className="defi-details">
          <dt>{t("defi.contract")}</dt>
          <dd>
            <code>{p.contract}</code>
          </dd>
          {p.pool && (
            <>
              <dt>{t("defi.pool")}</dt>
              <dd>
                <Link to="/address/$address" params={{ address: p.pool }}>
                  {p.pool}
                </Link>
              </dd>
            </>
          )}
          {p.pool_id && (
            <>
              <dt>Pool ID</dt>
              <dd>
                <code>{p.pool_id}</code>
              </dd>
            </>
          )}
          {p.receipt_amount !== undefined && (
            <>
              <dt>{t("defi.receipt")}</dt>
              <dd>{formatInteger(p.receipt_amount, locale)}</dd>
            </>
          )}
          {p.liquidity !== undefined && (
            <>
              <dt>{t("defi.liquidity")}</dt>
              <dd>{formatInteger(p.liquidity, locale)}</dd>
            </>
          )}
          {p.range && (
            <>
              <dt>{t("defi.range")}</dt>
              <dd>
                {p.range.lower_tick} ≤ tick &lt; {p.range.upper_tick} ·{" "}
                {t(p.range.in_range ? "defi.inRange" : "defi.outOfRange")}
              </dd>
              <dt>{t("defi.currentTick")}</dt>
              <dd>{p.range.current_tick}</dd>
            </>
          )}
          {p.fee !== undefined && (
            <>
              <dt>{t("defi.fee")}</dt>
              <dd>{p.fee} / 1,000,000</dd>
            </>
          )}
          {p.tick_spacing !== undefined && (
            <>
              <dt>{t("defi.tickSpacing")}</dt>
              <dd>{p.tick_spacing}</dd>
            </>
          )}
          {p.hook && (
            <>
              <dt>Hook</dt>
              <dd>
                <code>{p.hook}</code>
              </dd>
            </>
          )}
          {p.collateral !== undefined && (
            <>
              <dt>{t("defi.collateral")}</dt>
              <dd>{t(p.collateral ? "defi.enabled" : "defi.disabled")}</dd>
            </>
          )}
        </dl>
      </details>
      {p.limitations.map((code) => (
        <p className="context-note" key={code}>
          {t(`defi.limitations.${code}`, { defaultValue: t("defi.limitations.unavailable") })}
        </p>
      ))}
    </article>
  );
}
function Asset({ asset, locale }: { asset: DeFiAsset; locale: string }) {
  const { t } = useTranslation();
  return (
    <>
      <span className="defi-quantity">
        {formatTokenAmount(asset.amount, asset.decimals, locale)}
        {asset.decimals === undefined && <small> {t("addressAssets.rawAmount")}</small>}
      </span>
      {asset.kind === "native" ? (
        <span>{t("defi.native")}</span>
      ) : (
        asset.address && (
          <Link to="/token/$address" params={{ address: asset.address }} title={asset.address}>
            {asset.symbol || shorten(asset.address)}
          </Link>
        )
      )}
    </>
  );
}
