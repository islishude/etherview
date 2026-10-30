import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { QueryNotice } from "@/components/QueryNotice";
import { ApiError } from "@/api/client";
import i18n from "@/i18n";
it.each(["en", "zh"])(
  "distinguishes an unindexed name from confirmed absence in %s",
  async (language) => {
    await i18n.changeLanguage(language);
    render(
      <QueryNotice
        error={
          new ApiError(503, {
            error: {
              code: "capability_unavailable",
              message: "unavailable",
              request_id: "test",
              details: { capability: "name", state: "not_indexed", code: "not_indexed" },
            },
          })
        }
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      language === "en" ? "no current verified publication" : "暂无有效的已验证发布结果",
    );
    await i18n.changeLanguage("en");
  },
);
