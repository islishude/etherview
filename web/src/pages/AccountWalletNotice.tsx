import { useTranslation } from "react-i18next";
import { useAuth } from "@/auth/AuthProvider";
import { useWallet } from "@/wallet/WalletProvider";
import { chainsMatch } from "@/wallet/eip6963";
import { usePublicConfig } from "@/api/publicConfig";

export function AccountWalletNotice() {
  const { t } = useTranslation();
  const auth = useAuth();
  const wallet = useWallet();
  const config = usePublicConfig();
  const account = auth.session.user;
  const active = wallet.active;
  const correctChain = chainsMatch(active?.chainID, config.data?.chain_id);
  const matches =
    active && account && active.account.toLowerCase() === account.address.toLowerCase();
  const warning =
    active &&
    !auth.loading &&
    (!auth.session.authenticated && !correctChain
      ? t("auth.walletState.wrongChain", {
          expected: config.data?.chain_id ?? "—",
          actual: active.chainID,
        })
      : account && !matches
        ? t("adminUI.identityMismatch")
        : undefined);
  return warning ? (
    <p role="status" className="m-0 text-sm text-warning">
      {warning}
    </p>
  ) : null;
}
