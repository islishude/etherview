import { withVerificationSession, VerificationTestSession } from "../../test/verificationSession";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import "@/i18n/resources/sourcify";
import { textFile, uploadFiles } from "@/test/verificationUpload";
import { VerifyPage } from "./VerifyPage";
import { MAX_UPLOAD_BYTES, useVerificationFiles } from "./verificationFiles";

const address = `0x${"12".repeat(20)}`;
async function setup() {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const data =
      path === "/api/v1/config"
        ? { chain_id: "1", features: { verification: true } }
        : path.includes("/compilers")
          ? { language: "solidity", versions: ["0.8.30"] }
          : { id: "123e4567-e89b-42d3-a456-426614174000", status: "queued", kind: "address" };
    void init;
    return Response.json({ data, meta: { chain_id: "1", request_id: "upload-test" } });
  });
  vi.stubGlobal("fetch", withVerificationSession(fetcher));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <VerificationTestSession>
        <VerifyPage initialAddress={address} />
      </VerificationTestSession>
    </QueryClientProvider>,
  );
  await screen.findByRole("option", { name: "0.8.30" });
  return { fetcher, ...view };
}
const posts = (fetcher: Awaited<ReturnType<typeof setup>>["fetcher"]) =>
  fetcher.mock.calls.filter(([, init]) => init?.method === "POST");
const submit = () =>
  userEvent.setup().click(screen.getByRole("button", { name: "Submit verification" }));
beforeEach(async () => {
  await i18n.changeLanguage("en");
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("verification uploads", () => {
  it.each([
    ["solidity", "A.sol", "contract A {}", ".sol"],
    ["yul", "A.yul", 'object "A" { code { stop() } }', ".yul"],
    ["vyper", "A.vy", "@external\ndef value() -> uint256:\n    return 42\n", ".vy,.vyi"],
    ["geas", "main.eas", "push 1", ".eas"],
  ])("submits %s source content under the edited path", async (language, name, content, accept) => {
    const { fetcher } = await setup();
    fireEvent.change(screen.getByLabelText("Language"), { target: { value: language } });
    if (language !== "geas")
      fireEvent.change(screen.getByLabelText("Input format"), { target: { value: "multipart" } });
    const input = screen.getByLabelText(language === "geas" ? "Geas source files" : "Source files");
    expect(input).toHaveAttribute("accept", accept);
    await uploadFiles(input, { [name]: content });
    fireEvent.change(screen.getByLabelText(`Source path: ${name}`), {
      target: { value: `src/${name}` },
    });
    if (language === "vyper")
      fireEvent.change(screen.getByLabelText("Target file"), { target: { value: `src/${name}` } });
    if (language === "geas")
      fireEvent.change(screen.getByLabelText("Runtime entrypoint"), {
        target: { value: `src/${name}` },
      });
    await submit();
    await waitFor(() => expect(posts(fetcher)).toHaveLength(1));
    expect(JSON.parse(String(posts(fetcher)[0]![1]?.body))).toMatchObject({
      language,
      input_kind: language === "geas" ? "geas_sources" : "multipart",
      sources: { [`src/${name}`]: content },
    });
  });

  it("uploads Vyper interface files alongside source files", async () => {
    const { fetcher } = await setup();
    fireEvent.change(screen.getByLabelText("Language"), { target: { value: "vyper" } });
    fireEvent.change(screen.getByLabelText("Input format"), { target: { value: "multipart" } });
    const sources = { "A.vy": "import I as I", "I.vyi": "@external\ndef foo(): ..." };
    await uploadFiles(screen.getByLabelText("Source files"), sources);
    await submit();
    await waitFor(() => expect(posts(fetcher)).toHaveLength(1));
    expect(JSON.parse(String(posts(fetcher)[0]![1]?.body)).sources).toEqual(sources);
  });

  it.each(["solidity", "yul", "vyper"])(
    "submits uploaded %s Standard JSON without restricting its source keys",
    async (language) => {
      const { fetcher } = await setup();
      fireEvent.change(screen.getByLabelText("Language"), { target: { value: language } });
      const input = {
        language: language === "solidity" ? "Solidity" : language === "yul" ? "Yul" : "Vyper",
        sources: { "A.custom": { content: "source bytes" } },
        settings: {},
      };
      const control = screen.getByLabelText("Standard JSON input");
      expect(control).toHaveAttribute("accept", ".json");
      expect(control).not.toHaveAttribute("multiple");
      await uploadFiles(control, { "input.json": JSON.stringify(input) });
      await submit();
      await waitFor(() => expect(posts(fetcher)).toHaveLength(1));
      expect(JSON.parse(String(posts(fetcher)[0]![1]?.body))).toMatchObject({
        language,
        input_kind: "standard_json",
        input,
      });
    },
  );

  it.each(["bad.exe", "input.JSON", "input.json.exe", "input"])(
    "rejects %s even with JSON MIME and accept filtering bypassed",
    async (name) => {
      const { fetcher } = await setup();
      const file = textFile(name, "{}", "application/json");
      await userEvent
        .setup({ applyAccept: false })
        .upload(screen.getByLabelText("Standard JSON input"), file);
      expect(await screen.findByRole("alert")).toHaveTextContent(name);
      expect(screen.getByRole("alert")).toHaveTextContent(".json");
      await submit();
      expect(posts(fetcher)).toHaveLength(0);
    },
  );

  it.each([
    ["[]", "one JSON object"],
    ["null", "one JSON object"],
    ['{"a":' + "[".repeat(258) + "0" + "]".repeat(258) + "}", "nesting limit"],
  ])("retains Standard JSON validation for uploaded input", async (content, error) => {
    const { fetcher } = await setup();
    await uploadFiles(screen.getByLabelText("Standard JSON input"), { "input.json": content });
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent(error);
    expect(posts(fetcher)).toHaveLength(0);
  });

  it("rejects serialized request overhead after a file fits the raw byte budget", async () => {
    const { fetcher } = await setup();
    const content = JSON.stringify({ x: "x".repeat(MAX_UPLOAD_BYTES - 8) });
    expect(content.length).toBe(MAX_UPLOAD_BYTES);
    await uploadFiles(screen.getByLabelText("Standard JSON input"), { "input.json": content });
    expect(screen.getByRole("button", { name: "Remove input.json" })).toBeVisible();
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent("5 MiB");
    expect(posts(fetcher)).toHaveLength(0);
  });

  it("rejects duplicate edited paths and clears selection on format or language changes", async () => {
    const { fetcher } = await setup();
    fireEvent.change(screen.getByLabelText("Input format"), { target: { value: "multipart" } });
    await uploadFiles(screen.getByLabelText("Source files"), { "A.sol": "A", "B.sol": "B" });
    fireEvent.change(screen.getByLabelText("Source path: B.sol"), { target: { value: "A.sol" } });
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent("Duplicate source path");
    expect(posts(fetcher)).toHaveLength(0);
    fireEvent.change(screen.getByLabelText("Language"), { target: { value: "yul" } });
    expect(screen.queryByLabelText("Source path: A.sol")).toBeNull();
    await uploadFiles(screen.getByLabelText("Source files"), { "A.yul": "Yul" });
    fireEvent.change(screen.getByLabelText("Input format"), { target: { value: "standard_json" } });
    expect(screen.queryByLabelText("Source path: A.yul")).toBeNull();
    await submit();
    expect(await screen.findByRole("alert")).toHaveTextContent("Select a file");
    expect(posts(fetcher)).toHaveLength(0);
  });

  it("disables submission during reads and discards reads after switching language", async () => {
    const { fetcher } = await setup();
    let resolve!: (value: string) => void;
    const pending = new Promise<string>((done) => {
      resolve = done;
    });
    const file = new File(["{}"], "input.json");
    Object.defineProperty(file, "text", { value: () => pending });
    await userEvent.setup().upload(screen.getByLabelText("Standard JSON input"), file);
    expect(screen.getByRole("button", { name: "Reading files…" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Language"), { target: { value: "vyper" } });
    await act(async () => {
      resolve("{}");
      await pending;
    });
    expect(screen.queryByRole("button", { name: "Remove input.json" })).toBeNull();
    await submit();
    expect(posts(fetcher)).toHaveLength(0);
  });
});

describe("file reading boundaries", () => {
  const setupHook = (single = false) =>
    renderHook(() => useVerificationFiles({ extensions: [".sol"], single, onChange: vi.fn() }));

  it.each([
    "../A.sol",
    "src//A.sol",
    "src/./A.sol",
    "src/../A.sol",
    "\\A.sol",
    "/A.sol",
    "bad\n.sol",
    "x".repeat(381) + ".sol",
  ])("rejects invalid path before reading: %s", async (name) => {
    const { result } = setupHook();
    const read = vi.fn(async () => "content");
    const file = new File([], name);
    Object.defineProperty(file, "text", { value: read });
    await act(() => result.current.select([file]));
    expect(result.current.error).toContain("Invalid source path");
    expect(read).not.toHaveBeenCalled();
  });

  it("rejects duplicate paths, excess count, size and single-file violations before reading", async () => {
    const { result } = setupHook();
    const read = vi.fn(async () => "");
    const file = new File([], "A.sol");
    Object.defineProperty(file, "text", { value: read });
    for (const selected of [[file, file], Array.from({ length: 1025 }, () => file)]) {
      await act(() => result.current.select(selected));
      expect(result.current.error).toBeTruthy();
    }
    Object.defineProperty(file, "size", { value: MAX_UPLOAD_BYTES + 1 });
    await act(() => result.current.select([file]));
    expect(result.current.error).toContain("5 MiB");
    expect(read).not.toHaveBeenCalled();
    const single = setupHook(true);
    await act(() => single.result.current.select([textFile("A.sol", ""), textFile("B.sol", "")]));
    expect(single.result.current.error).toContain("exactly one");
  });

  it("accepts the count boundary and retains selection after read failure or cancel", async () => {
    const { result } = setupHook();
    const files = Array.from({ length: 1024 }, (_, index) => textFile(`${index}.sol`, ""));
    await act(() => result.current.select(files));
    expect(result.current.files).toHaveLength(1024);
    const broken = new File([], "broken.sol");
    Object.defineProperty(broken, "text", {
      value: async () => {
        throw new Error("read failed");
      },
    });
    await act(() => result.current.select([broken]));
    expect(result.current.files).toHaveLength(1024);
    expect(result.current.error).toContain("previous selection was preserved");
    await act(() => result.current.select([]));
    expect(result.current.files).toHaveLength(1024);
  });

  it("discards an older read after a replacement and after unmount", async () => {
    const { result, unmount } = setupHook();
    let resolve!: (value: string) => void;
    const delayed = new Promise<string>((done) => {
      resolve = done;
    });
    const old = new File([], "old.sol");
    Object.defineProperty(old, "text", { value: () => delayed });
    let selection!: Promise<void>;
    act(() => {
      selection = result.current.select([old]);
    });
    await act(() => result.current.select([textFile("new.sol", "new")]));
    await act(async () => {
      resolve("old");
      await selection;
    });
    expect(result.current.files.map((file) => file.path)).toEqual(["new.sol"]);
    act(() => {
      selection = result.current.select([old]);
    });
    unmount();
    await act(async () => {
      await selection;
    });
  });
});
