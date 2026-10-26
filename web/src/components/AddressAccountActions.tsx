import { UIIcon } from "@/components/UIIcon";
import { SelectControl } from "@/components/SelectControl";
import { Bell, BellRing, Download, ChevronDown } from "lucide-react";
import { useTurnstile } from "./TurnstileChallenge";
import "./AddressAccountActions.css";
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
      <p className="address-actions-login">
        <Link to="/account">{t("watchlist.loginHint")}</Link>
      </p>
    );
  return <AddressActions key={`${identity}:${address}`} address={address} />;
}
function AddressActions({ address }: { address: string }) {
  const { t } = useTranslation();
  const query = useWatches();
  const action = useAccountAction();
  const challenge = useTurnstile("csv_export");
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
    if (!challenge.ready || action.pending) return;
    const token = challenge.consume({ deferRefresh: true });
    void action
      .run((csrf, signal) =>
        downloadAddressCSV(
          { address, kind, direction, from: `${from}:00Z`, to: `${to}:00Z` },
          csrf,
          signal,
          token,
        ),
      )
      .finally(challenge.refresh);
  }
  return (
    <section
      className="address-actions"
      aria-label={t("watchlist.actions")}
      aria-busy={action.pending}
    >
      <div className="address-actions-toolbar">
        <button
          className="address-action"
          aria-pressed={Boolean(watch)}
          aria-label={
            watch ? `${t("watchlist.watching")}: ${t("watchlist.remove")}` : t("watchlist.add")
          }
          title={t(watch ? "watchlist.remove" : "watchlist.add")}
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
          {watch ? <UIIcon icon={BellRing} /> : <UIIcon icon={Bell} />}
          {t(watch ? "watchlist.watching" : "watchlist.add")}
        </button>
        <button
          className="address-action"
          aria-expanded={open}
          aria-controls={open ? "address-csv-export" : undefined}
          onClick={() => setOpen(!open)}
        >
          <UIIcon icon={Download} />
          {t("watchlist.export")}
          <UIIcon icon={ChevronDown} className="address-action-chevron" />
        </button>
      </div>
      {open && (
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
      )}
      <AccountActionError code={action.error || (query.isError ? "activity_unavailable" : "")} />
    </section>
  );
}
