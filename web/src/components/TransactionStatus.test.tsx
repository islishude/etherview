import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  TransactionStatus,
  type TransactionVisualStatus,
} from "./TransactionStatus";

const cases: Array<{
  status: TransactionVisualStatus;
  label: string;
  icon: string;
}> = [
  { status: "success", label: "Success", icon: "lucide-circle-check" },
  { status: "failed", label: "Failed", icon: "lucide-circle-x" },
  { status: "pending", label: "Pending", icon: "lucide-clock-3" },
  { status: "replaced", label: "Replaced", icon: "lucide-arrow-right-left" },
  { status: "orphan", label: "Orphaned", icon: "lucide-git-branch" },
  { status: "unknown", label: "Unknown", icon: "lucide-circle-question-mark" },
];

describe("TransactionStatus", () => {
  it.each(cases)("renders $status with visible text and an auxiliary icon", ({
    status,
    label,
    icon,
  }) => {
    const { container } = render(<TransactionStatus label={label} status={status} />);

    const statusElement = screen.getByText(label).closest(".transaction-status");
    expect(statusElement).toHaveAttribute("data-status", status);
    expect(statusElement).toHaveClass(status);
    const svg = container.querySelector(`svg.${icon}`);
    expect(svg).not.toBeNull();
    expect(svg).toHaveAttribute("aria-hidden", "true");
  });
});
