import {
  useEffect,
  useRef,
  useState,
} from "react";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { QRCodeSVG } from "qrcode.react";
import { isAddress, } from "viem";

import {
  useAddressERC20Balances,
  useAddressERC20Transfers,
  useAddressInternalTransactions,
  useAddressNFTBalances,
  useAddressNFTTransfers,
  useAddressTransactions,
  useAddressWithdrawals,
  useAddressUserOperations,
  useAddress,
  usePublicConfig,
} from "@/api/hooks";
import { ContractPage, isContractTabHash } from "./ContractPage";
import {
  DelegatedAccountPanel,
  isDelegatedAccountTabHash,
} from "@/contracts/DelegatedAccountPanel";
import type {
  AddressInternalTransaction,
  AddressSummary,
  AddressTokenTransfer,
  AddressWithdrawal,
  ERC20Balance,
  NFTBalance,
  TransactionSummary,
} from "@/api/types";
import {
  formatEtherFromGwei,
  formatInteger,
  formatNativeAmount,
  formatRelativeTimestamp,
  shorten,
} from "@/components/format";
import { CopyableField } from "@/components/CopyButton";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import { UserOperationTable } from "@/components/UserOperationTable";
import {
  CORE_PAGE_SIZE,
  CursorPagination,
  Detail,
  DetailList,
  FinalityBadge,
  NFTTokenIDLink,
  Page,
  confidenceLabel,
  formatTokenEventAmount,
  isNFTStandard,
  tokenStandardLabel,
  useCursorHistory,
} from "./pages";
import { TransactionStatusBadge } from "./TransactionPage";

type AddressTab =
  | "transactions"
  | "internal-transactions"
  | "withdrawals"
  | "erc20-transfers"
  | "nft-transfers"
  | "assets"
  | "user-operations"
  | "delegation"
  | "contract";

export function AddressDetailPage({ address, tab }: { address: string; tab: string }) {
  const { i18n, t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const requestedHash = location.hash.replace(/^#/u, "");
  const contractHash = isContractTabHash(requestedHash);
  const delegationHash = isDelegatedAccountTabHash(requestedHash);
  const transactionPager = useCursorHistory(`address-transactions:${address}`);
  const internalPager = useCursorHistory(`address-internal-transactions:${address}`);
  const withdrawalPager = useCursorHistory(`address-withdrawals:${address}`);
  const erc20Pager = useCursorHistory(`address-erc20-transfers:${address}`);
  const nftTransferPager = useCursorHistory(`address-nft-transfers:${address}`);
  const erc20BalancePager = useCursorHistory(`address-erc20-balances:${address}`);
  const nftPager = useCursorHistory(`address-nfts:${address}`);
  const userOperationPager = useCursorHistory(`address-user-operations:${address}`);
  const account = useAddress(address);
  const publicConfig = usePublicConfig();
  const userOperationsEnabled = publicConfig.data?.features.user_operations === true;
  const contractAvailable = account.data?.type === "contract" && Boolean(account.data.code_hash);
  const currentlyDelegated = account.data?.type === "delegated_eoa";
  const delegationAvailable = currentlyDelegated || Boolean(account.data?.has_delegation_history);
  const activeTab: AddressTab = account.data?.type === "contract" && contractHash
    ? "contract"
    : (account.isPending || delegationAvailable) && delegationHash
      ? "delegation"
      : isAddressTab(tab) &&
          (tab !== "delegation" || account.isPending || delegationAvailable) &&
          (tab !== "user-operations" || userOperationsEnabled)
        ? tab : "transactions";
  const transactions = useAddressTransactions(
    address,
    transactionPager.cursor,
    CORE_PAGE_SIZE,
    transactionPager.refreshGeneration,
    activeTab === "transactions" && isAddress(address),
  );
  const internalTransactions = useAddressInternalTransactions(
    address,
    internalPager.cursor,
    CORE_PAGE_SIZE,
    internalPager.refreshGeneration,
    activeTab === "internal-transactions" && isAddress(address),
  );
  const withdrawals = useAddressWithdrawals(
    address,
    withdrawalPager.cursor,
    CORE_PAGE_SIZE,
    withdrawalPager.refreshGeneration,
    activeTab === "withdrawals" && isAddress(address),
  );
  const erc20Transfers = useAddressERC20Transfers(
    address,
    erc20Pager.cursor,
    CORE_PAGE_SIZE,
    erc20Pager.refreshGeneration,
    activeTab === "erc20-transfers" && isAddress(address),
  );
  const nftTransfers = useAddressNFTTransfers(
    address,
    nftTransferPager.cursor,
    CORE_PAGE_SIZE,
    nftTransferPager.refreshGeneration,
    activeTab === "nft-transfers" && isAddress(address),
  );
  const nfts = useAddressNFTBalances(
    address,
    nftPager.cursor,
    CORE_PAGE_SIZE,
    nftPager.refreshGeneration,
    activeTab === "assets" && isAddress(address),
  );
  const erc20Balances = useAddressERC20Balances(
    address,
    erc20BalancePager.cursor,
    CORE_PAGE_SIZE,
    erc20BalancePager.refreshGeneration,
    activeTab === "assets" && isAddress(address),
  );
  const userOperations = useAddressUserOperations(
    address,
    userOperationPager.cursor,
    activeTab === "user-operations" && isAddress(address) && userOperationsEnabled,
  );
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const locale = i18n.resolvedLanguage ?? "en";
  const displayAddress = account.data?.address ?? address;
  const title = account.data?.type === "contract"
    ? t("page.contract")
    : account.data?.type === "delegated_eoa"
      ? t("page.delegatedAccount")
      : t("page.address");
  const qrPayload = publicConfig.data
    ? `ethereum:${displayAddress}@${publicConfig.data.chain_id}`
    : undefined;
  useEffect(() => {
    if (!contractHash || delegationHash || account.isPending || account.error || contractAvailable) return;
    void navigate({
      to: "/address/$address",
      params: { address },
      search: {},
      hash: "",
      replace: true,
    });
  }, [account.error, account.isPending, address, contractAvailable, contractHash, delegationHash, navigate]);

  return (
    <Page
      title={title}
      description={(
        <AddressHeader address={displayAddress} qrPayload={qrPayload} />
      )}
      mono
    >
      <QueryNotice loading={account.isPending} error={account.error} />
      {account.data && (
        <>
          <DetailList label={t("detail.addressSummary")}>
            <Detail
              label={t("detail.nativeBalance", { symbol: nativeSymbol })}
              value={`${formatNativeAmount(account.data.balance, locale, nativeDecimals)} ${nativeSymbol}`.trim()}
            />
            <Detail label={t("detail.nonce")} value={formatInteger(account.data.nonce, locale)} />
            <AddressOriginDetails origin={account.data.origin} />
          </DetailList>
          <p className="context-note" role="note">{t("context.addressSnapshot")}</p>
        </>
      )}
      <nav className="transaction-tabs" aria-label={t("detail.addressSections")}>
        {([
          ["transactions", t("addressTab.transactions")],
          ["internal-transactions", t("addressTab.internalTransactions")],
          ["withdrawals", t("addressTab.withdrawals")],
          ["erc20-transfers", t("addressTab.erc20Transfers")],
          ["nft-transfers", t("addressTab.nftTransfers")],
          ["assets", t("addressTab.assets")],
          ...(userOperationsEnabled ? [["user-operations", t("addressTab.userOperations")]] as const : []),
        ] as const).map(([tabID, label]) => (
          <Link
            key={tabID}
            activeOptions={{ exact: true, includeHash: true }}
            activeProps={{ className: "" }}
            className={activeTab === tabID ? "transaction-tab active" : "transaction-tab"}
            to="/address/$address"
            params={{ address }}
            search={{ tab: tabID }}
            hash={() => ""}
            aria-current={activeTab === tabID ? "page" : undefined}
          >
            {label}
          </Link>
        ))}
        {contractAvailable ? (
          <Link
            activeOptions={{ exact: true, includeHash: true }}
            activeProps={{ className: "" }}
            aria-current={activeTab === "contract" ? "page" : undefined}
            className={activeTab === "contract" ? "transaction-tab active" : "transaction-tab"}
            hash="code"
            params={{ address: account.data?.address ?? address }}
            search={{}}
            to="/address/$address"
          >
            {t("addressTab.contract")}
          </Link>
        ) : null}
        {delegationAvailable ? (
          <Link
            activeOptions={{ exact: true, includeHash: true }}
            activeProps={{ className: "" }}
            aria-current={activeTab === "delegation" ? "page" : undefined}
            className={activeTab === "delegation" ? "transaction-tab active" : "transaction-tab"}
            hash={currentlyDelegated ? "code" : "history"}
            params={{ address }}
            search={{ tab: "delegation" }}
            to="/address/$address"
          >
            {t("addressTab.delegation")}
          </Link>
        ) : null}
      </nav>
      {activeTab === "transactions" && (
        <AddressTransactions
          address={address}
          busy={transactions.isFetching}
          error={transactions.error}
          hasNext={Boolean(transactions.data?.next_cursor)}
          items={transactions.data?.items}
          loading={transactions.isPending}
          locale={locale}
          nativeDecimals={nativeDecimals}
          nativeSymbol={nativeSymbol}
          onNext={() => transactionPager.next(transactions.data?.next_cursor)}
          onPrevious={transactionPager.previous}
          onReset={transactionPager.reset}
          page={transactionPager.page}
          hasPrevious={transactionPager.hasPrevious}
        />
      )}
      {activeTab === "internal-transactions" && (
        <AddressInternalTransactions
          address={address}
          busy={internalTransactions.isFetching}
          error={internalTransactions.error}
          hasNext={Boolean(internalTransactions.data?.next_cursor)}
          items={internalTransactions.data?.items}
          loading={internalTransactions.isPending}
          locale={locale}
          nativeDecimals={nativeDecimals}
          nativeSymbol={nativeSymbol}
          onNext={() => internalPager.next(internalTransactions.data?.next_cursor)}
          onPrevious={internalPager.previous}
          onReset={internalPager.reset}
          page={internalPager.page}
          hasPrevious={internalPager.hasPrevious}
        />
      )}
      {activeTab === "withdrawals" && (
        <AddressWithdrawals
          busy={withdrawals.isFetching}
          error={withdrawals.error}
          hasNext={Boolean(withdrawals.data?.next_cursor)}
          hasPrevious={withdrawalPager.hasPrevious}
          items={withdrawals.data?.items}
          loading={withdrawals.isPending}
          locale={locale}
          onNext={() => withdrawalPager.next(withdrawals.data?.next_cursor)}
          onPrevious={withdrawalPager.previous}
          onReset={withdrawalPager.reset}
          page={withdrawalPager.page}
        />
      )}
      {activeTab === "erc20-transfers" && (
        <AddressTokenTransfers
          address={address}
          busy={erc20Transfers.isFetching}
          error={erc20Transfers.error}
          hasNext={Boolean(erc20Transfers.data?.next_cursor)}
          items={erc20Transfers.data?.items}
          loading={erc20Transfers.isPending}
          locale={locale}
          onNext={() => erc20Pager.next(erc20Transfers.data?.next_cursor)}
          onPrevious={erc20Pager.previous}
          onReset={erc20Pager.reset}
          page={erc20Pager.page}
          hasPrevious={erc20Pager.hasPrevious}
          title={t("addressTab.erc20Transfers")}
        />
      )}
      {activeTab === "nft-transfers" && (
        <AddressTokenTransfers
          address={address}
          busy={nftTransfers.isFetching}
          error={nftTransfers.error}
          hasNext={Boolean(nftTransfers.data?.next_cursor)}
          items={nftTransfers.data?.items}
          loading={nftTransfers.isPending}
          locale={locale}
          onNext={() => nftTransferPager.next(nftTransfers.data?.next_cursor)}
          onPrevious={nftTransferPager.previous}
          onReset={nftTransferPager.reset}
          page={nftTransferPager.page}
          hasPrevious={nftTransferPager.hasPrevious}
          title={t("addressTab.nftTransfers")}
        />
      )}
      {activeTab === "assets" && (
        <div className="address-assets">
          <AddressERC20Balances
            balances={erc20Balances.data?.items}
            busy={erc20Balances.isFetching}
            coverageEnd={erc20Balances.data?.meta.coverage_end}
            error={erc20Balances.error}
            hasNext={Boolean(erc20Balances.data?.next_cursor)}
            loading={erc20Balances.isPending}
            locale={locale}
            onNext={() => erc20BalancePager.next(erc20Balances.data?.next_cursor)}
            onPrevious={erc20BalancePager.previous}
            onReset={erc20BalancePager.reset}
            page={erc20BalancePager.page}
            hasPrevious={erc20BalancePager.hasPrevious}
          />
          <AddressNFTBalances
            balances={nfts.data?.items}
            busy={nfts.isFetching}
            coverageEnd={nfts.data?.meta.coverage_end}
            error={nfts.error}
            hasNext={Boolean(nfts.data?.next_cursor)}
            loading={nfts.isPending}
            locale={locale}
            onNext={() => nftPager.next(nfts.data?.next_cursor)}
            onPrevious={nftPager.previous}
            onReset={nftPager.reset}
            page={nftPager.page}
            hasPrevious={nftPager.hasPrevious}
          />
        </div>
      )}
      {activeTab === "user-operations" && (
        <section className="panel transaction-tab-panel" aria-labelledby="address-user-operations-title">
          <h2 id="address-user-operations-title">{t("addressTab.userOperations")}</h2>
          <QueryNotice loading={userOperations.isPending} error={userOperations.error} onReset={userOperationPager.reset} />
          {userOperations.data?.items.length === 0 ? (
            <p className="empty-result">{t("state.noUserOperations")}</p>
          ) : null}
          {userOperations.data && userOperations.data.items.length > 0 ? (
            <UserOperationTable items={userOperations.data.items} />
          ) : null}
          {userOperations.data ? (
            <CursorPagination
              busy={userOperations.isFetching}
              hasNext={Boolean(userOperations.data.next_cursor)}
              hasPrevious={userOperationPager.hasPrevious}
              label={t("pagination.userOperations")}
              onNext={() => userOperationPager.next(userOperations.data?.next_cursor)}
              onPrevious={userOperationPager.previous}
              page={userOperationPager.page}
            />
          ) : null}
        </section>
      )}
      {activeTab === "contract" && contractAvailable ? <ContractPage address={address} /> : null}
      {activeTab === "delegation" && delegationAvailable ? (
        <DelegatedAccountPanel authority={displayAddress} currentlyDelegated={currentlyDelegated} />
      ) : null}
    </Page>
  );
}

function AddressHeader({ address, qrPayload }: { address: string; qrPayload?: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const openButtonRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    dialogRef.current?.focus();
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("keydown", closeOnEscape);
      openButtonRef.current?.focus();
    };
  }, [open]);

  return (
    <>
      <span className="address-header-actions">
        <AddressIdentity address={address} compact={false} copy link={false} />
        {qrPayload ? (
          <button
            aria-label={t("actions.showQRCode")}
            aria-haspopup="dialog"
            className="address-qr-button"
            onClick={() => setOpen(true)}
            ref={openButtonRef}
            type="button"
          >
            <span aria-hidden="true">▦</span>
          </button>
        ) : null}
      </span>
      {open && qrPayload ? (
        <div
          className="dialog-backdrop"
          onMouseDown={(event) => {
            if (event.currentTarget === event.target) setOpen(false);
          }}
          role="presentation"
        >
          <div
            aria-labelledby="address-qr-title"
            aria-modal="true"
            className="qr-dialog"
            onKeyDown={(event) => {
              if (event.key !== "Tab") return;
              const focusable = [...event.currentTarget.querySelectorAll<HTMLElement>(
                "button:not([disabled]), a[href], [tabindex]:not([tabindex='-1'])",
              )];
              if (focusable.length === 0) {
                event.preventDefault();
                event.currentTarget.focus();
                return;
              }
              const first = focusable[0]!;
              const last = focusable.at(-1)!;
              if (event.shiftKey && document.activeElement === first) {
                event.preventDefault();
                last.focus();
              } else if (!event.shiftKey && document.activeElement === last) {
                event.preventDefault();
                first.focus();
              } else if (document.activeElement === event.currentTarget) {
                event.preventDefault();
                (event.shiftKey ? last : first).focus();
              }
            }}
            ref={dialogRef}
            role="dialog"
            tabIndex={-1}
          >
            <div className="qr-dialog-heading">
              <h2 id="address-qr-title">{t("detail.addressQRCode")}</h2>
              <button
                aria-label={t("common.close")}
                className="dialog-close"
                onClick={() => setOpen(false)}
                type="button"
              >
                ×
              </button>
            </div>
            <div className="qr-code-surface">
              <QRCodeSVG
                bgColor="#ffffff"
                fgColor="#111111"
                includeMargin
                level="M"
                size={224}
                title={t("detail.addressQRCode")}
                value={qrPayload}
              />
            </div>
            <CopyableField value={qrPayload}><code>{qrPayload}</code></CopyableField>
          </div>
        </div>
      ) : null}
    </>
  );
}

function AddressOriginDetails({ origin }: { origin?: AddressSummary["origin"] }) {
  const { t } = useTranslation();
  if (!origin) {
    return <Detail label={t("detail.origin")} value={t("state.originUnavailable")} wide />;
  }
  const blockOrigin = origin.kind === "withdrawal" || origin.kind === "block_fee_recipient";
  const sourceLabel = origin.kind === "contract_creation"
    ? t("detail.contractCreator")
    : origin.kind === "withdrawal"
      ? t("detail.blockWithdrawal")
      : origin.kind === "block_fee_recipient"
        ? t("detail.blockFeeRecipient")
        : t("detail.fundedBy");
  const transactionLabel = origin.kind === "contract_creation"
    ? t("detail.creationTransaction")
    : t("detail.fundingTransaction");
  if (origin.state === "genesis") {
    return (
      <>
        <Detail label={sourceLabel} value={t("state.originGenesis")} />
        <Detail label={transactionLabel} value={t("state.originGenesis")} />
      </>
    );
  }
  if (origin.state === "unavailable") {
    return <Detail label={sourceLabel} value={t("state.originUnavailable")} wide />;
  }
  if (origin.state === "not_found") {
    return <Detail label={sourceLabel} value="—" />;
  }
  if (blockOrigin) {
    return (
      <>
        <Detail
          label={sourceLabel}
          mono
          value={origin.block_hash ? (
            <Link to="/blocks/$blockID" params={{ blockID: origin.block_hash }} search={{ tab: "overview" }}>
              {origin.block_hash}
            </Link>
          ) : undefined}
        />
        {origin.kind === "withdrawal" ? (
          <Detail label={t("detail.withdrawalIndex")} value={origin.withdrawal_index} mono />
        ) : null}
      </>
    );
  }
  return (
    <>
      <Detail
        label={sourceLabel}
        value={origin.source_address ? (
          <AddressIdentity address={origin.source_address} activity compact={false} copy />
        ) : undefined}
      />
      <Detail
        label={transactionLabel}
        mono
        value={origin.transaction_hash ? (
          <CopyableField value={origin.transaction_hash}>
            <Link
              params={{ hash: origin.transaction_hash }}
              search={{ tab: "overview" }}
              to="/tx/$hash"
            >
              {origin.transaction_hash}
            </Link>
          </CopyableField>
        ) : undefined}
      />
    </>
  );
}

function isAddressTab(tab: string): tab is AddressTab {
  return [
    "transactions",
    "internal-transactions",
    "withdrawals",
    "erc20-transfers",
    "nft-transfers",
    "assets",
    "user-operations",
    "delegation",
  ].includes(tab);
}

interface AddressActivityProps {
  address: string;
  busy: boolean;
  error: unknown;
  hasNext: boolean;
  hasPrevious: boolean;
  loading: boolean;
  locale: string;
  onNext: () => void;
  onPrevious: () => void;
  onReset: () => void;
  page: number;
}

function AddressTransactions({
  address,
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  nativeDecimals,
  nativeSymbol,
  onNext,
  onPrevious,
  onReset,
  page,
}: AddressActivityProps & {
  items?: TransactionSummary[];
  nativeDecimals: number;
  nativeSymbol: string;
}) {
  const { t } = useTranslation();
  return (
    <AddressActivitySection
      busy={busy}
      error={error}
      hasNext={hasNext}
      hasPrevious={hasPrevious}
      loading={loading}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={t("addressTab.transactions")}
      empty={items?.length === 0}
    >
      {items && items.length > 0 ? (
        <AddressActivityTable
          label={t("addressTab.transactions")}
          finality
          method
          nativeAmountLabel="value"
          nativeSymbol={nativeSymbol}
          status
        >
          {items.map((transaction) => {
            const destination = transaction.to ?? transaction.contract_address;
            return (
              <tr key={`${transaction.block_hash}:${transaction.hash}`}>
                <ActivityIdentity
                  blockNumber={transaction.block_number}
                  hash={transaction.hash}
                  timestamp={transaction.block_timestamp}
                  locale={locale}
                  method={{
                    value: transaction.method,
                    signature: transaction.method_signature,
                  }}
                />
                <td><TransactionStatusBadge showFinality={false} transaction={transaction} /></td>
                <AddressCell address={transaction.from} currentAddress={address} />
                <DirectionCell
                  direction={addressDirection(address, transaction.from, destination)}
                />
                <AddressCell
                  address={destination}
                  created={!transaction.to && Boolean(destination)}
                  currentAddress={address}
                />
                <td><code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code></td>
                <td><FinalityBadge finality={transaction.finality} /></td>
              </tr>
            );
          })}
        </AddressActivityTable>
      ) : null}
    </AddressActivitySection>
  );
}

function AddressInternalTransactions({
  address,
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  nativeDecimals,
  nativeSymbol,
  onNext,
  onPrevious,
  onReset,
  page,
}: AddressActivityProps & {
  items?: AddressInternalTransaction[];
  nativeDecimals: number;
  nativeSymbol: string;
}) {
  const { t } = useTranslation();
  return (
    <AddressActivitySection
      busy={busy}
      error={error}
      hasNext={hasNext}
      hasPrevious={hasPrevious}
      loading={loading}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={t("addressTab.internalTransactions")}
      empty={items?.length === 0}
    >
      {items && items.length > 0 ? (
        <AddressActivityTable
          label={t("addressTab.internalTransactions")}
          action
          nativeSymbol={nativeSymbol}
        >
          {items.map((transaction) => {
            const destination = transaction.created_address ?? transaction.to;
            return (
              <tr key={`${transaction.block_hash}:${transaction.transaction_hash}:${transaction.path.join(".")}`}>
                <ActivityIdentity
                  blockNumber={transaction.block_number}
                  hash={transaction.transaction_hash}
                  timestamp={transaction.block_timestamp}
                  locale={locale}
                />
                <td>
                  <span className="table-primary">
                    {transaction.call_type}
                    {transaction.reverted ? <small>{t("activity.reverted")}</small> : null}
                  </span>
                </td>
                <AddressCell address={transaction.from} currentAddress={address} />
                <DirectionCell
                  direction={addressDirection(address, transaction.from, destination)}
                />
                <AddressCell
                  address={destination}
                  created={Boolean(transaction.created_address)}
                  currentAddress={address}
                />
                <td><code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code></td>
              </tr>
            );
          })}
        </AddressActivityTable>
      ) : null}
    </AddressActivitySection>
  );
}

function AddressWithdrawals({
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  onNext,
  onPrevious,
  onReset,
  page,
}: Omit<AddressActivityProps, "address"> & { items?: AddressWithdrawal[] }) {
  const { t } = useTranslation();
  const title = t("addressTab.withdrawals");
  return (
    <section className="detail-section address-activity" aria-label={title}>
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {items?.length === 0 ? (
        <p className="empty-result" role="status">{t("state.noAddressWithdrawals")}</p>
      ) : null}
      {items && items.length > 0 ? (
        <div className="table-scroll" tabIndex={0} aria-label={title}>
          <table className="address-activity-table">
            <caption className="sr-only">{title}</caption>
            <thead>
              <tr>
                <th>{t("detail.withdrawalIndex")}</th>
                <th>{t("detail.validatorIndex")}</th>
                <th>{t("table.block")}</th>
                <th>{t("table.age")}</th>
                <th>{t("detail.withdrawalAmount")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((withdrawal) => (
                <tr key={`${withdrawal.block_hash}:${withdrawal.index}`}>
                  <td><code>{formatInteger(withdrawal.index, locale)}</code></td>
                  <td><code>{formatInteger(withdrawal.validator_index, locale)}</code></td>
                  <td>
                    <Link to="/blocks/$blockID" params={{ blockID: withdrawal.block_hash }}>
                      {formatInteger(withdrawal.block_number, locale)}
                    </Link>
                  </td>
                  <td>{formatRelativeTimestamp(withdrawal.block_timestamp, locale)}</td>
                  <td><code>{formatEtherFromGwei(withdrawal.amount, locale)} Ether</code></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {!loading && !error ? (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.addressWithdrawals")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      ) : null}
    </section>
  );
}

function AddressTokenTransfers({
  address,
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  onNext,
  onPrevious,
  onReset,
  page,
  title,
}: AddressActivityProps & { items?: AddressTokenTransfer[]; title: string }) {
  const { t } = useTranslation();
  return (
    <AddressActivitySection
      busy={busy}
      error={error}
      hasNext={hasNext}
      hasPrevious={hasPrevious}
      loading={loading}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={title}
      empty={items?.length === 0}
    >
      {items && items.length > 0 ? (
        <AddressActivityTable label={title} token>
          {items.map((transfer) => (
            <tr key={`${transfer.block_hash}:${transfer.transaction_hash}:${transfer.log_index}:${transfer.sub_index}`}>
              <ActivityIdentity
                blockNumber={transfer.block_number}
                hash={transfer.transaction_hash}
                timestamp={transfer.block_timestamp}
                locale={locale}
              />
              <td>
                <span className="table-primary">
                  <CopyableField value={transfer.token_address}>
                    {sameAddress(transfer.token_address, address) ? (
                      <code>{shorten(transfer.token_address)}</code>
                    ) : (
                      <Link to="/token/$address" params={{ address: transfer.token_address }}>
                        <code>{shorten(transfer.token_address)}</code>
                      </Link>
                    )}
                  </CopyableField>
                  <small>{tokenStandardLabel(transfer.standard, t)}</small>
                </span>
              </td>
              <AddressCell address={transfer.from} currentAddress={address} />
              <DirectionCell direction={addressDirection(address, transfer.from, transfer.to)} />
              <AddressCell address={transfer.to} currentAddress={address} />
              <td>
                <span className="table-primary">
                  {transfer.amount !== undefined ? (
                    <code>{formatTokenEventAmount(transfer, locale)}</code>
                  ) : transfer.token_id !== undefined && isNFTStandard(transfer.standard) ? (
                    <NFTTokenIDLink address={transfer.token_address} prefix tokenID={transfer.token_id} />
                  ) : (
                    <code>{transfer.token_id !== undefined ? `#${transfer.token_id}` : "—"}</code>
                  )}
                  {transfer.amount !== undefined && transfer.token_id !== undefined && isNFTStandard(transfer.standard)
                    ? <small><NFTTokenIDLink address={transfer.token_address} prefix tokenID={transfer.token_id} /></small>
                    : null}
                </span>
              </td>
            </tr>
          ))}
        </AddressActivityTable>
      ) : null}
    </AddressActivitySection>
  );
}

function AddressActivitySection({
  busy,
  children,
  empty,
  error,
  hasNext,
  hasPrevious,
  loading,
  onNext,
  onPrevious,
  onReset,
  page,
  title,
}: Omit<AddressActivityProps, "address" | "locale"> & {
  children: React.ReactNode;
  empty: boolean;
  title: string;
}) {
  const { t } = useTranslation();
  return (
    <section className="detail-section address-activity" aria-label={title}>
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {empty ? <p className="empty-result" role="status">{t("state.noAddressActivity")}</p> : null}
      {children}
      {!loading && !error ? (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.addressActivity")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      ) : null}
    </section>
  );
}

function AddressActivityTable({
  action,
  children,
  finality,
  label,
  method,
  nativeAmountLabel,
  nativeSymbol,
  status,
  token,
}: {
  action?: boolean;
  children: React.ReactNode;
  finality?: boolean;
  label: string;
  method?: boolean;
  nativeAmountLabel?: "amount" | "value";
  nativeSymbol?: string;
  status?: boolean;
  token?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="table-scroll" tabIndex={0} aria-label={label}>
      <table className="address-activity-table">
        <caption className="sr-only">{label}</caption>
        <thead>
          <tr>
            <th>{t("table.hash")}</th>
            {method ? <th>{t("table.method")}</th> : null}
            <th>{t("table.block")}</th>
            <th>{t("table.age")}</th>
            {status ? <th>{t("table.status")}</th> : null}
            {action ? <th>{t("table.action")}</th> : null}
            {token ? <th>{t("table.token")}</th> : null}
            <th>{t("table.from")}</th>
            <th>{t("table.direction")}</th>
            <th>{t("table.to")}</th>
            <th>
              {token
                ? t("detail.amountOrTokenID")
                : nativeAmountLabel === "value"
                  ? t("table.value", { symbol: nativeSymbol ?? "" })
                  : t("detail.nativeAmount", { symbol: nativeSymbol ?? "" })}
            </th>
            {finality ? <th>{t("table.finality")}</th> : null}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

function ActivityIdentity({
  blockNumber,
  hash,
  locale,
  method,
  timestamp,
}: {
  blockNumber?: string;
  hash: string;
  locale: string;
  method?: { value?: string; signature?: string };
  timestamp?: string;
}) {
  return (
    <>
      <td>
        <Link to="/tx/$hash" params={{ hash }} search={{ tab: "overview" }}>
          <code>{shorten(hash)}</code>
        </Link>
      </td>
      {method ? <TransactionMethodCell method={method.value} signature={method.signature} /> : null}
      <td>
        {blockNumber ? (
          <Link to="/blocks/$blockID" params={{ blockID: blockNumber }}>
            {formatInteger(blockNumber, locale)}
          </Link>
        ) : "—"}
      </td>
      <td>{timestamp ? formatRelativeTimestamp(timestamp, locale) : "—"}</td>
    </>
  );
}

export function TransactionMethodCell({
  method,
  signature,
}: {
  method?: string;
  signature?: string;
}) {
  const accessibleName = signature ?? method;
  return (
    <td className="transaction-method-cell">
      <code
        aria-label={accessibleName}
        className="transaction-method"
        title={accessibleName}
      >{method ?? "—"}</code>
    </td>
  );
}

function AddressCell({
  address,
  created,
  currentAddress,
}: {
  address?: string;
  created?: boolean;
  currentAddress: string;
}) {
  const { t } = useTranslation();
  if (!address) return <td>—</td>;
  return (
    <td>
      <span className="table-primary">
        <AddressIdentity address={address} activity copy link={!sameAddress(address, currentAddress)} />
        {created ? <small>{t("activity.created")}</small> : null}
      </span>
    </td>
  );
}

function sameAddress(left: string, right: string): boolean {
  return left.toLowerCase() === right.toLowerCase();
}

function DirectionCell({ direction }: { direction: "in" | "out" | "self" }) {
  const { t } = useTranslation();
  return (
    <td>
      <span className={`address-direction ${direction}`}>
        {t(`activity.direction.${direction}`)}
      </span>
    </td>
  );
}

function addressDirection(
  address: string,
  from?: string,
  destination?: string,
): "in" | "out" | "self" {
  const subject = address.toLowerCase();
  const outgoing = from?.toLowerCase() === subject;
  const incoming = destination?.toLowerCase() === subject;
  if (outgoing && incoming) return "self";
  return outgoing ? "out" : "in";
}

function AddressERC20Balances({
  balances,
  busy,
  coverageEnd,
  error,
  hasNext,
  hasPrevious,
  loading,
  locale,
  onNext,
  onPrevious,
  onReset,
  page,
}: {
  balances?: ERC20Balance[];
  busy: boolean;
  coverageEnd?: string;
  error: unknown;
  hasNext: boolean;
  hasPrevious: boolean;
  loading: boolean;
  locale: string;
  onNext: () => void;
  onPrevious: () => void;
  onReset: () => void;
  page: number;
}) {
  const { t } = useTranslation();
  return (
    <section className="detail-section" aria-labelledby="erc20-balances-title">
      <h2 id="erc20-balances-title">{t("detail.erc20Balances")}</h2>
      {coverageEnd ? (
        <p className="context-note" role="note">
          {t("detail.assetSnapshot", { block: formatInteger(coverageEnd, locale) })}
        </p>
      ) : null}
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {balances && balances.length === 0 ? (
        <p className="empty-result" role="status">{t("state.noERC20Balances")}</p>
      ) : null}
      {balances && balances.length > 0 ? (
        <div className="table-scroll" tabIndex={0} aria-label={t("detail.erc20Balances")}>
          <table>
            <caption className="sr-only">{t("detail.erc20BalanceDescription")}</caption>
            <thead>
              <tr>
                <th>{t("table.token")}</th>
                <th>{t("detail.balance")}</th>
                <th>{t("table.confidence")}</th>
              </tr>
            </thead>
            <tbody>
              {balances.map((balance) => {
                const amount = balance.decimals === undefined
                  ? formatInteger(balance.balance, locale)
                  : formatNativeAmount(balance.balance, locale, balance.decimals);
                const tokenLabel = balance.symbol ?? balance.token_address;
                return (
                  <tr key={balance.token_address}>
                    <td>
                      <span className="table-primary">
                        <Link to="/token/$address" params={{ address: balance.token_address }}>
                          {balance.name ?? tokenLabel}
                        </Link>
                        <small><code>{balance.symbol ?? shorten(balance.token_address)}</code></small>
                      </span>
                    </td>
                    <td>
                      <code>{amount}{balance.symbol ? ` ${balance.symbol}` : ""}</code>
                    </td>
                    <td><span className="result-kind">{confidenceLabel(balance.confidence, t)}</span></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}
      {balances ? (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.erc20Balances")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      ) : null}
    </section>
  );
}

function AddressNFTBalances({
  balances,
  busy,
  coverageEnd,
  error,
  hasNext,
  hasPrevious,
  loading,
  locale,
  onNext,
  onPrevious,
  onReset,
  page,
}: {
  balances?: NFTBalance[];
  busy: boolean;
  coverageEnd?: string;
  error: unknown;
  hasNext: boolean;
  hasPrevious: boolean;
  loading: boolean;
  locale: string;
  onNext: () => void;
  onPrevious: () => void;
  onReset: () => void;
  page: number;
}) {
  const { t } = useTranslation();
  return (
    <section className="detail-section" aria-labelledby="nft-balances-title">
      <h2 id="nft-balances-title">{t("detail.nftBalances")}</h2>
      {coverageEnd && (
        <p className="context-note" role="note">
          {t("detail.nftSnapshot", { block: formatInteger(coverageEnd, locale) })}
        </p>
      )}
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {balances && balances.length === 0 && (
        <p className="empty-result" role="status">{t("state.noNFTBalances")}</p>
      )}
      {balances && balances.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("detail.nftBalances")}>
          <table>
            <caption className="sr-only">{t("detail.nftBalanceDescription")}</caption>
            <thead>
              <tr>
                <th>{t("table.token")}</th>
                <th>{t("detail.tokenID")}</th>
                <th>{t("detail.balance")}</th>
                <th>{t("table.confidence")}</th>
              </tr>
            </thead>
            <tbody>
              {balances.map((balance) => (
                <tr key={`${balance.token_address}:${balance.token_id}`}>
                  <td>
                    <Link to="/token/$address" params={{ address: balance.token_address }}>
                      <code>{shorten(balance.token_address)}</code>
                    </Link>
                  </td>
                  <td><NFTTokenIDLink address={balance.token_address} tokenID={balance.token_id} /></td>
                  <td><code>{balance.balance}</code></td>
                  <td><span className="result-kind">{confidenceLabel(balance.confidence, t)}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {balances && (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.nfts")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      )}
    </section>
  );
}
