import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { VerifyPage } from "./VerifyPage";

const address = `0x${"12".repeat(20)}`;
const jobID = "123e4567-e89b-42d3-a456-426614174000";
const externalID = "123e4567-e89b-42d3-a456-426614174001";
const secret = "ev_sourcify_private_key";
const files = {
  "src/A.sol": "contract A {}",
  "metadata.json": '{"compiler":{},"settings":{},"output":{}}',
};
const envelope = (data: unknown) =>
  Response.json({ data, meta: { chain_id: "1", request_id: "test" } });

function setup({
  enabled = true,
  sourcify = enabled,
  outcome = "sourcify_success",
  kind = "sourcify",
}: { enabled?: boolean; sourcify?: boolean; outcome?: string; kind?: string } = {}) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/api/v1/config")
      return envelope({ chain_id: "1", features: { verification: enabled, sourcify } });
    if (path.includes("/compilers"))
      return envelope({ language: "solidity", versions: ["0.8.30"] });
    if (path.includes("/verifier/sourcify")) {
      expect(new Headers(init?.headers).get("X-API-Key")).toBe(secret);
      return envelope({ id: jobID, kind, status: "queued" });
    }
    if (path === `/api/v1/verifier/jobs/${jobID}`) {
      expect(new Headers(init?.headers).get("X-API-Key")).toBe(secret);
      return envelope({
        id: jobID,
        kind,
        status: "succeeded",
        outcome: { kind: outcome, verification_id: externalID },
        updated_at: "2026-10-25T00:00:00Z",
      });
    }
    return Response.json({ error: { code: "not_found", message: "Not found" } }, { status: 404 });
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <VerifyPage initialAddress={address} />
    </QueryClientProvider>,
  );
  return { fetcher, client };
}

async function selectSourcify() {
  fireEvent.change(await screen.findByLabelText("Verification service"), {
    target: { value: "sourcify" },
  });
  fireEvent.change(screen.getByLabelText(/^API key/), {
    target: { value: secret },
  });
}

beforeEach(async () => {
  await i18n.changeLanguage("en");
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Sourcify verification", () => {
  it("hides the entry when the capability is disabled", async () => {
    const { fetcher } = setup({ enabled: false });
    await screen.findByText("Public verification is unavailable");
    expect(screen.queryByLabelText("Verification service")).toBeNull();
    expect(fetcher.mock.calls.some(([url]) => String(url).includes("sourcify"))).toBe(false);
  });

  it("keeps local verification available when only Sourcify is disabled", async () => {
    const { fetcher } = setup({ sourcify: false });
    await screen.findByText("Verification request");
    expect(screen.queryByLabelText("Verification service")).toBeNull();
    expect(screen.getByRole("button", { name: "Submit verification" })).toBeVisible();
    expect(fetcher.mock.calls.some(([url]) => String(url).includes("sourcify"))).toBe(false);
  });

  it("shows a submission error without automatically repeating an external publication", async () => {
    const { fetcher } = setup();
    await selectSourcify();
    const original = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (url, init) =>
      init?.method === "POST"
        ? Response.json(
            {
              error: {
                code: "verification_unavailable",
                message: "Verification is unavailable",
                request_id: "sourcify-rejected",
              },
            },
            { status: 503 },
          )
        : original(url, init),
    );
    fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
      target: { value: "etherscan" },
    });
    await userEvent.setup().click(screen.getByRole("checkbox"));
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Import from Etherscan and publish to Sourcify" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Verification is unavailable");
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    expect(fetcher.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
    expect(screen.queryByText("Sourcify verification succeeded (external result)")).toBeNull();
  });

  it.each(["sources", "etherscan"] as const)(
    "submits %s only with explicit consent and shows the external ticket",
    async (mode) => {
      const { fetcher, client } = setup({
        kind: mode === "sources" ? "sourcify" : "sourcify_from_etherscan",
      });
      await selectSourcify();
      fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
        target: { value: mode },
      });
      if (mode === "sources")
        fireEvent.change(screen.getByLabelText(/^Sourcify files/), {
          target: { value: JSON.stringify(files) },
        });
      const button = screen.getByRole("button", {
        name:
          mode === "sources"
            ? "Publish sources to Sourcify"
            : "Import from Etherscan and publish to Sourcify",
      });
      expect(button).toBeDisabled();
      expect(screen.getByText(/Submitting sends the source files/)).toBeVisible();
      await userEvent.setup().click(screen.getByRole("checkbox"));
      await userEvent.setup().click(button);
      await screen.findByText("Sourcify verification succeeded (external result)");
      const panel = screen.getByText("Sourcify verification ID").closest("section")!;
      expect(within(panel).getByText(externalID)).toBeVisible();
      expect(
        within(panel).getByText(/do not mark this contract as locally verified/),
      ).toBeVisible();
      expect(within(panel).queryByText("Runtime match")).toBeNull();
      expect(screen.getByRole("checkbox")).not.toBeChecked();
      const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
      expect(post[0]).toBe(
        `/api/v1/verifier/sourcify${mode === "etherscan" ? "/from-etherscan" : ""}`,
      );
      expect(JSON.parse(String(post[1]?.body))).toEqual({
        chain_id: "1",
        address,
        ...(mode === "sources" ? { files } : {}),
      });
      expect(fetcher.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
      expect(
        JSON.stringify(
          client
            .getQueryCache()
            .getAll()
            .map((query) => query.queryKey),
        ),
      ).not.toContain(secret);
      expect(location.href).not.toContain(secret);
      expect(
        Array.from({ length: localStorage.length }, (_, index) =>
          localStorage.getItem(localStorage.key(index)!),
        ).join(),
      ).not.toContain(secret);
    },
  );

  it("loads selected files and revokes consent when the request changes", async () => {
    setup();
    await selectSourcify();
    const selected = Object.entries(files).map(([name, content]) => {
      const file = new File([content], name);
      Object.defineProperty(file, "text", { value: async () => content });
      return file;
    });
    await userEvent
      .setup()
      .upload(screen.getByLabelText("Select source files and metadata.json"), selected);
    await waitFor(() =>
      expect(screen.getByLabelText(/^Sourcify files/)).toHaveValue(JSON.stringify(files, null, 2)),
    );
    await userEvent.setup().click(screen.getByRole("checkbox"));
    fireEvent.change(screen.getByLabelText("Address"), {
      target: { value: `0x${"34".repeat(20)}` },
    });
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    await userEvent.setup().click(screen.getByRole("checkbox"));
    fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
      target: { value: "etherscan" },
    });
    expect(screen.getByRole("checkbox")).not.toBeChecked();
  });

  it.each(['{"a":"one","a":"two"}', '{"a":42}', "{}", "[]", "{"])(
    "rejects invalid file maps: %s",
    async (value) => {
      const { fetcher } = setup();
      await selectSourcify();
      fireEvent.change(screen.getByLabelText(/^Sourcify files/), {
        target: { value },
      });
      await userEvent.setup().click(screen.getByRole("checkbox"));
      await userEvent
        .setup()
        .click(screen.getByRole("button", { name: "Publish sources to Sourcify" }));
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "Provide a non-empty JSON path-to-content map",
      );
      expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
    },
  );

  it("rejects oversized and over-count requests before making a POST", async () => {
    const { fetcher } = setup();
    await selectSourcify();
    for (const value of [
      JSON.stringify({ "A.sol": "x".repeat(5 * 1024 * 1024) }),
      JSON.stringify(
        Object.fromEntries(Array.from({ length: 1025 }, (_, index) => [`${index}.sol`, ""])),
      ),
    ]) {
      fireEvent.change(screen.getByLabelText(/^Sourcify files/), { target: { value } });
      await userEvent.setup().click(screen.getByRole("checkbox"));
      await userEvent
        .setup()
        .click(screen.getByRole("button", { name: "Publish sources to Sourcify" }));
      expect(await screen.findByRole("alert")).toBeVisible();
    }
    expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("labels a completed negative external outcome as unsuccessful, not local success", async () => {
    setup({ outcome: "verification_failure", kind: "sourcify_from_etherscan" });
    await selectSourcify();
    fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
      target: { value: "etherscan" },
    });
    await userEvent.setup().click(screen.getByRole("checkbox"));
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Import from Etherscan and publish to Sourcify" }));
    expect(await screen.findByText("Sourcify verification did not succeed")).toBeVisible();
    expect(screen.queryByText("succeeded", { exact: true })).toBeNull();
    expect(screen.getByText("Completed", { exact: true })).toBeVisible();
    expect(screen.queryByText("Sourcify verification ID")).toBeNull();
  });

  it("reads external results after submissions are disabled and translates their provenance", async () => {
    setup({ enabled: false });
    await screen.findByText("Public verification is unavailable");
    fireEvent.change(screen.getByLabelText("Job ID"), { target: { value: jobID } });
    fireEvent.change(screen.getByLabelText("Job read API key"), { target: { value: secret } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Load job" }));
    expect(
      await screen.findByText("Sourcify verification succeeded (external result)"),
    ).toBeVisible();
    await i18n.changeLanguage("zh");
    expect(await screen.findByText("Sourcify 验证成功（外部结果）")).toBeVisible();
    expect(screen.getByText("Sourcify 外部验证")).toBeVisible();
  });
});
