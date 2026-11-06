import assert from "node:assert/strict";
import test from "node:test";
import { createImageFilterApp } from "./app.js";
import { deferredRequests, flush, setup } from "./test-support.js";

const routes = [
  ["default", "/api/image/filter"],
  ["libvips", "/api/image/filter/libvips"],
  ["go", "/api/image/filter/go"],
];

test("URL and file submissions use only the selected authenticated API route", async () => {
  for (const [backend, path] of routes) {
    const { app, requests } = setup();
    app.backend = backend;
    await app.selectFile(new File(["image"], "sample.jpeg", { type: "image/jpeg" }));
    app.mode = "url"; app.url = "https://example.com/sample.jpeg";
    await app.detect();
    assert.deepEqual(requests.map((request) => request.url), [path, path]);
    assert.equal(requests[0].options.body.get("file").name, "sample.jpeg");
    assert.deepEqual(JSON.parse(requests[1].options.body), { url: app.url });
    assert.ok(requests.every((request) => request.options.headers.Authorization === "Bearer test-token"));
    assert.ok(app.history.every((entry) => entry.backend === backend));
    app.destroy();
  }
});

test("switching routes automatically retests the current image without losing comparison history", async () => {
  const { app, requests, browser } = setup();
  await app.selectFile(new File(["same image"], "sample.png", { type: "image/png" }));
  app.backend = "go"; await app.onBackendChange();
  app.backend = "libvips"; await app.onBackendChange();
  assert.deepEqual(requests.map((request) => request.url), [
    "/api/image/filter", "/api/image/filter/go", "/api/image/filter/libvips",
  ]);
  assert.deepEqual(app.history.map((entry) => entry.backend), ["libvips", "go", "default"]);
  app.showEntry(app.history[1].id);
  assert.equal(app.active.backend, "go");
  assert.equal(app.backend, "libvips");
  const refreshed = createImageFilterApp(browser);
  refreshed.$el = { dataset: {} }; refreshed.init();
  assert.equal(refreshed.backend, "libvips");
  assert.deepEqual(refreshed.history.map((entry) => entry.backend), ["libvips", "go", "default"]);
  app.destroy(); refreshed.destroy();
});

test("request history retains the captured route when a later selection is queued", async () => {
  const pending = deferredRequests();
  const { app } = setup({ fetch: pending.fetch });
  app.backend = "go";
  const first = app.selectFile(new File(["image"], "sample.png", { type: "image/png" }));
  await flush();
  app.backend = "libvips"; await app.onBackendChange();
  assert.equal(pending.requests.length, 1);
  assert.equal(pending.requests[0].url, "/api/image/filter/go");
  pending.requests[0].resolve(); await first; await flush();
  assert.equal(pending.requests.length, 2);
  assert.equal(pending.requests[1].url, "/api/image/filter/libvips");
  assert.equal(app.history[0].backend, "go");
  pending.requests[1].resolve(); await flush();
  assert.deepEqual(app.history.map((entry) => entry.backend), ["libvips", "go"]);
  app.destroy();
});

test("an unknown route cannot send the API credential to another endpoint", async () => {
  const { app, requests } = setup();
  app.backend = "https://example.com/collect";
  app.mode = "url"; app.url = "https://example.com/sample.jpeg";
  await app.detect();
  assert.equal(requests.length, 0);
  assert.equal(app.history.length, 0);
  assert.match(app.error, /检测路径/);
  app.destroy();
});
