import i18n from "@/i18n";
import { nameResources } from "@/i18n/resources/names";
import type { NameDetail } from "@/api/heyapi/types.gen";

i18n.addResourceBundle("en", "translation", nameResources.en, true, true);
i18n.addResourceBundle("zh", "translation", nameResources.zh, true, true);

export function nameDeadlines(detail: NameDetail | undefined): number[] {
  if (!detail) return [];
  return [
    Date.parse(detail.profile_observation?.valid_until ?? ""),
    Date.parse(detail.resolution_observation?.valid_until ?? ""),
    detail.registration_state === "registered" && detail.expiration_time.state === "value"
      ? Number(detail.expiration_time.value) * 1000
      : NaN,
  ].filter(Number.isFinite);
}
export function freshNameDetail(detail: NameDetail, now: number, invalid = false): NameDetail {
  const pending = { state: "pending" as const };
  const profileCurrent =
    !invalid && Date.parse(detail.profile_observation?.valid_until ?? "") > now;
  const resolutionCurrent =
    !invalid && Date.parse(detail.resolution_observation?.valid_until ?? "") > now;
  const expired =
    detail.registration_state === "registered" &&
    detail.expiration_time.state === "value" &&
    Number(detail.expiration_time.value) * 1000 <= now;
  return {
    ...detail,
    registration_state: profileCurrent
      ? expired
        ? "expired"
        : detail.registration_state
      : "pending",
    registrant: profileCurrent && !expired ? detail.registrant : pending,
    controller: profileCurrent && !expired ? detail.controller : pending,
    expiration_time: profileCurrent ? detail.expiration_time : pending,
    resolved_address: resolutionCurrent ? detail.resolved_address : pending,
    profile_observation: profileCurrent ? detail.profile_observation : undefined,
    resolution_observation: resolutionCurrent ? detail.resolution_observation : undefined,
  };
}
export function expirationText(value: string | undefined, locale: string): string {
  if (!value) return "";
  const date = new Date(Number(value) * 1000);
  return Number.isFinite(date.getTime())
    ? new Intl.DateTimeFormat(locale, { dateStyle: "long", timeStyle: "long" }).format(date)
    : `${value} (Unix seconds)`;
}
