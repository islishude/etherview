import { Message } from "@/components/Message";
import "@/i18n/resources/addressActions";
import { type SubmitEvent, type ReactNode, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { authErrorTranslationKey, useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity } from "@/api/privateAccount";
import { downloadAddressCSV, type AddressExportRequest } from "@/api/watchlist";
import { SIWELoginControl } from "@/components/SIWELoginControl";
import { QueryNotice } from "@/components/QueryNotice";
import { SelectControl } from "@/components/SelectControl";
import { useTurnstile } from "@/components/TurnstileChallenge";
import { AccountActionError, useAccountAction } from "@/components/AccountAction";
import { Page } from "./pages";
import "@/components/AddressAccountActions.css";

function AddressActionPage({ address, children }: { address: string; children: ReactNode }) {
  const { t } = useTranslation();
  const auth = useAuth();
  return (
    <Page
      title={t("watchlist.export")}
      description={<code className="address-action-address">{address}</code>}
    >
      <Link to="/address/$address" params={{ address }}>
        {t("watchlist.backToAddress")}
      </Link>
      {auth.loading ? (
        <QueryNotice loading error={null} />
      ) : !auth.enabled || !auth.session.authenticated ? (
        <section className="panel address-action-login">
          <p>{t(!auth.enabled ? "watchlist.signInUnavailable" : "watchlist.exportSignIn")}</p>
          {auth.enabled && <SIWELoginControl label={t("watchlist.signIn")} />}
          {auth.error && <Message type="error">{t(authErrorTranslationKey(auth.error))}</Message>}
        </section>
      ) : (
        children
      )}
    </Page>
  );
}

export function AddressExportPage({ address }: { address: string }) {
  const identity = usePrivateIdentity();
  return (
    <AddressActionPage address={address}>
      <ExportForm key={`${identity}:${address}`} address={address} />
    </AddressActionPage>
  );
}

function ExportForm({ address }: { address: string }) {
  const { t } = useTranslation();
  const action = useAccountAction();
  const challenge = useTurnstile("csv_export");
  const [downloaded, setDownloaded] = useState(false);
  const [kind, setKind] = useState<AddressExportRequest["kind"]>("all");
  const [direction, setDirection] = useState<AddressExportRequest["direction"]>("both");
  const [from, setFrom] = useState(() =>
    new Date(Date.now() - 7 * 86400000).toISOString().slice(0, 16),
  );
  const [to, setTo] = useState(() => new Date().toISOString().slice(0, 16));
  async function download(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!challenge.ready || action.pending) return;
    setDownloaded(false);
    const token = challenge.consume({ deferRefresh: true });
    try {
      const success = await action.run((csrf, signal) =>
        downloadAddressCSV(
          { address, kind, direction, from: `${from}:00Z`, to: `${to}:00Z` },
          csrf,
          signal,
          token,
        ),
      );
      if (success) setDownloaded(true);
    } finally {
      challenge.refresh();
    }
  }
  return (
    <section
      className="address-actions"
      aria-label={t("watchlist.export")}
      aria-busy={action.pending}
    >
      <form
        id="address-csv-export"
        className="profile-form address-export-form"
        onSubmit={download}
      >
        <p>{t("watchlist.exportHint")}</p>
        <label className="field-control">
          <span>{t("watchlist.kinds")}</span>
          <SelectControl
            value={kind}
            onChange={(e) => setKind(e.target.value as AddressExportRequest["kind"])}
          >
            <option value="all">{t("watchlist.all")}</option>
            <option value="transaction">{t("watchlist.transactions")}</option>
            <option value="erc20">ERC-20</option>
            <option value="nft">NFT (ERC-721 / ERC-1155)</option>
          </SelectControl>
        </label>
        <label className="field-control">
          <span>{t("watchlist.fromUTC")}</span>
          <input
            required
            type="datetime-local"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
        </label>
        <label className="field-control">
          <span>{t("watchlist.toUTC")}</span>
          <input
            required
            type="datetime-local"
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
        </label>
        <label className="field-control">
          <span>{t("watchlist.direction")}</span>
          <SelectControl
            value={direction}
            onChange={(e) => setDirection(e.target.value as AddressExportRequest["direction"])}
          >
            {["both", "in", "out"].map((d) => (
              <option key={d} value={d}>
                {t(`watchlist.${d}`)}
              </option>
            ))}
          </SelectControl>
        </label>
        {challenge.challenge}
        <button className="button primary" disabled={action.pending || !challenge.ready}>
          {t(action.pending ? "watchlist.loading" : "watchlist.download")}
        </button>
      </form>

      <AccountActionError code={action.error} />
      {downloaded && <Message type="success">{t("watchlist.downloaded")}</Message>}
    </section>
  );
}
