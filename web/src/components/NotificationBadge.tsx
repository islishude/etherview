import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useNotifications, usePrivateIdentity } from "@/api/privateAccount";

export function NotificationBadge() {
  const identity = usePrivateIdentity();
  const query = useNotifications();
  const { t } = useTranslation();
  if (!identity) return null;
  return (
    <Link to="/account" search={{ tab: "notifications" }}>
      {t("watchlist.notifications")} ({query.data?.unread_count ?? "—"})
    </Link>
  );
}
