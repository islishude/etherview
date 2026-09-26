import { type FormEvent, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity, useWatches } from "@/api/privateAccount";
import {
  deleteWatch,
  downloadAddressCSV,
  saveWatch,
  type AddressExportRequest,
} from "@/api/watchlist";
import { AccountActionError, useAccountAction } from "@/pages/WatchlistPages";

export function AddressAccountActions({ address }: { address: string }) {
  const identity = usePrivateIdentity();
  const auth = useAuth();
  const { t } = useTranslation();
  if (!auth.enabled) return null;
  if (!identity)
    return (
      <p>
        <Link to="/account">{t("watchlist.loginHint")}</Link>
      </p>
    );
  return <AddressActions key={`${identity}:${address}`} address={address} />;
}
function AddressActions({ address }: { address: string }) {
  const { t } = useTranslation();
  const query = useWatches();
  const action = useAccountAction();
  const watch = query.data?.find((item) => item.address.toLowerCase() === address.toLowerCase());
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<AddressExportRequest["kind"]>("transaction");
  const [direction, setDirection] = useState<AddressExportRequest["direction"]>("both");
  const [from, setFrom] = useState(() =>
    new Date(Date.now() - 7 * 86400000).toISOString().slice(0, 16),
  );
  const [to, setTo] = useState(() => new Date().toISOString().slice(0, 16));
  function download(event: FormEvent) {
    event.preventDefault();
    void action.run((csrf, signal) =>
      downloadAddressCSV(
        { address, kind, direction, from: `${from}:00Z`, to: `${to}:00Z` },
        csrf,
        signal,
      ),
    );
  }
  return (
    <section className="panel watchlist-panel" aria-label={t("watchlist.actions")}>
      <div>
        <button
          className="button secondary"
          disabled={action.pending || !query.data}
          onClick={() =>
            void action.run((csrf, signal) =>
              watch
                ? deleteWatch(watch.id, csrf, signal)
                : saveWatch(
                    {
                      address,
                      label: "",
                      kinds: ["transaction", "erc20", "erc721", "erc1155"],
                      direction: "both",
                      enabled: true,
                    },
                    csrf,
                    signal,
                  ),
            )
          }
        >
          {t(watch ? "watchlist.remove" : "watchlist.add")}
        </button>
        <button className="button secondary" aria-expanded={open} onClick={() => setOpen(!open)}>
          {t("watchlist.export")}
        </button>
      </div>
      {open && (
        <form className="profile-form" onSubmit={download}>
          <p>{t("watchlist.exportHint")}</p>
          <label>
            {t("watchlist.kinds")}
            <select
              value={kind}
              onChange={(e) => setKind(e.target.value as AddressExportRequest["kind"])}
            >
              <option value="transaction">{t("watchlist.transactions")}</option>
              <option value="erc20">ERC-20</option>
              <option value="nft">NFT (ERC-721 / ERC-1155)</option>
            </select>
          </label>
          <label>
            {t("watchlist.fromUTC")}
            <input
              required
              type="datetime-local"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            />
          </label>
          <label>
            {t("watchlist.toUTC")}
            <input
              required
              type="datetime-local"
              value={to}
              onChange={(e) => setTo(e.target.value)}
            />
          </label>
          <label>
            {t("watchlist.direction")}
            <select
              value={direction}
              onChange={(e) => setDirection(e.target.value as AddressExportRequest["direction"])}
            >
              {["both", "in", "out"].map((d) => (
                <option key={d} value={d}>
                  {t(`watchlist.${d}`)}
                </option>
              ))}
            </select>
          </label>
          <button className="button primary" disabled={action.pending}>
            {t(action.pending ? "watchlist.loading" : "watchlist.download")}
          </button>
        </form>
      )}
      <AccountActionError code={action.error || (query.isError ? "activity_unavailable" : "")} />
    </section>
  );
}
