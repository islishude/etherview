import { defineConfig, devices } from "@playwright/test";

const useBundledChromium = process.env.PLAYWRIGHT_USE_BUNDLED === "1";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: process.env.CI ? [["line"], ["html", { open: "never" }]] : "line",
  use: {
    baseURL: "http://127.0.0.1:4173",
    channel: useBundledChromium ? undefined : "chrome",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    command: "go run ./e2e/server",
    cwd: ".",
    url: "http://127.0.0.1:4173/health/live",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
