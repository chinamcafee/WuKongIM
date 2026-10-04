import { expect, test, type FrameLocator } from "@playwright/test";

test.afterEach(async ({ page }, info) => {
  if (info.status === info.expectedStatus) return;
  // Keep only one redacted screenshot; the Go owner applies the byte/count bounds.
  await page.evaluate(() => {
    document.querySelectorAll("iframe").forEach((frame) => frame.remove());
    document.querySelectorAll("input").forEach((input) => { input.value = ""; });
    document.querySelectorAll('[data-testid="event-log"], [data-testid="ui-error"]').forEach((node) => { node.textContent = "[REDACTED]"; });
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) {
      const node = walker.currentNode;
      node.textContent = (node.textContent ?? "").replace(/alice|bob|docs-dev-[A-Za-z0-9_-]+/gi, "[REDACTED]");
    }
  });
  const body = await page.locator("body").innerText();
  if (/alice|bob|docs-dev-/i.test(body)) return;
  await page.screenshot({ path: info.outputPath("failure.png"), timeout: 2_000 }).catch(() => {});
});

test("Alice and Bob exchange durable messages and recover an offline message", async ({ page }) => {
  const pageErrors: string[] = [];
  const productRequests: string[] = [];
  const socketURLs: string[] = [];
  const discoveredURLs: Promise<string>[] = [];
  const productOrigin = new URL(process.env.WK_DOCS_QUICKSTART_E2E_PRODUCT_HTTP_URL!).origin;
  page.on("pageerror", (error) => pageErrors.push(error.name));
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.origin === productOrigin) productRequests.push(url.pathname);
  });
  page.on("websocket", (socket) => socketURLs.push(socket.url()));
  page.on("response", (response) => {
    if (new URL(response.url()).pathname === "/api/development/identity") {
      // Retain only the discovered route; never put issued credentials in artifacts.
      discoveredURLs.push(response.json().then((body) => String(body.websocketUrl)));
    }
  });

  const docs = process.env.WK_DOCS_SITE_E2E_URL!;
  for (const locale of ["en", "zh"]) {
    const response = await page.goto(docs + "/" + locale + "/sdk/javascript/quickstart");
    expect(response?.status()).toBe(200);
    await expect(page.getByRole("heading", { level: 1 })).toContainText("JavaScript");
  }

  await page.goto("/");
  const alice = page.frameLocator('[data-testid="alice-frame"]');
  const bob = page.frameLocator('[data-testid="bob-frame"]');
  for (const session of [alice, bob]) {
    await session.getByTestId("connect-button").click();
    await expect(session.getByTestId("connection-status")).toHaveAttribute("data-state", "connected");
  }

  async function send(session: FrameLocator, text: string, ackCount: number) {
    await session.getByTestId("message-input").fill(text);
    await session.getByTestId("send-button").click();
    const acks = session.getByTestId("event-sendack");
    await expect(acks).toHaveCount(ackCount);
    await expect(acks.last()).toContainText(/SENDACK success · seq [1-9][0-9]*/);
  }

  await send(alice, "alice-online", 1);
  await expect(bob.getByTestId("event-received")).toContainText("alice-online");
  await send(bob, "bob-online", 1);
  await expect(alice.getByTestId("event-received")).toContainText("bob-online");

  await bob.getByTestId("disconnect-button").click();
  await expect(bob.getByTestId("connection-status")).toHaveAttribute("data-state", "disconnected");
  await send(alice, "alice-offline", 2);
  await expect(bob.getByTestId("event-received")).toHaveCount(1);
  const syncResponse = page.waitForResponse((response) =>
    new URL(response.url()).pathname === "/api/messages/sync" && response.request().method() === "POST",
  );
  await bob.getByTestId("reconnect-sync-button").click();
  await expect(bob.getByTestId("connection-status")).toHaveAttribute("data-state", "connected");
  const sync = await syncResponse;
  expect(sync.status()).toBe(200);
  const history = await sync.json() as { messages: Array<{ fromUid: string; payload: string }> };
  const offlineHistory = history.messages.filter((message) =>
    message.fromUid === "alice" &&
    JSON.parse(Buffer.from(message.payload, "base64").toString("utf8")).content === "alice-offline",
  );
  expect(offlineHistory).toHaveLength(1);
  await expect(bob.getByTestId("event-status").last()).toContainText("Sync complete");
  // Reconnect delivery may beat the history response; the session deduplicates both.
  const recovered = bob.locator('[data-testid="event-received"], [data-testid="event-synced"]')
    .filter({ hasText: "alice-offline" });
  await expect(recovered).toHaveCount(1);

  const routes = await Promise.all(discoveredURLs);
  expect(routes).toHaveLength(3);
  expect(socketURLs).toHaveLength(3);
  expect(new Set(socketURLs)).toEqual(new Set(routes));
  expect(productRequests).toEqual([]);
  expect(pageErrors).toEqual([]);
  for (const session of [alice, bob]) {
    await expect(session.getByTestId("ui-error")).toBeHidden();
    await session.getByTestId("disconnect-button").click();
    await expect(session.getByTestId("connection-status")).toHaveAttribute("data-state", "disconnected");
  }
});
