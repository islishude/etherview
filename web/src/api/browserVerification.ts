import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity } from "./privateAccount";
import { ApiError, apiClient, requireEnvelope } from "./client";
import * as sdk from "./heyapi/sdk.gen";
import type {
  BrowserVerificationJob,
  VerificationSubmission,
  SourcifySubmission,
  SourcifyFromEtherscanSubmission,
} from "./types";

type Accepted = (job: BrowserVerificationJob) => void;
type RequestOptions = {
  signal: AbortSignal;
  headers: { "X-CSRF-Token": string };
  client: typeof apiClient;
};

function sessionError(error: unknown) {
  return (
    error instanceof ApiError &&
    ["authentication_required", "user_disabled", "csrf_invalid"].includes(error.code)
  );
}

function useBrowserMutation<T>(
  send: (input: T, options: RequestOptions) => Promise<BrowserVerificationJob>,
  accepted: Accepted,
) {
  const auth = useAuth();
  const identity = usePrivateIdentity();
  const current = useRef(identity);
  current.current = identity;
  const controllers = useRef(new Set<AbortController>());
  useEffect(() => {
    const pending = controllers.current;
    return () => {
      for (const controller of pending) controller.abort();
      pending.clear();
    };
  }, [identity]);
  const mutation = useMutation({
    mutationFn: async ({ input, owner }: { input: T; owner: string; onAccepted: Accepted }) => {
      if (!owner || owner !== current.current || !auth.session.csrf_token)
        throw new Error("Authentication required");
      const controller = new AbortController();
      controllers.current.add(controller);
      try {
        const result = await send(input, {
          client: apiClient,
          signal: controller.signal,
          headers: { "X-CSRF-Token": auth.session.csrf_token },
        });
        if (controller.signal.aborted || owner !== current.current)
          throw new DOMException("Session changed", "AbortError");
        return result;
      } catch (error) {
        if (controller.signal.aborted || owner !== current.current)
          throw new DOMException("Session changed", "AbortError");
        throw error;
      } finally {
        controllers.current.delete(controller);
      }
    },
    onSuccess: (job, variables) => {
      if (variables.owner === current.current) variables.onAccepted(job);
    },
    onError: (error, variables) => {
      if (variables.owner === current.current && sessionError(error)) void auth.refresh();
    },
    gcTime: 0,
    retry: false,
  });
  const reset = mutation.reset;
  useEffect(() => {
    reset();
  }, [identity, reset]);
  return {
    mutate: (input: T) => mutation.mutate({ input, owner: identity, onAccepted: accepted }),
    reset: mutation.reset,
    isPending: mutation.isPending && mutation.variables?.owner === identity,
    error: mutation.variables?.owner === identity ? mutation.error : null,
  };
}

export function useSubmitVerification(address: string, accepted: Accepted) {
  return useBrowserMutation<VerificationSubmission>(
    async (body, options) =>
      requireEnvelope(
        await sdk.submitBrowserAddressVerification({ ...options, body: { ...body, address } }),
      ).data,
    accepted,
  );
}

export function useImportSourcifyContract(accepted: Accepted) {
  return useBrowserMutation<{ address: string; chainID: string }>(
    async ({ address, chainID }, options) =>
      requireEnvelope(
        await sdk.importBrowserSourcifyContract({
          ...options,
          body: { address, chain_id: chainID },
        }),
      ).data,
    accepted,
  );
}

export function useSubmitSourcifyVerification(accepted: Accepted) {
  return useBrowserMutation<
    | { kind: "sources"; body: SourcifySubmission }
    | { kind: "etherscan"; body: SourcifyFromEtherscanSubmission }
  >(async (request, options) => {
    try {
      return requireEnvelope(
        request.kind === "sources"
          ? await sdk.submitBrowserSourcifyVerification({ ...options, body: request.body })
          : await sdk.submitBrowserSourcifyFromEtherscan({ ...options, body: request.body }),
      ).data;
    } finally {
      if (request.kind === "etherscan") delete request.body.etherscan_api_key;
    }
  }, accepted);
}

export function useVerificationJob(id: string) {
  const auth = useAuth();
  const identity = usePrivateIdentity();
  const current = useRef(identity);
  current.current = identity;
  const client = useQueryClient();
  useEffect(
    () => () => {
      if (!identity) return;
      const filter = { queryKey: ["private-account", identity, "verification"] };
      void client.cancelQueries(filter);
      client.removeQueries(filter);
    },
    [client, identity],
  );
  return useQuery({
    queryKey: ["private-account", identity, "verification", id],
    queryFn: async ({ signal }) => {
      try {
        return requireEnvelope(
          await sdk.getBrowserVerifierJob({ client: apiClient, path: { id }, signal }),
        ).data;
      } catch (error) {
        if (!signal.aborted && identity === current.current && sessionError(error))
          void auth.refresh();
        throw error;
      }
    },
    enabled: Boolean(identity && id),
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchInterval: (query) =>
      query.state.status === "error"
        ? false
        : ["queued", "running"].includes(query.state.data?.status ?? "")
          ? 2_000
          : false,
  });
}
