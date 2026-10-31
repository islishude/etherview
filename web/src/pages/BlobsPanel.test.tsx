import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type * as schemas from "@/api/heyapi/types.gen";
import { BlobsPanel } from "./BlobsPanel";
import { BlobPage } from "./BlobPage";
import { BlobActions } from "./BlobActions";
import { shorten } from "@/components/format";

const authState = vi.hoisted(() => ({
  enabled: true,
  loading: false,
  session: { authenticated: true, csrf_token: "csrf", expires_at: "one", user: { id: "user" } },
}));
vi.mock("@/auth/AuthProvider", () => ({ useAuth: () => authState }));
vi.mock("@/components/SIWELoginControl", () => ({
  SIWELoginControl: ({ label }: { label: string }) => <button>{label}</button>,
}));
vi.mock("@/components/TurnstileChallenge", () => ({
  useTurnstile: () => {
    throw new Error("Blob content must not use Turnstile");
  },
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  Link: ({
    children,
    to,
    params,
    ...props
  }: {
    children: ReactNode;
    to: string;
    params: Record<string, string>;
  }) => (
    <a {...props} href={to.replace(/\$(\w+)/g, (_, key: string) => params[key] ?? "")}>
      {children}
    </a>
  ),
}));

const block = `0x${"11".repeat(32)}`;
const transaction = `0x${"22".repeat(32)}`;
const versionedHash = `0x${"33".repeat(32)}`;
const commitment = `0x${"44".repeat(48)}`;
const checksum = `0x${"55".repeat(32)}`;
const data: schemas.BlockBlobs = {
  block_hash: block,
  state: "complete",
  items: [
    {
      index: "42",
      transaction_hash: transaction,
      transaction_index: "1",
      versioned_hash: versionedHash,
      commitment,
      sha256: checksum,
      size: "131072",
    },
  ],
};

beforeEach(async () => {
  authState.session.authenticated = true;
  authState.session.expires_at = "one";
  await i18n.changeLanguage("en");
});
afterEach(() => vi.unstubAllGlobals());

function renderBlobs({
  metadata = data,
  tx,
  index,
  hashes,
  content = async () => new Response(new Uint8Array(131072).fill(7)),
}: {
  metadata?: typeof data;
  tx?: string;
  index?: string;
  hashes?: string[];
  content?: () => Promise<Response>;
} = {}) {
  const reads: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (request: RequestInfo | URL) => {
      const path = new URL(String(request), "http://etherview.test").pathname;
      if (path.endsWith("/content")) {
        reads.push(path);
        const response = await content();
        response.headers.set(
          "Content-Disposition",
          "attachment; filename=blob-9007199254740993-42.bin",
        );
        return response;
      }
      expect(path).toBe(`/api/v1/blocks/${block}/blobs`);
      return Response.json({ data: metadata, meta: { chain_id: "1", request_id: "blob-test" } });
    }),
  );
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      {index !== undefined ? (
        <BlobPage hash={block} index={index} />
      ) : (
        <BlobsPanel hash={block} transaction={tx} hashes={hashes} />
      )}
    </QueryClientProvider>,
  );
  return reads;
}

it.each(["en", "zh"])(
  "keeps the %s list compact and links to exact blob details",
  async (language) => {
    await i18n.changeLanguage(language);
    const reads = renderBlobs();
    const link = await screen.findByRole("link", {
      name: language === "en" ? "View Blob 42" : "查看 Blob 42",
    });
    expect(link).toHaveAttribute("href", `/blocks/${block}/blobs/42`);
    expect(link).toHaveTextContent(shorten(versionedHash, 12, 8));
    expect(screen.queryByText(versionedHash)).not.toBeInTheDocument();
    expect(screen.queryByText(commitment)).not.toBeInTheDocument();
    expect(screen.queryByText(checksum)).not.toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(reads).toEqual([]);
  },
);

it.each(["en", "zh"])("shows full %s metadata only on the detail page", async (language) => {
  await i18n.changeLanguage(language);
  const reads = renderBlobs({ index: "42" });
  expect(
    screen.getByRole("heading", {
      level: 1,
      name: language === "en" ? "Blob details" : "Blob 详情",
    }),
  ).toBeVisible();
  expect(await screen.findByText(versionedHash)).toBeVisible();
  expect(screen.getByText(commitment)).toBeVisible();
  expect(screen.getByText(checksum)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: language === "en" ? "Download blob" : "下载 Blob" }),
  ).not.toBeInTheDocument();
  expect(reads).toEqual([]);
});

it("previews only on demand, disables actions while loading, and hides without another read", async () => {
  let finish!: (response: Response) => void;
  const reads = renderBlobs({
    index: "42",
    content: () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  });
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Preview first 1 KiB" }));
  expect(screen.getByRole("button", { name: "Loading preview…" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Download blob" })).not.toBeInTheDocument();
  await act(async () => {
    finish(new Response(new Uint8Array(131072).fill(7)));
  });
  const preview = await screen.findByLabelText("Hex preview · first 1 KiB");
  expect(preview).toHaveTextContent("07".repeat(1024));
  expect(preview.textContent).toHaveLength(2048);
  expect(reads).toEqual([`/api/v1/blocks/${block}/blobs/42/content`]);
  await user.click(screen.getByRole("button", { name: "Hide preview" }));
  expect(preview).not.toBeVisible();
  expect(reads).toHaveLength(1);
});

it.each(["unavailable", "pending", "failed", "disabled"] as const)(
  "retains %s metadata without content actions",
  async (state) => {
    renderBlobs({ index: "42", metadata: { ...data, state } });
    expect(await screen.findByText(versionedHash)).toBeVisible();
    expect(screen.getByRole("status")).toBeVisible();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  },
);

it("keeps content errors readable and allows retry without exposing a partial preview", async () => {
  let attempt = 0;
  renderBlobs({
    index: "42",
    content: async () => new Response(new Uint8Array(++attempt === 1 ? 16 : 131072).fill(7)),
  });
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Preview first 1 KiB" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Blob storage is unavailable");
  expect(screen.queryByLabelText("Hex preview · first 1 KiB")).not.toBeVisible();
  await user.click(screen.getByRole("button", { name: "Preview first 1 KiB" }));
  expect(await screen.findByRole("button", { name: "Hide preview" })).toBeEnabled();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("filters by transaction without changing the block-scoped blob index", async () => {
  const reads = renderBlobs({
    tx: transaction.toUpperCase(),
    metadata: {
      ...data,
      items: [...data.items, { ...data.items[0]!, index: "43", transaction_hash: block }],
    },
  });
  const link = await screen.findByRole("link", { name: "View Blob 42" });
  expect(link).toHaveAttribute("href", `/blocks/${block}/blobs/42`);
  expect(screen.getAllByRole("listitem")).toHaveLength(1);
  expect(screen.queryByText(transaction)).not.toBeInTheDocument();
  expect(reads).toEqual([]);
});

it("shows a clear empty state", async () => {
  renderBlobs({ metadata: { ...data, items: [] } });
  expect(await screen.findByText("No blobs to display.")).toBeVisible();
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
});

it("retains transaction hashes when archive collection has no items", async () => {
  renderBlobs({
    metadata: { ...data, state: "pending", items: [] },
    hashes: [versionedHash],
    tx: transaction,
  });
  expect(await screen.findByText("Awaiting collection")).toBeVisible();
  expect(screen.getByText(versionedHash)).toBeVisible();
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
});

it("does not substitute another blob for an unknown index", async () => {
  const reads = renderBlobs({ index: "0" });
  expect(await screen.findByText("This block has no blob at this index.")).toBeVisible();
  expect(screen.queryByText(versionedHash)).not.toBeInTheDocument();
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
  expect(reads).toEqual([]);
});

it.each(["en", "zh"])(
  "saves full cached content from the expanded detail icon (%s)",
  async (language) => {
    await i18n.changeLanguage(language);
    const create = vi.fn((_blob: Blob) => "blob:test");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: create, revokeObjectURL: vi.fn() }));
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const reads = renderBlobs({ index: "42" });
    const user = userEvent.setup();
    const label = i18n.t("consensus.download");
    await user.click(await screen.findByRole("button", { name: i18n.t("consensus.preview") }));
    const download = screen.getByRole("button", { name: label });
    expect(download).toHaveAttribute("title", label);
    expect(download).toHaveTextContent("");
    expect(download.closest(".blob-preview")).toBeVisible();
    await user.click(download);
    expect(create).toHaveBeenCalledOnce();
    expect(create.mock.calls[0]![0].size).toBe(131072);
    expect(click.mock.instances[0]).toHaveAttribute("download", "blob-9007199254740993-42.bin");
    await user.click(screen.getByRole("button", { name: i18n.t("consensus.hidePreview") }));
    expect(screen.queryByRole("button", { name: label })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: i18n.t("consensus.preview") }));
    expect(screen.getByRole("button", { name: label })).toBeVisible();
    expect(reads).toHaveLength(1);
    click.mockRestore();
  },
);

it("discards cached bytes when the session changes", async () => {
  const fetch = vi.fn(
    async () =>
      new Response(new Uint8Array(131072).fill(7), {
        headers: { "Content-Disposition": "attachment; filename=blob-1-42.bin" },
      }),
  );
  vi.stubGlobal("fetch", fetch);
  const view = render(<BlobActions hash={block} index="42" />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Preview first 1 KiB" }));
  expect(screen.getByLabelText("Hex preview · first 1 KiB")).toBeVisible();
  authState.session.expires_at = "two";
  view.rerender(<BlobActions hash={block} index="42" />);
  expect(screen.getByLabelText("Hex preview · first 1 KiB")).not.toBeVisible();
  await user.click(screen.getByRole("button", { name: "Preview first 1 KiB" }));
  expect(fetch).toHaveBeenCalledTimes(2);
});
it("aborts an in-flight request and ignores its bytes after logout", async () => {
  let finish!: (response: Response) => void;
  let signal: AbortSignal | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn((_request: unknown, options: RequestInit) => {
      signal = options.signal ?? undefined;
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    }),
  );
  const view = render(<BlobActions hash={block} index="42" />);
  await userEvent.setup().click(screen.getByRole("button", { name: "Preview first 1 KiB" }));
  authState.session.authenticated = false;
  view.rerender(<BlobActions hash={block} index="42" />);
  expect(signal?.aborted).toBe(true);
  await act(async () => {
    finish(
      new Response(new Uint8Array(131072), {
        headers: { "Content-Disposition": "attachment; filename=blob-1-42.bin" },
      }),
    );
  });
  expect(screen.queryByLabelText("Hex preview · first 1 KiB")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Sign in" })).toBeVisible();
});
