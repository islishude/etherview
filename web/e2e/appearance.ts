import type { Page } from "@playwright/test";

export const appearanceCases = [390, 768, 1440].flatMap((width) =>
  (["en", "zh"] as const).flatMap((language) =>
    (["light", "dark"] as const).map((theme) => ({ width, language, theme })),
  ),
);

export async function configureAppearance(
  page: Page,
  appearance: (typeof appearanceCases)[number],
) {
  const { width, language, theme } = appearance;
  await page.setViewportSize({ width, height: 1000 });
  await page.addInitScript(
    ({ language, theme }) => {
      localStorage.setItem("etherview.language", language);
      localStorage.setItem("etherview.theme", theme);
    },
    { language, theme },
  );
}

export function collectBrowserErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (/content security policy|violates.*directive/i.test(message.text())) {
      errors.push(message.text());
    }
  });
  return errors;
}
