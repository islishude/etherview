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
import { useQueryClient } from "@tanstack/react-query";
import { usePublicConfig } from "@/api/publicConfig";
import type * as schemas from "@/api/heyapi/types.gen";
import { subscribeNames, registerNamesRefresh } from "./nameEvents";

type PrimaryName = schemas.PrimaryName;
type AddressNamesContextValue = {
  lookup(address: string): PrimaryName | undefined;
  register(address: string): () => void;
};
const AddressNamesContext = createContext<AddressNamesContextValue>({
  lookup: () => undefined,
  register: () => () => {},
});

export function AddressNamesProvider({ children }: { children: ReactNode }) {
  const enabled = usePublicConfig().data?.features.ens === true;
  const client = useQueryClient();
  const [names, setNames] = useState<Map<string, PrimaryName>>(new Map());
  const [revision, setRevision] = useState(0);
  const visible = useRef(new Map<string, number>());
  const attempted = useRef(new Set<string>());
  const generation = useRef(0);
  const request = useRef<AbortController | undefined>(undefined);
  const register = useCallback((address: string) => {
    const key = normalizeAddress(address);
    if (!key) return () => {};
    visible.current.set(key, (visible.current.get(key) ?? 0) + 1);
    if (visible.current.get(key) === 1) setRevision((value) => value + 1);
    return () => {
      const count = (visible.current.get(key) ?? 1) - 1;
      if (count > 0) visible.current.set(key, count);
      else {
        visible.current.delete(key);
        attempted.current.delete(key);
      }
    };
  }, []);
  const load = useCallback(
    async (all: boolean) => {
      if (!enabled) return;
      const addresses = [...visible.current.keys()].filter(
        (address) => all || !attempted.current.has(address),
      );
      if (!addresses.length) return;
      const epoch = ++generation.current;
      request.current?.abort();
      const controller = new AbortController();
      request.current = controller;
      const current = () => !controller.signal.aborted && epoch === generation.current;
      try {
        const { loadVisibleNames } = await import("./loadVisibleNames");
        // New visible addresses do not renew the observations of existing rows.
        const result = await loadVisibleNames(addresses, undefined, controller.signal, () => {
          if (current())
            setNames(
              (prior) => new Map([...prior].filter(([address]) => !addresses.includes(address))),
            );
        });
        if (current()) {
          setNames((prior) => {
            const next = new Map([...prior].filter(([address]) => visible.current.has(address)));
            for (const address of addresses) next.delete(address);
            for (const [address, name] of result.names) {
              if (visible.current.has(address)) next.set(address, name);
            }
            return next;
          });
        }
      } catch {
        // Failed manual reads never extend the validity of retained observations.
      } finally {
        if (current())
          for (const address of addresses) {
            if (visible.current.has(address)) attempted.current.add(address);
          }
      }
    },
    [enabled],
  );
  useEffect(() => registerNamesRefresh(client, () => load(true)), [client, load]);
  useEffect(
    () =>
      subscribeNames(client, (change) => {
        if (change !== "invalidate") return;
        generation.current++;
        request.current?.abort();
        for (const address of visible.current.keys()) attempted.current.add(address);
        setNames(new Map());
      }),
    [client],
  );
  useEffect(() => {
    if (!enabled) {
      request.current?.abort();
      attempted.current.clear();
      setNames(new Map());
      return;
    }
    const timer = setTimeout(() => {
      void load(false);
    }, 0);
    return () => clearTimeout(timer);
  }, [enabled, revision, load]);
  useEffect(
    () => () => {
      request.current?.abort();
    },
    [],
  );
  useEffect(() => {
    if (names.size === 0) return;
    const expiry = Math.min(...[...names.values()].map((name) => Date.parse(name.valid_until)));
    const timer = setTimeout(
      () => {
        setNames(
          (current) =>
            new Map([...current].filter(([, name]) => Date.parse(name.valid_until) > Date.now())),
        );
      },
      Math.max(0, Math.min(expiry - Date.now(), 2_147_483_647)),
    );
    return () => clearTimeout(timer);
  }, [names]);
  const lookup = useCallback(
    (address: string) => {
      const name = names.get(normalizeAddress(address));
      return name && Date.parse(name.valid_until) > Date.now() ? name : undefined;
    },
    [names],
  );
  const value = useMemo(() => ({ lookup, register }), [lookup, register]);
  return <AddressNamesContext.Provider value={value}>{children}</AddressNamesContext.Provider>;
}
export function usePrimaryName(address: string): PrimaryName | undefined {
  const { register, lookup } = useContext(AddressNamesContext);
  useEffect(() => register(address), [address, register]);
  return lookup(address);
}
function normalizeAddress(address: string): string {
  return /^0x[0-9a-fA-F]{40}$/.test(address) ? address.toLowerCase() : "";
}
