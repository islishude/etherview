import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { apiClient, ApiError, requireEnvelope } from "@/api/client";
import { usePublicConfig } from "@/api/hooks";
import type { components } from "@/api/schema.gen";

type PrimaryName = components["schemas"]["PrimaryName"];

type AddressNamesContextValue = {
  lookup(address: string): PrimaryName | undefined;
  register(address: string): void;
};

const AddressNamesContext = createContext<AddressNamesContextValue>({
  lookup: () => undefined,
  register: () => undefined,
});

export function AddressNamesProvider({ children }: { children: ReactNode }) {
  const publicConfig = usePublicConfig();
  const enabled = publicConfig.data?.features.ens === true;
  const [names, setNames] = useState<Map<string, PrimaryName>>(new Map());
  const [pending, setPending] = useState<string[]>([]);
  const [snapshot, setSnapshot] = useState<string>();
  const [loading, setLoading] = useState(false);
  const known = useRef(new Set<string>());

  const register = useCallback((address: string) => {
    if (!enabled) return;
    const key = normalizeAddress(address);
    if (!key || known.current.has(key)) return;
    known.current.add(key);
    setPending((current) => current.includes(key) ? current : [...current, key]);
  }, [enabled]);

  useEffect(() => {
    if (!enabled || loading || pending.length === 0) return;
    const chunk = pending.slice(0, 100);
    setPending((current) => current.slice(chunk.length));
    setLoading(true);
    void (async () => {
      try {
        const response = requireEnvelope(
          await apiClient.GET("/address-names", {
            params: { query: { addresses: chunk.join(","), snapshot } },
          }),
        );
        setSnapshot(response.data.snapshot);
        setNames((current) => {
          const next = new Map(current);
          for (const item of response.data.items) {
            const key = normalizeAddress(item.address);
            if (key && item.state === "resolved" && item.primary_name) {
              next.set(key, item.primary_name);
            }
          }
          return next;
        });
      } catch (error) {
        if (error instanceof ApiError && error.code === "invalid_cursor") {
          known.current.clear();
          setNames(new Map());
          setSnapshot(undefined);
          setPending(chunk);
        }
      } finally {
        setLoading(false);
      }
    })();
  }, [enabled, loading, pending, snapshot]);

  useEffect(() => {
    if (enabled) return;
    known.current.clear();
    setNames(new Map());
    setPending([]);
    setSnapshot(undefined);
  }, [enabled]);

  const lookup = useCallback((address: string) => names.get(normalizeAddress(address)), [names]);
  const value = useMemo(() => ({ lookup, register }), [lookup, register]);
  return <AddressNamesContext.Provider value={value}>{children}</AddressNamesContext.Provider>;
}

export function usePrimaryName(address: string): PrimaryName | undefined {
  const context = useContext(AddressNamesContext);
  useEffect(() => {
    context.register(address);
  }, [address, context]);
  return context.lookup(address);
}

function normalizeAddress(address: string): string {
  return /^0x[0-9a-fA-F]{40}$/.test(address) ? address.toLowerCase() : "";
}
