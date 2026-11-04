import assert from "node:assert/strict";
import test from "node:test";
import { createImageFilterApp } from "./app.js";
import { HISTORY_KEY } from "./history.js";
import { TOKEN_KEY } from "./credential.js";
import { setup } from "./test-support.js";
test("URL classification persists token separately from session history and restores both", async () => {
  const { app, requests, browser, saved, localSaved } = setup();
  app.mode = "url"; app.url = "https://example.com/image.png";
  await app.detect();
  assert.equal(requests[0].url, "/api/image/filter");
  assert.equal(requests[0].options.headers.Authorization, "Bearer test-token");
  assert.deepEqual(JSON.parse(requests[0].options.body), { url: app.url });
  assert.equal(app.result.label, "1_valid");
  assert.equal(app.history.length, 1);
  assert.doesNotMatch(saved.get(HISTORY_KEY), /test-token/);
  assert.equal(JSON.parse(localSaved.get(TOKEN_KEY)).token, "test-token");
  const refreshed = createImageFilterApp(browser);
  refreshed.$el = { dataset: {} }; refreshed.init();
  assert.equal(refreshed.history.length, 1);
  assert.equal(refreshed.token, "test-token");
  app.destroy(); refreshed.destroy();
});
test("clipboard images upload once and release their object URLs", async () => {
  const { app, requests, revoked } = setup();
  let prevented = false;
  const file = new File(["image"], "paste.png", { type: "image/png" });
  await app.onPaste({ clipboardData: { items: [{ kind: "file", type: file.type, getAsFile: () => file }] }, preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].options.body.get("file").name, "paste.png");
  assert.equal(app.history[0].kind, "paste");
  app.clearFile(); app.destroy();
  assert.deepEqual(revoked, ["blob:test"]);
});

test("all four image formats and unreported MIME types can be uploaded", async () => {
  const cases = [
    ["image.jpeg", "image/jpeg"], ["image.jpg", "image/jpeg"],
    ["image.webp", "image/webp"], ["image.avif", "image/avif"], ["image.png", "image/png"],
    ["image.AVIF", ""], ["image.avif", "application/octet-stream"],
  ];
  for (const [name, type] of cases) {
    const { app, requests } = setup();
    await app.selectFile(new File(["image"], name, { type }));
    assert.equal(app.canSubmit, true, name);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].options.body.get("file").name, name);
    assert.equal(app.result.label, "1_valid");
    app.destroy();
  }
});
test("invalid URLs, missing tokens and oversized files do not start inference", async () => {
  const { app, requests } = setup();
  app.mode = "url"; app.url = "file:///etc/passwd";
  await app.detect();
  app.url = "https://example.com/a"; app.token = "";
  await app.detect();
  assert.equal(app.accessOpen, true);
  app.maxBytes = 1;
  await app.selectFile(new File(["too large"], "image.png", { type: "image/png" }));
  assert.equal(requests.length, 0);
  assert.equal(app.history.length, 0);
  app.destroy();
});
test("failed requests are visible in history and history can be cleared", async () => {
  const { app, localSaved } = setup({ fetch: async () => ({ ok: false, status: 403, json: async () => ({}) }) });
  app.mode = "url"; app.url = "https://example.com/image.png";
  await app.detect();
  assert.match(app.error, /Token/);
  assert.equal(app.history[0].result, null);
  assert.equal(app.busy, false);
  assert.equal(app.token, "");
  assert.equal(localSaved.has(TOKEN_KEY), false);
  app.clearHistory();
  assert.equal(app.history.length, 0);
  app.destroy();
});

test("expired credentials are cleared before any API request", async () => {
  const { app, requests } = setup();
  app.mode = "url"; app.url = "https://example.com/image.png";
  app.tokenExpiresAt = Date.now() - 1;
  await app.detect();
  assert.equal(requests.length, 0);
  assert.equal(app.token, "");
  assert.match(app.error, /过期/);
  app.destroy();
});

test("blocked localStorage preserves successful detection in memory", async () => {
  const { app } = setup({ localStorage: { getItem() { throw new Error("disabled"); }, setItem() { throw new Error("disabled"); }, removeItem() {} } });
  app.mode = "url"; app.url = "https://example.com/image.png";
  await app.detect();
  assert.equal(app.result.label, "1_valid");
  assert.equal(app.error, "");
  assert.match(app.credentialNotice, /无法保存/);
  app.destroy();
});

test("clearing history keeps the saved credential and clearing token keeps history", async () => {
  const { app, localSaved } = setup();
  app.mode = "url"; app.url = "https://example.com/image.png";
  await app.detect();
  app.clearHistory();
  assert.equal(localSaved.has(TOKEN_KEY), true);
  await app.detect();
  app.forgetToken();
  assert.equal(localSaved.has(TOKEN_KEY), false);
  assert.equal(app.history.length, 1);
  app.destroy();
});
test("concurrent submissions coalesce at the page and cancellation stops the request", async () => {
  let calls = 0;
  const { app } = setup({ fetch: (_url, { signal }) => new Promise((_resolve, reject) => {
    calls++; signal.addEventListener("abort", () => reject(new DOMException("canceled", "AbortError")), { once: true });
  }) });
  app.mode = "url"; app.url = "https://example.com/image.png";
  const pending = app.detect();
  await app.detect();
  assert.equal(calls, 1);
  app.cancel(); await pending;
  assert.equal(app.busy, false);
  assert.match(app.history[0].error, /取消/);
  app.destroy();
});

test("DNS and download failures give specific hints without clearing valid credentials", async () => {
  for (const [code, message] of [["image_url_dns_failed", /DNS/], [undefined, /无法下载图片/]]) {
    const { app } = setup({ fetch: async () => ({ ok: false, status: 502, json: async () => ({ status: 502, data: { code } }) }) });
    app.mode = "url"; app.url = "https://example.com/image.png";
    await app.detect();
    assert.match(app.error, message);
    assert.equal(app.token, "test-token");
    assert.equal(app.history.length, 1);
    app.destroy();
  }
});
