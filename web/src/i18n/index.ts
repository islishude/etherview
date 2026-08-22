import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { shellResources } from "./resources/shell";
import { chainResources } from "./resources/chain";
import { transactionsResources } from "./resources/transactions";
import { addressesResources } from "./resources/addresses";
import { tokensResources } from "./resources/tokens";
import { accountResources } from "./resources/account";
import { verificationResources } from "./resources/verification";

const resources = {
  en: {
    translation: {
      ...shellResources.en,
      ...chainResources.en,
      ...transactionsResources.en,
      ...addressesResources.en,
      ...tokensResources.en,
      ...accountResources.en,
      ...verificationResources.en,
    },
  },
  zh: {
    translation: {
      ...shellResources.zh,
      ...chainResources.zh,
      ...transactionsResources.zh,
      ...addressesResources.zh,
      ...tokensResources.zh,
      ...accountResources.zh,
      ...verificationResources.zh,
    },
  },
} as const;

function initialLanguage(): "en" | "zh" {
  if (typeof window === "undefined") return "en";
  const stored = readPreference("etherview.language");
  if (stored === "en" || stored === "zh") return stored;
  return window.navigator.language.toLowerCase().startsWith("zh") ? "zh" : "en";
}

void i18n.use(initReactI18next).init({
  resources,
  lng: initialLanguage(),
  fallbackLng: "en",
  interpolation: { escapeValue: false },
  returnNull: false,
});

i18n.on("languageChanged", (language) => {
  if (typeof document !== "undefined") {
    document.documentElement.lang = language.startsWith("zh") ? "zh-CN" : "en";
  }
  if (typeof window !== "undefined") {
    writePreference("etherview.language", language.startsWith("zh") ? "zh" : "en");
  }
});

function readPreference(key: string): string | null {
  try {
    return window.localStorage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

function writePreference(key: string, value: string): void {
  try {
    window.localStorage?.setItem(key, value);
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
}

export default i18n;
