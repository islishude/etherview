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

export function formatNativeAmount(
  value?: string | number | bigint,
  locale?: string,
  nativeDecimals = 18,
): string {
  if (value === undefined || value === null || value === "") return "—";

  const decimals = Number.isInteger(nativeDecimals) && nativeDecimals >= 0
    ? nativeDecimals
    : 18;
  const maxDecimalDigits = Math.min(decimals, 18);

  try {
    const rawValue = BigInt(value);
    const sign = rawValue < 0n ? "-" : "";
    const absolute = rawValue < 0n ? -rawValue : rawValue;
    const scale = 10n ** BigInt(decimals);
    const integerPart = absolute / scale;
    const fractionalRaw = (absolute % scale).toString().padStart(decimals, "0");
    const truncatedFraction = fractionalRaw.slice(0, maxDecimalDigits);
    const trimmedFraction = truncatedFraction.replace(/0+$/, "");

    if (!trimmedFraction) {
      return `${sign}${new Intl.NumberFormat(locale).format(integerPart)}`;
    }

    return `${sign}${new Intl.NumberFormat(locale).format(integerPart)}.${trimmedFraction}`;
  } catch {
    return "—";
  }
}

export function formatGweiFromWei(value?: string | number | bigint, locale?: string): string {
  return formatNativeAmount(value, locale, 9);
}
