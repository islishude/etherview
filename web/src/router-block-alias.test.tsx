import { createMemoryHistory } from "@tanstack/react-router";
import { waitFor } from "@testing-library/react";
import { expect, it } from "vitest";

import { makeRouter } from "./router";

it.each(["123", "0", "9007199254740993", "18446744073709551615", "invalid", "999999"])(
  "redirects block identifier %s unchanged and replaces its history entry",
  async (identifier) => {
    const history = createMemoryHistory({
      initialEntries: ["/blocks", `/block/${identifier}`],
    });
    const router = makeRouter(history);
    await router.load();
    await waitFor(() => expect(router.state.location.pathname).toBe(`/blocks/${identifier}`));
    expect(history.length).toBe(2);
    history.back();
    expect(history.location.pathname).toBe("/blocks");
  },
);

it.each(["overview", "transactions", "withdrawals", "consensus", "requests", "blobs"])(
  "preserves the %s tab and URL fragment",
  async (tab) => {
    const history = createMemoryHistory({
      initialEntries: [`/block/123?tab=${tab}#main-content`],
    });
    const router = makeRouter(history);
    await router.load();
    await waitFor(() =>
      expect(router.state.location.href).toBe(`/blocks/123?tab=${tab}#main-content`),
    );
  },
);

it("uses the existing block search validation for invalid tabs", async () => {
  const router = makeRouter(createMemoryHistory({ initialEntries: ["/block/123?tab=invalid"] }));
  await router.load();
  await waitFor(() => expect(router.state.location.href).toBe("/blocks/123"));
});

it.each(["123", `0x${"11".repeat(32)}`])("preserves the existing /blocks/%s route", async (id) => {
  const path = `/blocks/${id}?tab=transactions`;
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  await router.load();
  expect(router.state.location.href).toBe(path);
  expect(router.state.matches.at(-1)?.routeId).toBe("/blocks/$blockID");
});
