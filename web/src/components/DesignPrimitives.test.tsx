import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AppFrame, Surface } from "./DesignPrimitives";

describe("design primitives", () => {
  it("composes the responsive Tailwind layout contract without hiding caller classes", () => {
    render(
      <AppFrame className="app-frame">
        <Surface className="min-h-[180px] max-[700px]:items-start" aria-label="capability">
          content
        </Surface>
      </AppFrame>,
    );

    const surface = screen.getByRole("region", { name: "capability" });
    const frame = surface.parentElement;
    expect(frame).toHaveClass(
      "min-h-screen",
      "text-ui-text",
      "flex",
      "flex-col",
      "dark:[color-scheme:dark]",
      "app-frame",
    );

    expect(surface.tagName).toBe("SECTION");
    expect(surface).toHaveClass(
      "rounded-ui-md",
      "border",
      "border-ui-line",
      "bg-ui-surface",
      "min-h-[180px]",
      "max-[700px]:items-start",
    );
  });
});
