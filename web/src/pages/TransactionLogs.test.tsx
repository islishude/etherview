import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import i18n from "@/i18n";
import type { TransactionLog } from "@/api/types";
import { TransactionLogCard } from "./TransactionLogs";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: ReactNode }) => <span>{children}</span>,
}));

const address = "0xfffffffffffffffffffffffffffffffffffffffe";
const amount = "115792089237316195423570985008687907853269984665640564039457584007913129639935";
const log: TransactionLog = {
  address,
  log_index: "0",
  topics: ["0x" + "dd".repeat(32), "0x" + "00".repeat(32), "0x" + "11".repeat(32)],
  data: "0x" + "ff".repeat(32),
  decoding: {
    status: "decoded",
    protocol: "eip7708",
    event_name: "Transfer",
    signature: "Transfer(address,address,uint256)",
    arguments: [{ name: "value", type: "uint256", indexed: false, hashed: false, value: amount }],
    candidates: [],
    attribution: { mode: "protocol", trace_path: [] },
  },
};

it.each([
  ["en", "System Address", "Protocol source", "More details"],
  ["zh", "系统地址", "协议来源", "更多详情"],
])("renders %s protocol provenance and exact amounts", async (language, label, source, details) => {
  await i18n.changeLanguage(language);
  render(
    <I18nextProvider i18n={i18n}>
      <TransactionLogCard log={log} locale={language} />
    </I18nextProvider>,
  );
  expect(screen.getByText(label)).toBeVisible();
  expect(screen.getByText(amount)).toBeVisible();
  await userEvent.setup().click(screen.getByText(details));
  expect(screen.getByRole("heading", { name: source })).toBeVisible();
  expect(screen.getByText(log.data)).toBeVisible();
  expect(document.querySelector(".transaction-log-provenance-card")).toBeNull();
  await i18n.changeLanguage("en");
});
