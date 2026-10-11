import * as React from "react";
import { Suspense } from "react";
import { act, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { lazyRouteComponent } from "./lazyRouteComponent";

it("resumes a delayed route import without conditional use diagnostics", async () => {
  const errors = vi.spyOn(console, "error");
  let resolve!: (module: { Page: () => React.JSX.Element }) => void;
  const promise = new Promise<{ Page: () => React.JSX.Element }>((done) => {
    resolve = done;
  });
  const Page = lazyRouteComponent(() => promise, "Page");
  await act(async () => {
    render(
      <Suspense fallback={<p>Loading</p>}>
        <Page />
      </Suspense>,
    );
  });
  expect(screen.getByText("Loading")).toBeVisible();
  await act(async () => {
    resolve({ Page: () => <h1>Loaded</h1> });
    await promise;
  });
  expect(await screen.findByRole("heading", { name: "Loaded" })).toBeVisible();
  expect(errors).not.toHaveBeenCalled();
});

it("resumes a cold import with the browser scheduler without use diagnostics", async () => {
  const { createRoot } = await import("react-dom/client");
  // Exercise the browser scheduler: act can hide React's suspended-retry warning.
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", false);
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  const errors = vi.spyOn(console, "error");
  let resolve!: (module: { Page: () => React.JSX.Element }) => void;
  const promise = new Promise<{ Page: () => React.JSX.Element }>((done) => {
    resolve = done;
  });
  const Page = lazyRouteComponent(() => promise, "Page");
  try {
    root.render(
      <Suspense fallback={<p>Loading</p>}>
        <Page />
      </Suspense>,
    );
    await vi.waitFor(() => expect(container.textContent).toBe("Loading"));
    resolve({ Page: () => <h1>Loaded</h1> });
    await vi.waitFor(() => expect(container.textContent).toBe("Loaded"));
    expect(errors).not.toHaveBeenCalled();
  } finally {
    root.unmount();
    container.remove();
    vi.unstubAllGlobals();
  }
});

it("reuses router preloads and forwards page props without importing twice", async () => {
  const importer = vi.fn(async () => ({ Page: ({ name }: { name: string }) => <h1>{name}</h1> }));
  const Page = lazyRouteComponent(importer, "Page");
  const first = Page.preload?.();
  expect(Page.preload?.()).toBe(first);
  await first;
  expect(Page.preload).toBeUndefined();
  const errors = vi.spyOn(console, "error");
  await act(async () => {
    render(
      <Suspense fallback={<p>Loading</p>}>
        <Page name="Preloaded" />
      </Suspense>,
    );
  });
  expect(screen.getByRole("heading", { name: "Preloaded" })).toBeVisible();
  expect(importer).toHaveBeenCalledTimes(1);
  expect(errors).not.toHaveBeenCalled();
});

it("preserves failed-preload retry before rendering", async () => {
  const importer = vi
    .fn<() => Promise<{ Page: () => React.JSX.Element }>>()
    .mockRejectedValueOnce(new Error("Temporary import failure"))
    .mockResolvedValueOnce({ Page: () => <h1>Retried</h1> });
  const Page = lazyRouteComponent(importer, "Page");
  await Page.preload?.();
  expect(Page.preload).toBeTypeOf("function");
  await Page.preload?.();
  const errors = vi.spyOn(console, "error");
  await act(async () => {
    render(
      <Suspense fallback={<p>Loading</p>}>
        <Page />
      </Suspense>,
    );
  });
  expect(screen.getByRole("heading", { name: "Retried" })).toBeVisible();
  expect(importer).toHaveBeenCalledTimes(2);
  expect(errors).not.toHaveBeenCalled();
});

it("delivers import failures to the error boundary", async () => {
  const failure = new Error("Route import failed");
  const Page = lazyRouteComponent(async (): Promise<{ default: () => React.JSX.Element }> => {
    throw failure;
  }, "default");
  const caught: unknown[] = [];
  class Boundary extends React.Component<{ children: React.ReactNode }, { failed: boolean }> {
    state = { failed: false };
    static getDerivedStateFromError() {
      return { failed: true };
    }
    render() {
      return this.state.failed ? <p>Failed to load</p> : this.props.children;
    }
  }
  const errors = vi.spyOn(console, "error");
  await act(async () => {
    render(
      <Boundary>
        <Suspense fallback={<p>Loading</p>}>
          <Page />
        </Suspense>
      </Boundary>,
      {
        onCaughtError: (error) => {
          caught.push(error);
        },
      },
    );
  });
  expect(screen.getByText("Failed to load")).toBeVisible();
  expect(caught).toEqual([failure]);
  expect(errors).not.toHaveBeenCalled();
});
