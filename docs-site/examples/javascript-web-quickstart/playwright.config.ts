import { defineConfig } from "@playwright/test";

function loopbackURL(name: string): URL {
  const value = process.env[name];
  if (!value) throw new Error(name + " must be supplied by the Go E2E harness");
  const url = new URL(value);
  if (url.protocol !== "http:" || url.hostname !== "127.0.0.1") {
    throw new Error(name + " must name the harness loopback HTTP listener");
  }
  return url;
}

const ui = loopbackURL("WK_DOCS_QUICKSTART_E2E_UI_URL");
const product = loopbackURL("WK_DOCS_QUICKSTART_E2E_PRODUCT_HTTP_URL");
loopbackURL("WK_DOCS_SITE_E2E_URL");
const outputDir = process.env.WK_DOCS_QUICKSTART_E2E_OUTPUT_DIR;
if (!outputDir) throw new Error("The Go harness must supply a bounded artifact directory");

export default defineConfig({
  testDir: "./e2e",
  outputDir,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  workers: 1,
  retries: 0,
  reporter: "line",
  use: {
    browserName: "chromium",
    baseURL: ui.origin,
    headless: true,
    screenshot: "off",
    trace: "off",
    video: "off",
  },
  webServer: {
    command: "node dist/server.mjs",
    url: ui.origin,
    timeout: 15_000,
    reuseExistingServer: false,
    env: {
      WK_DOCS_QUICKSTART_HOST: ui.hostname,
      WK_DOCS_QUICKSTART_PORT: ui.port,
      WK_DOCS_QUICKSTART_PRODUCT_HTTP_URL: product.origin,
    },
  },
});
