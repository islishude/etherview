import {
  createContext,
  type PropsWithChildren,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";

export type Theme = "light" | "dark";

interface ThemeContextValue {
  theme: Theme;
  followsSystem: boolean;
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
  followSystem: () => void;
}

const ThemeContext = createContext<ThemeContextValue | undefined>(undefined);

function readStoredTheme(): Theme | undefined {
  if (typeof window === "undefined") return undefined;

  let stored: string | null = null;
  try {
    stored = window.localStorage?.getItem("etherview.theme") ?? null;
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
  if (stored === "light" || stored === "dark") return stored;
  return undefined;
}

function readSystemTheme(): Theme {
  if (typeof window === "undefined") return "light";
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function subscribeSystemTheme(onChange: () => void) {
  const media = window.matchMedia?.("(prefers-color-scheme: dark)");
  media?.addEventListener("change", onChange);
  return () => media?.removeEventListener("change", onChange);
}

export function resolveInitialTheme(): Theme {
  return readStoredTheme() ?? readSystemTheme();
}

export function ThemeProvider({ children }: PropsWithChildren) {
  const [preference, setPreference] = useState(readStoredTheme);
  const systemTheme = useSyncExternalStore<Theme>(
    subscribeSystemTheme,
    readSystemTheme,
    () => "light",
  );
  const theme = preference ?? systemTheme;

  useLayoutEffect(() => {
    // Passive consumers such as charts must read tokens from the current theme.
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  useEffect(() => {
    try {
      if (preference) {
        window.localStorage?.setItem("etherview.theme", preference);
      } else {
        window.localStorage?.removeItem("etherview.theme");
      }
    } catch {
      // The applied document theme remains functional without persistence.
    }
  }, [preference]);

  const value = useMemo<ThemeContextValue>(
    () => ({
      theme,
      followsSystem: preference === undefined,
      setTheme: setPreference,
      toggleTheme: () =>
        setPreference((current) => ((current ?? systemTheme) === "light" ? "dark" : "light")),
      followSystem: () => setPreference(undefined),
    }),
    [theme, preference, systemTheme],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext);
  if (!context) throw new Error("useTheme must be used inside ThemeProvider");
  return context;
}
