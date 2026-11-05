import test from "node:test";
import assert from "node:assert/strict";
import { createOCRApp, HISTORY_LIMIT } from "./app.js";
import { readOCRResult } from "./result.js";
import { TOKEN_KEY } from "../shared/credential.js";

import { result, setup, imageFile, response } from "./test-support.js";

test("AVIF upload and separate input/history preview lifetimes", async () => {
  const { app, requests, revoked } = setup();
  await app.selectFile(imageFile()); const input = app.preview;
  assert.equal(requests[0].url, "/api/image/ocr");
  assert.equal(requests[0].options.headers.Authorization, "Bearer test-token");
  assert.equal(requests[0].options.body.get("file").name, "image.avif");
  assert.equal(app.result.text, result.text);
  const preview = app.active.preview;
  app.clearFile(); assert.ok(revoked.includes(input)); assert.ok(!revoked.includes(preview));
  app.clearHistory(); assert.ok(revoked.includes(preview));
  app.destroy();
});

test("paste automatically recognizes images without intercepting text", async () => {
  const { app, requests } = setup(); let prevented = false;
  app.onPaste({ clipboardData: { items: [{ kind: "string", type: "text/plain" }] }, preventDefault: () => { prevented = true; } });
  assert.equal(prevented, false);
  await app.onPaste({ clipboardData: { items: [{ kind: "file", type: "image/avif", getAsFile: imageFile }] }, preventDefault: () => { prevented = true; } });
  assert.equal(prevented, true); assert.equal(app.fileKind, "paste"); assert.equal(requests.length, 1);
  assert.equal(app.active.kind, "paste"); app.destroy();
});

test("URL validation and JSON submission", async () => {
  const { app, requests } = setup(); app.selectMode("url"); app.url = "file:///private/image.png";
  await app.recognize(); assert.equal(requests.length, 0);
  app.url = "https://example.com/image.png"; await app.recognize();
  assert.deepEqual(JSON.parse(requests[0].options.body), { url: app.url });
  assert.equal(app.active.preview, app.url); app.destroy();
});

test("history stays in memory across requests; only the shared credential persists", async () => {
  const { app, browser, saved } = setup(); await app.selectFile(imageFile());
  assert.deepEqual([...saved.keys()], [TOKEN_KEY]);
  const fresh = createOCRApp(browser); fresh.$el = { dataset: {} }; fresh.init();
  assert.equal(fresh.token, "test-token"); assert.deepEqual(fresh.history, []);
  app.destroy(); fresh.destroy();
});

test("history evicts and releases the oldest previews", async () => {
  const { app, revoked } = setup(); await app.selectFile(imageFile()); const first = app.active;
  for (let i = 0; i < HISTORY_LIMIT; i++) await app.recognize();
  assert.equal(app.history.length, HISTORY_LIMIT); assert.ok(revoked.includes(first.preview));
  const latest = app.activeID; app.showEntry(app.history.at(-1).id); assert.notEqual(app.activeID, latest);
  app.destroy();
});

test("cancellation is recorded, destruction suppresses late results and frees previews", async () => {
  const { app, revoked } = setup({ fetch: (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener("abort", () => reject(new DOMException("canceled", "AbortError")), { once: true });
  }) });
  const run = app.selectFile(imageFile()); app.cancel(); await run;
  assert.equal(app.history.length, 1); assert.match(app.active.error, /取消/);
  const second = app.recognize(); app.destroy(); await second;
  assert.equal(app.history.length, 1); assert.equal(new Set(revoked).size, 3);
});

test("input validation, empty OCR and malformed output coordinates", async () => {
  const { app } = setup({ fetch: async () => response({ text: "", width: 32, height: 32, lines: [] }) });
  app.selectFile(new File(["x"], "script.svg", { type: "image/svg+xml" }));
  assert.equal(app.preview, ""); assert.match(app.error, /JPEG/);
  app.token = ""; await app.selectFile(imageFile()); assert.equal(app.accessOpen, true);
  app.token = "test-token"; await app.recognize(); assert.equal(app.result.text, ""); assert.deepEqual(app.result.lines, []);
  assert.throws(() => readOCRResult({ ...result, lines: [{ ...result.lines[0], box: [{ x: NaN, y: 0 }, ...result.lines[0].box.slice(1)] }] }));
  app.destroy();
});

test("authentication failure keeps history and opens access settings", async () => {
  const { app } = setup({ fetch: async () => ({ ok: false, status: 403, json: async () => ({ message: "unauthorized" }) }) });
  await app.selectFile(imageFile());
  assert.equal(app.accessOpen, true); assert.match(app.active.error, /Token/); assert.equal(app.busy, false); app.destroy();
});

test("timed-out requests leave a retryable history entry", async () => {
  let expire;
  const { app } = setup({ setTimeout: (callback) => { expire = callback; return 1; }, clearTimeout: () => {},
    fetch: (_url, options) => new Promise((_resolve, reject) => options.signal.addEventListener("abort", () => reject(new DOMException("timeout", "AbortError")), { once: true })),
  });
  const run = app.selectFile(imageFile()); expire(); await run;
  assert.equal(app.busy, false); assert.match(app.active.error, /超时/); app.destroy();
});
