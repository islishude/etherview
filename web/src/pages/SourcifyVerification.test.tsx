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
  await userEvent
    .setup()
    .click(await screen.findByRole("radio", { name: /Sourcify source library/ }));
  await userEvent.setup().click(screen.getAllByRole("button", { name: "Publish to Sourcify" })[0]!);
  fireEvent.change(screen.getByLabelText(/^Etherview API Key(?!\s*[(（])/), {
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
  it.each(["", "0", "01", "-1", "1.5", "1e3", "18446744073709551616"])(
    "rejects invalid source chain %s without sending a request",
    async (chain) => {
      const { fetcher } = setup();
      await userEvent
        .setup()
        .click(await screen.findByRole("radio", { name: /Sourcify source library/ }));
      fireEvent.change(screen.getByLabelText("Source Chain ID"), { target: { value: chain } });
      fireEvent.change(screen.getByLabelText(/^Etherview API Key(?!\s*[(（])/), {
        target: { value: secret },
      });
      await userEvent
        .setup()
        .click(screen.getByRole("button", { name: "Import from Sourcify into Etherview" }));
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "Enter a positive decimal Chain ID",
      );
      expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
    },
  );

  it("replaces uploads independently and rejects misplaced or invalid metadata", async () => {
    setup();
    await selectSourcify();
    const file = (name: string, content: string) => {
      const value = new File([content], name, {
        type: name.endsWith(".json") ? "application/json" : "text/plain",
      });
      Object.defineProperty(value, "text", { value: async () => content });
      return value;
    };
    const uploadMetadata = async (content: string) =>
      userEvent
        .setup()
        .upload(
          screen.getByLabelText("metadata.json", { exact: true }),
          file("metadata.json", content),
        );
    await uploadMetadata(files["metadata.json"]);
    await waitFor(() =>
      expect(screen.getByLabelText(/^Sourcify files/)).toHaveValue(
        JSON.stringify({ "metadata.json": files["metadata.json"] }, null, 2),
      ),
    );
    await userEvent
      .setup()
      .upload(screen.getByLabelText("Source files"), file("A.sol", "contract A {}"));
    await waitFor(() =>
      expect(
        JSON.parse((screen.getByLabelText(/^Sourcify files/) as HTMLTextAreaElement).value),
      ).toEqual({ "A.sol": "contract A {}", "metadata.json": files["metadata.json"] }),
    );
    await userEvent.setup().click(screen.getByRole("checkbox"));
    await userEvent
      .setup()
      .upload(screen.getByLabelText("Source files"), file("B.sol", "contract B {}"));
    await waitFor(() => expect(screen.getByRole("checkbox")).not.toBeChecked());
    await waitFor(() =>
      expect(
        JSON.parse((screen.getByLabelText(/^Sourcify files/) as HTMLTextAreaElement).value),
      ).toEqual({ "B.sol": "contract B {}", "metadata.json": files["metadata.json"] }),
    );
    const updated = '{"compiler":{},"settings":{},"output":{},"version":1}';
    await uploadMetadata(updated);
    await waitFor(() =>
      expect(
        JSON.parse((screen.getByLabelText(/^Sourcify files/) as HTMLTextAreaElement).value),
      ).toEqual({ "B.sol": "contract B {}", "metadata.json": updated }),
    );
    await uploadMetadata("{}");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Select one valid compiler metadata",
    );
    await userEvent
      .setup()
      .upload(screen.getByLabelText("Source files"), file("metadata.json", updated));
    expect(await screen.findByRole("alert")).toHaveTextContent("separate upload area");
    expect(
      JSON.parse((screen.getByLabelText(/^Sourcify files/) as HTMLTextAreaElement).value),
    ).toEqual({ "B.sol": "contract B {}", "metadata.json": updated });
  });

  it("retains the contract address when switching between retrieval and publication", async () => {
    setup();
    await userEvent
      .setup()
      .click(await screen.findByRole("radio", { name: /Sourcify source library/ }));
    const edited = `0x${"34".repeat(20)}`;
    fireEvent.change(screen.getByLabelText("Address"), { target: { value: edited } });
    await userEvent
      .setup()
      .click(screen.getAllByRole("button", { name: "Publish to Sourcify" })[0]!);
    expect(screen.getByLabelText("Address")).toHaveValue(edited);
    fireEvent.change(screen.getByLabelText("Address"), { target: { value: address } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Import into Etherview" }));
    expect(screen.getByLabelText("Address")).toHaveValue(address);
  });
  it("opens direct import first and queues local verification without uploading files", async () => {
    const { fetcher } = setup();
    const original = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (url, init) =>
      String(url).endsWith("/verification/sourcify")
        ? envelope({ id: jobID, kind: "address", status: "queued" })
        : original(url, init),
    );
    await userEvent
      .setup()
      .click(await screen.findByRole("radio", { name: /Sourcify source library/ }));
    expect(screen.getByRole("button", { name: "Import into Etherview" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.queryByLabelText(/^Sourcify files/)).toBeNull();
    expect(screen.queryByLabelText(/^Etherscan API Key/)).toBeNull();
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.getByLabelText("Source Chain ID")).toHaveValue("1");
    fireEvent.change(screen.getByLabelText("Source Chain ID"), {
      target: { value: "9007199254740993" },
    });
    fireEvent.change(screen.getByLabelText(/^Etherview API Key(?!\s*[(（])/), {
      target: { value: secret },
    });
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Import from Sourcify into Etherview" }));
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(([url]) => String(url).endsWith("/verification/sourcify")),
      ).toBe(true),
    );
    const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
    expect(post[0]).toBe(`/api/v1/contracts/${address}/verification/sourcify`);
    expect(new Headers(post[1]?.headers).get("X-API-Key")).toBe(secret);
    expect(JSON.parse(String(post[1]?.body))).toEqual({ chain_id: "9007199254740993" });
    expect(fetcher.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
  });

  it("keeps a failed direct import explicit and never uploads automatically", async () => {
    const { fetcher } = setup();
    const original = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (url, init) =>
      String(url).endsWith("/verification/sourcify")
        ? Response.json(
            {
              error: {
                code: "sourcify_not_found",
                request_id: "import-test",
                message: "No verified contract was found on Sourcify",
              },
            },
            { status: 404 },
          )
        : original(url, init),
    );
    await userEvent
      .setup()
      .click(await screen.findByRole("radio", { name: /Sourcify source library/ }));
    fireEvent.change(screen.getByLabelText(/^Etherview API Key(?!\s*[(（])/), {
      target: { value: secret },
    });
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Import from Sourcify into Etherview" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("No verified contract was found");
    expect(fetcher.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Import into Etherview" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("imports the submitted address after publication, retaining separate local and external results", async () => {
    const localID = "123e4567-e89b-42d3-a456-426614174002";
    const { fetcher } = setup({ kind: "sourcify_from_etherscan" });
    const original = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (url, init) =>
      String(url).endsWith("/verification/sourcify")
        ? envelope({ id: localID, kind: "address", status: "queued" })
        : String(url).endsWith(`/jobs/${localID}`)
          ? envelope({
              id: localID,
              kind: "address",
              status: "succeeded",
              outcome: { kind: "verification_success", runtime_match: { match_type: "full" } },
            })
          : original(url, init),
    );
    await selectSourcify();
    fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
      target: { value: "etherscan" },
    });
    await userEvent.setup().click(screen.getByRole("checkbox"));
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Import from Etherscan and publish to Sourcify" }));
    await screen.findByText("Sourcify verification succeeded (external result)");
    fireEvent.change(screen.getByLabelText("Address"), {
      target: { value: `0x${"34".repeat(20)}` },
    });
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Import from Sourcify into Etherview" }));
    await screen.findByText("verification_success");
    expect(screen.getByText("Sourcify verification succeeded (external result)")).toBeVisible();
    expect(screen.getByText("Runtime match")).toBeVisible();
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST").map(([url]) => url),
    ).toEqual([
      "/api/v1/verifier/sourcify/from-etherscan",
      `/api/v1/contracts/${address}/verification/sourcify`,
    ]);
  });

  it.each([false, true])(
    "forwards and clears the separate Etherscan key (failure=%s)",
    async (failure) => {
      const etherscanKey = "etherscan-private-test-key";
      const { fetcher, client } = setup({ kind: "sourcify_from_etherscan" });
      await selectSourcify();
      expect(screen.queryByLabelText(/^Etherscan API Key/)).toBeNull();
      fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
        target: { value: "etherscan" },
      });
      const input = screen.getByLabelText(/^Etherscan API Key/);
      expect(input).toHaveAttribute("type", "password");
      expect(input).not.toBeRequired();
      await userEvent.setup().click(screen.getByRole("checkbox"));
      fireEvent.change(input, { target: { value: etherscanKey } });
      expect(screen.getByRole("checkbox")).not.toBeChecked();
      if (failure) {
        const original = fetcher.getMockImplementation()!;
        fetcher.mockImplementation(async (url, init) =>
          init?.method === "POST"
            ? Response.json(
                {
                  error: {
                    code: "verification_unavailable",
                    message: "Verification is unavailable",
                  },
                },
                { status: 503 },
              )
            : original(url, init),
        );
      }
      await userEvent.setup().click(screen.getByRole("checkbox"));
      await userEvent
        .setup()
        .click(
          screen.getByRole("button", { name: "Import from Etherscan and publish to Sourcify" }),
        );
      await waitFor(() => expect(input).toHaveValue(""));
      const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST")!;
      expect(new Headers(post[1]?.headers).get("X-API-Key")).toBe(secret);
      expect(JSON.parse(String(post[1]?.body))).toEqual({
        chain_id: "1",
        address,
        etherscan_api_key: etherscanKey,
      });
      expect(
        JSON.stringify(
          client
            .getMutationCache()
            .getAll()
            .map((mutation) => mutation.state.variables),
        ),
      ).not.toContain(etherscanKey);
      expect(
        JSON.stringify(
          client
            .getQueryCache()
            .getAll()
            .map((query) => query.queryKey),
        ),
      ).not.toContain(etherscanKey);
      expect(
        location.href + JSON.stringify(localStorage) + JSON.stringify(sessionStorage),
      ).not.toContain(etherscanKey);
      fireEvent.change(input, { target: { value: etherscanKey } });
      fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
        target: { value: "sources" },
      });
      fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
        target: { value: "etherscan" },
      });
      expect(screen.getByLabelText(/^Etherscan API Key/)).toHaveValue("");
    },
  );

  it.each(["has space", "密钥", "x".repeat(257)])(
    "rejects an invalid Etherscan credential before posting",
    async (value) => {
      const { fetcher } = setup();
      await selectSourcify();
      fireEvent.change(screen.getByLabelText("Sourcify submission method"), {
        target: { value: "etherscan" },
      });
      fireEvent.change(screen.getByLabelText(/^Etherscan API Key/), { target: { value } });
      await userEvent.setup().click(screen.getByRole("checkbox"));
      await userEvent
        .setup()
        .click(
          screen.getByRole("button", { name: "Import from Etherscan and publish to Sourcify" }),
        );
      expect(await screen.findByRole("alert")).toBeVisible();
      expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
    },
  );

  it("hides the entry when the capability is disabled", async () => {
    const { fetcher } = setup({ enabled: false });
    await screen.findByText("Public verification is unavailable");
    expect(screen.queryByRole("group", { name: "Verification service" })).toBeNull();
    expect(fetcher.mock.calls.some(([url]) => String(url).includes("sourcify"))).toBe(false);
  });

  it("keeps local verification available when only Sourcify is disabled", async () => {
    const { fetcher } = setup({ sourcify: false });
    await screen.findByText("Verification request");
    expect(screen.queryByRole("group", { name: "Verification service" })).toBeNull();
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
      const file = new File([content], name, {
        type: name.endsWith(".json") ? "application/json" : "text/plain",
      });
      Object.defineProperty(file, "text", { value: async () => content });
      return file;
    });
    await userEvent.setup().upload(screen.getByLabelText("Source files"), selected[0]!);
    await waitFor(() =>
      expect(screen.getByLabelText(/^Sourcify files/)).toHaveValue(
        JSON.stringify({ "src/A.sol": files["src/A.sol"] }, null, 2),
      ),
    );
    await userEvent
      .setup()
      .upload(screen.getByLabelText("metadata.json", { exact: true }), selected[1]!);
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
    fireEvent.change(screen.getByLabelText("Etherview API Key (job read)"), {
      target: { value: secret },
    });
    await userEvent.setup().click(screen.getByRole("button", { name: "Load job" }));
    expect(
      await screen.findByText("Sourcify verification succeeded (external result)"),
    ).toBeVisible();
    await i18n.changeLanguage("zh");
    expect(await screen.findByText("Sourcify 验证成功（外部结果）")).toBeVisible();
    expect(screen.getByText("Sourcify 外部验证")).toBeVisible();
  });
});
