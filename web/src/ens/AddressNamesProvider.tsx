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
import { subscribeNames } from "./nameEvents";

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
  const snapshot = useRef<string | undefined>(undefined);
  const generation = useRef(0);
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
        setRevision((value) => value + 1);
      }
    };
  }, []);
  useEffect(
    () =>
      subscribeNames(client, () => {
        generation.current++;
        snapshot.current = undefined;
        setNames(new Map());
        setRevision((value) => value + 1);
      }),
    [client],
  );
  useEffect(() => {
    const epoch = ++generation.current;
    const controller = new AbortController();
    if (!enabled) {
      snapshot.current = undefined;
      setNames(new Map());
      return;
    }
    const addresses = [...visible.current.keys()];
    const load = async () => {
      const current = () => !controller.signal.aborted && epoch === generation.current;
      const reset = () => {
        if (current()) {
          setNames(new Map());
          snapshot.current = undefined;
        }
      };
      try {
        const { loadVisibleNames } = await import("./loadVisibleNames");
        const result = await loadVisibleNames(
          addresses,
          snapshot.current,
          controller.signal,
          reset,
        );
        if (current()) {
          snapshot.current = result.token;
          setNames(result.names);
        }
      } catch {
        reset();
      }
    };
    const scheduled = setTimeout(() => {
      void load();
    }, 0);
    return () => {
      controller.abort();
      clearTimeout(scheduled);
    };
  }, [enabled, revision]);
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
