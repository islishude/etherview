import { act, fireEvent, render, screen, waitFor, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useTurnstile } from "./TurnstileChallenge";
import {
  useImportSourcifyContract,
  useSubmitSourcifyVerification,
  useSubmitVerification,
} from "@/api/browserVerification";
import { verificationSession } from "../../test/verificationSession";
import i18n from "@/i18n";

const auth = vi.hoisted(() => ({
  enabled: true,
  session: {} as typeof verificationSession,
  refresh: vi.fn(),
}));
vi.mock("@/auth/AuthProvider", () => ({ useAuth: () => auth }));
type Options = Parameters<NonNullable<Window["turnstile"]>["render"]>[1];
let callbacks: Options[];
let remove = vi.fn<(id: string) => void>();
beforeEach(async () => {
  auth.session = verificationSession;
  callbacks = [];
  remove = vi.fn<(id: string) => void>();
  window.turnstile = {
    render: vi.fn((_container, options) => {
      callbacks.push(options);
      return String(callbacks.length);
    }),
    remove,
  };
  await i18n.changeLanguage("en");
});
afterEach(() => {
  cleanup();
  delete window.turnstile;
  vi.unstubAllGlobals();
});
function wrap(node: React.ReactNode, enabled = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData(["config"], {
    features: { user_auth: true },
    turnstile: { verification_enabled: enabled, csv_export_enabled: enabled, site_key: "public" },
  });
  return <QueryClientProvider client={client}>{node}</QueryClientProvider>;
}
function ChallengeForm() {
  const gate = useTurnstile("csv_export");
  return (
    <>
      {gate.challenge}
      <button disabled={!gate.ready} onClick={() => gate.consume()}>
        Download
      </button>
    </>
  );
}
it("expires, retries and consumes tokens without retaining them across identities", async () => {
  const tree = wrap(<ChallengeForm />);
  const view = render(tree);
  await waitFor(() => expect(callbacks).toHaveLength(1));
  const button = screen.getByRole("button", { name: "Download" });
  expect(button).toBeDisabled();
  act(() => callbacks[0]!.callback("first"));
  expect(button).toBeEnabled();
  act(() => callbacks[0]!["expired-callback"]());
  expect(button).toBeDisabled();
  fireEvent.click(screen.getByText("Retry verification"));
  await waitFor(() => expect(callbacks).toHaveLength(2));
  act(() => callbacks[1]!.callback("second"));
  fireEvent.click(button);
  expect(button).toBeDisabled();
  await waitFor(() => expect(callbacks).toHaveLength(3));
  act(() => callbacks[2]!.callback("third"));
  auth.session = {
    ...verificationSession,
    user: { ...verificationSession.user, id: "different-user" },
  };
  view.rerender(wrap(<ChallengeForm />));
  await waitFor(() => expect(callbacks).toHaveLength(4));
  expect(button).toBeDisabled();
  act(() => callbacks[2]!.callback("stale-owner"));
  expect(button).toBeDisabled();
  act(() => callbacks[3]!.callback("new-owner"));
  expect(button).toBeEnabled();
  view.unmount();
  expect(remove).toHaveBeenCalled();
});
it("does not mount or load a challenge while disabled or logged out", async () => {
  const view = render(wrap(<ChallengeForm />, false));
  expect(screen.getByRole("button", { name: "Download" })).toBeEnabled();
  expect(callbacks).toHaveLength(0);
  view.unmount();
  auth.session = { authenticated: false } as typeof verificationSession;
  render(wrap(<ChallengeForm />));
  expect(callbacks).toHaveLength(0);
  expect(document.querySelector('script[src*="challenges.cloudflare.com"]')).toBeNull();
});
function SubmissionForm({ kind }: { kind: string }) {
  const accepted = vi.fn();
  const local = useSubmitVerification("0x1111111111111111111111111111111111111111", accepted);
  const imported = useImportSourcifyContract(accepted);
  const sourcify = useSubmitSourcifyVerification(accepted);
  const mutation = kind === "local" ? local : kind === "sourcify-import" ? imported : sourcify;
  return (
    <>
      {mutation.challenge}
      <button
        disabled={!mutation.ready || mutation.isPending}
        onClick={() => {
          if (kind === "local")
            local.mutate({
              language: "solidity",
              compiler_version: "0.8.30",
              input_kind: "standard_json",
              input: {},
            });
          else if (kind === "sourcify-import")
            imported.mutate({
              address: "0x1111111111111111111111111111111111111111",
              chainID: "1",
            });
          else
            sourcify.mutate(
              kind === "sourcify"
                ? {
                    kind: "sources",
                    body: {
                      address: "0x1111111111111111111111111111111111111111",
                      chain_id: "1",
                      files: {},
                    },
                  }
                : {
                    kind: "etherscan",
                    body: { address: "0x1111111111111111111111111111111111111111", chain_id: "1" },
                  },
            );
        }}
      >
        Submit
      </button>
      {mutation.error && <p role="alert">{mutation.error.message}</p>}
    </>
  );
}
for (const kind of ["local", "sourcify-import", "sourcify", "sourcify-from-etherscan"]) {
  it(`sends a single token through the generated ${kind} client and allows a fresh retry`, async () => {
    const fetcher = vi.fn(async () =>
      Response.json(
        { error: { code: "turnstile_invalid", message: "Rejected", request_id: "test" } },
        { status: 403 },
      ),
    );
    vi.stubGlobal("fetch", fetcher);
    render(wrap(<SubmissionForm kind={kind} />));
    await waitFor(() => expect(callbacks).toHaveLength(1));
    act(() => callbacks[0]!.callback("token-one"));
    fireEvent.click(screen.getByText("Submit"));
    await screen.findByRole("alert");
    expect(fetcher).toHaveBeenCalledTimes(1);
    const [url, options] = fetcher.mock.calls[0]! as unknown as [string, RequestInit];
    expect(url).toBe(`/api/v1/verification-jobs/${kind}`);
    expect(new Headers(options.headers).get("X-Turnstile-Token")).toBe("token-one");
    expect(new Headers(options.headers).get("X-CSRF-Token")).toBe(verificationSession.csrf_token);
    expect(screen.getByText("Submit")).toBeDisabled();
    await waitFor(() => expect(callbacks).toHaveLength(2));
    act(() => callbacks[1]!.callback("token-two"));
    fireEvent.click(screen.getByText("Submit"));
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    expect(
      new Headers((fetcher.mock.calls[1]! as unknown as [string, RequestInit])[1].headers).get(
        "X-Turnstile-Token",
      ),
    ).toBe("token-two");
  });
}
