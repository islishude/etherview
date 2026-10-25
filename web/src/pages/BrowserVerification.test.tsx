import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WalletProvider } from "@/wallet/WalletProvider";
import { verificationSession } from "../../test/verificationSession";
import { uploadFiles } from "@/test/verificationUpload";
import i18n from "@/i18n";
import { VerifyPage } from "./VerifyPage";
import { VerificationJobPanel } from "./VerificationJobPanel";

const auth = vi.hoisted(() => ({
  enabled: true,
  loading: false,
  pending: false,
  session: { authenticated: false } as Record<string, unknown>,
  refresh: vi.fn(),
  login: vi.fn(),
  error: undefined,
}));
vi.mock("@/auth/AuthProvider", async (original) => ({
  ...(await original<object>()),
  useAuth: () => auth,
}));
const id = "123e4567-e89b-42d3-a456-426614174000";
const address = "0x1111111111111111111111111111111111111111";
const envelope = (data: unknown, status = 200) =>
  Response.json({ data, meta: { chain_id: "1", request_id: "flow" } }, { status });
function setup(initialJob = "") {
  const onJobChange = vi.fn();
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/api/v1/config")
      return envelope({ chain_id: "1", features: { verification: true, user_auth: true } });
    if (path.includes("compilers")) return envelope({ language: "solidity", versions: ["0.8.30"] });
    if (init?.method === "POST") return envelope({ id, kind: "address", status: "queued" }, 202);
    return envelope({
      id,
      address,
      chain_id: "1",
      kind: "address",
      status: "succeeded",
      outcome: { kind: "verification_success" },
    });
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      <WalletProvider>
        <VerifyPage initialAddress={address} initialJob={initialJob} onJobChange={onJobChange} />
      </WalletProvider>
    </QueryClientProvider>
  );
  const view = render(tree());
  return { ...view, fetcher, client, onJobChange, refresh: () => view.rerender(tree()) };
}
beforeEach(async () => {
  await i18n.changeLanguage("en");
  auth.enabled = true;
  auth.session = verificationSession;
  auth.refresh.mockClear();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("browser verification flow", () => {
  it("clears completed submission results from the cache on logout", async () => {
    const view = setup();
    await screen.findByRole("option", { name: "0.8.30" });
    await uploadFiles(screen.getByLabelText("Standard JSON input"), { "input.json": "{}" });
    await userEvent.setup().click(screen.getByRole("button", { name: "Submit verification" }));
    await screen.findByText("Contract verification succeeded.");
    expect(view.client.getMutationCache().getAll()).toHaveLength(1);
    auth.session = { authenticated: false };
    view.refresh();
    await waitFor(() => expect(view.client.getMutationCache().getAll()).toHaveLength(0));
  });

  it("does not claim that every contract in a completed batch was verified", () => {
    render(
      <VerificationJobPanel
        loading={false}
        error={null}
        job={{
          id,
          kind: "solidity_batch_standard_json",
          status: "succeeded",
          created_at: "2026-10-25T00:00:00Z",
          updated_at: "2026-10-25T00:00:01Z",
          outcome: { kind: "batch_results" },
        }}
      />,
    );
    expect(screen.queryByText("Contract verification succeeded.")).toBeNull();
    expect(screen.getByText(/Batch processing completed/)).toBeVisible();
    expect(screen.queryByRole("link", { name: "View verified contract" })).toBeNull();
  });

  it("explains disabled authentication without offering an API-key fallback", async () => {
    auth.enabled = false;
    auth.session = { authenticated: false };
    const view = setup();
    await screen.findByRole("option", { name: "0.8.30" });
    expect(screen.getByText(/user sign-in, which is disabled on this deployment/)).toBeVisible();
    expect(screen.getByRole("button", { name: "Submit verification" })).toBeDisabled();
    expect(screen.queryByLabelText(/API Key/)).toBeNull();
    expect(view.fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it.each(["compilation_failure", "verification_failure"] as const)(
    "shows %s as failure even when queue processing succeeded",
    (kind) => {
      render(
        <VerificationJobPanel
          loading={false}
          error={null}
          job={{
            id,
            address,
            chain_id: "1",
            kind: "address",
            status: "succeeded",
            created_at: "2026-10-25T00:00:00Z",
            updated_at: "2026-10-25T00:00:01Z",
            outcome: { kind },
          }}
        />,
      );
      expect(screen.getByText("failed", { exact: true })).toBeVisible();
      expect(screen.queryByText("succeeded", { exact: true })).toBeNull();
      expect(screen.queryByRole("link", { name: "View verified contract" })).toBeNull();
      expect(
        screen.getByText(
          kind === "compilation_failure" ? /Compilation failed/ : /compiled bytecode did not match/,
        ),
      ).toBeVisible();
    },
  );

  it("keeps the draft through login and back navigation, and submits only on an explicit click", async () => {
    auth.session = { authenticated: false };
    const view = setup();
    await screen.findByRole("option", { name: "0.8.30" });
    expect(screen.queryByRole("heading", { name: "Verification job" })).toBeNull();
    expect(screen.queryByLabelText(/Etherview API Key/)).toBeNull();
    expect(screen.queryByText("Open a durable verification job")).toBeNull();
    await uploadFiles(screen.getByLabelText("Standard JSON input"), {
      "input.json": '{"language":"Solidity","sources":{},"settings":{}}',
    });
    expect(screen.getByRole("button", { name: "Submit verification" })).toBeDisabled();
    auth.session = verificationSession;
    view.refresh();
    expect(view.fetcher.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
    await userEvent.setup().click(screen.getByRole("button", { name: "Submit verification" }));
    await screen.findByText("Contract verification succeeded.");
    expect(view.onJobChange).toHaveBeenCalledWith(id);
    expect(screen.queryByRole("button", { name: "Submit verification" })).toBeNull();
    const post = view.fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
    expect(post[0]).toBe("/api/v1/verification-jobs/local");
    expect(new Headers(post[1]?.headers).get("X-API-Key")).toBeNull();
    expect(new Headers(post[1]?.headers).get("X-CSRF-Token")).toBe(verificationSession.csrf_token);
    expect(JSON.parse(String(post[1]?.body)).address).toBe(address);
    expect(screen.getByRole("link", { name: "View verified contract" })).toHaveAttribute(
      "href",
      `/address/${address}#code`,
    );
    await userEvent.setup().click(screen.getByRole("button", { name: "Back to form" }));
    expect(screen.getByText("input.json")).toBeVisible();
    expect(screen.getByLabelText("Address")).toHaveValue(address);
  });

  it("restores a result only after login, clears it on logout, and permits explicit read retry", async () => {
    auth.session = { authenticated: false };
    const view = setup(id);
    await screen.findByText(/Sign in to submit or view/);
    expect(view.fetcher.mock.calls.some(([path]) => String(path).includes(id))).toBe(false);
    auth.session = verificationSession;
    view.refresh();
    await screen.findByText("Contract verification succeeded.");
    auth.session = { authenticated: false };
    view.refresh();
    expect(screen.queryByText("Contract verification succeeded.")).toBeNull();
    await waitFor(() =>
      expect(
        view.client
          .getQueryCache()
          .getAll()
          .filter((query) => query.queryKey[0] === "private-account"),
      ).toHaveLength(1),
    );
    const original = view.fetcher.getMockImplementation()!;
    let unavailable = true;
    view.fetcher.mockImplementation((input, init) =>
      String(input).includes(id) && unavailable
        ? Promise.resolve(
            Response.json(
              { error: { code: "not_found", message: "Job missing" } },
              { status: 404 },
            ),
          )
        : original(input, init),
    );
    auth.session = verificationSession;
    view.refresh();
    await screen.findByRole("button", { name: "Retry loading result" });
    unavailable = false;
    await userEvent.setup().click(screen.getByRole("button", { name: "Retry loading result" }));
    await screen.findByText("Contract verification succeeded.");
    expect(view.fetcher.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
  });

  it("ignores a late authentication error from a cancelled result read", async () => {
    auth.session = { authenticated: false };
    const view = setup(id);
    await screen.findByText(/Sign in to submit or view/);
    let resolve: ((value: Response) => void) | undefined;
    let signal: AbortSignal | null | undefined;
    const original = view.fetcher.getMockImplementation()!;
    view.fetcher.mockImplementation((input, init) =>
      String(input).includes(id)
        ? new Promise((done) => {
            resolve = done;
            signal = init?.signal;
          })
        : original(input, init),
    );
    auth.session = verificationSession;
    view.refresh();
    await waitFor(() => expect(resolve).toBeDefined());
    auth.session = { authenticated: false };
    view.refresh();
    expect(signal?.aborted).toBe(true);
    await act(async () =>
      resolve!(
        Response.json(
          { error: { code: "authentication_required", message: "Expired" } },
          { status: 401 },
        ),
      ),
    );
    expect(auth.refresh).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Retry loading result" })).toBeNull();
  });

  it("aborts submission on account change and ignores a late response", async () => {
    const view = setup();
    await screen.findByRole("option", { name: "0.8.30" });
    await uploadFiles(screen.getByLabelText("Standard JSON input"), { "input.json": "{}" });
    let resolve: ((value: Response) => void) | undefined;
    let signal: AbortSignal | null | undefined;
    const original = view.fetcher.getMockImplementation()!;
    view.fetcher.mockImplementation((input, init) =>
      init?.method === "POST"
        ? new Promise((done) => {
            resolve = done;
            signal = init.signal;
          })
        : original(input, init),
    );
    fireEvent.submit(screen.getByRole("button", { name: "Submit verification" }).closest("form")!);
    await waitFor(() => expect(resolve).toBeDefined());
    auth.session = {
      ...verificationSession,
      user: { ...verificationSession.user, id: "018f3b52-0b3d-7bf1-b65f-6f214827cb42" },
    };
    view.refresh();
    expect(signal?.aborted).toBe(true);
    await act(async () => resolve!(envelope({ id, status: "queued", kind: "address" }, 202)));
    expect(view.onJobChange).not.toHaveBeenCalled();
    expect(screen.queryByRole("heading", { name: "Verification result" })).toBeNull();
    await waitFor(() => expect(view.client.getMutationCache().getAll()).toHaveLength(0));
  });
});

it("polls active jobs and stops at a terminal result", async () => {
  const view = setup(id);
  let reads = 0;
  const original = view.fetcher.getMockImplementation()!;
  view.fetcher.mockImplementation((input, init) => {
    if (!String(input).includes(id)) return original(input, init);
    reads++;
    return Promise.resolve(
      envelope({
        id,
        kind: "address",
        status: reads === 1 ? "running" : "succeeded",
        ...(reads > 1 ? { outcome: { kind: "verification_success" } } : {}),
      }),
    );
  });
  await waitFor(() => expect(reads).toBe(2), { timeout: 3500 });
  await screen.findByText("Contract verification succeeded.");
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 2200));
  });
  expect(reads).toBe(2);
}, 10000);
