import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";

export function AuthGate({ detail, title }: { detail: string; title: string }) {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <section className="panel auth-gate" aria-labelledby="auth-gate-title">
        <h2 id="auth-gate-title">{title}</h2>
        <p>{detail}</p>
        <Button asChild variant="default">
          <Link className="button primary inline-button" to="/account">
            {t("auth.account.open")}
          </Link>
        </Button>
      </section>
    </Card>
  );
}

export function UnavailablePanel() {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <section className="panel auth-gate" aria-labelledby="auth-unavailable-title">
        <h2 id="auth-unavailable-title">{t("auth.unavailable.title")}</h2>
        <p>{t("auth.unavailable.description")}</p>
      </section>
    </Card>
  );
}
