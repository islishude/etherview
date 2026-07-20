export function shorten(value: string, head = 8, tail = 6): string {
  if (value.length <= head + tail + 1) return value;
  return `${value.slice(0, head)}…${value.slice(-tail)}`;
}

export function formatTimestamp(value: string, locale: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(date);
}

export function formatInteger(value?: string | number | bigint, locale?: string): string {
  if (value === undefined || value === null || value === "") return "—";
  try {
    return new Intl.NumberFormat(locale).format(BigInt(value));
  } catch {
    return String(value);
  }
}
