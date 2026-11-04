import assert from "node:assert/strict";
import test from "node:test";
import { deferredRequests, fakeClock, flush, setup } from "./test-support.js";

const image = (name = "image.png") => new File(["image"], name, { type: "image/png" });

test("file picker and drop automatically classify one image per selection", async () => {
  const { app, requests } = setup();
  const target = { files: [image("picked.png")], value: "picked.png" };
  await app.onFileInput({ target });
  assert.equal(target.value, "");
  await app.onDrop({ dataTransfer: { files: [image("dropped.png")] } });
  assert.deepEqual(requests.map((request) => request.options.body.get("file").name), ["picked.png", "dropped.png"]);
  assert.deepEqual(app.history.map((entry) => entry.name), ["dropped.png", "picked.png"]);
  app.destroy();
});

test("URL input debounces typing and duplicate events while allowing explicit retries", async () => {
  const clock = fakeClock();
  const { app, requests } = setup(clock);
  app.selectMode("url");
  app.onURLInput("https://example.com/first.png");
  clock.advance(400);
  app.onURLInput(" https://example.com/final.png ");
  clock.advance(599); await flush();
  assert.equal(requests.length, 0);
  clock.advance(1); await flush();
  assert.equal(requests.length, 1);
  assert.deepEqual(JSON.parse(requests[0].options.body), { url: "https://example.com/final.png" });
  app.onURLInput(app.url);
  clock.advance(600); await flush();
  assert.equal(requests.length, 1);
  await app.detect();
  assert.equal(requests.length, 2);
  app.destroy();
  assert.equal(clock.pending, 0);
});

test("invalid, incomplete, cleared and composing URLs never submit automatically", async () => {
  const clock = fakeClock();
  const { app, requests } = setup(clock);
  app.selectMode("url");
  for (const value of ["", "https://", "https:example.com/a", "https:/example.com/a", "https:///example.com/a", "example.com/image.png", "file:///image.png", "https://u:p@example.com/a", "https://example.com/a#fragment"]) {
    app.onURLInput(value); clock.advance(600); await flush();
  }
  app.onURLInput("https://example.com/image.png");
  app.onURLInput(""); clock.advance(600); await flush();
  app.onURLInput("https://example.com/image.png", true);
  clock.advance(600); await flush();
  assert.equal(requests.length, 0);
  assert.equal(app.history.length, 0);
  app.onURLInput("https://example.com/image.png");
  clock.advance(600); await flush();
  assert.equal(requests.length, 1);
  app.destroy();
});

test("saving a missing Token automatically resumes the selected image", async () => {
  const { app, requests } = setup();
  app.token = "";
  await app.selectFile(image());
  assert.equal(requests.length, 0);
  assert.equal(app.accessOpen, true);
  app.token = "new-token";
  assert.equal(app.rememberToken(), true);
  await flush();
  assert.equal(requests.length, 1);
  assert.equal(requests[0].options.headers.Authorization, "Bearer new-token");
  assert.equal(app.result.label, "1_valid");
  app.destroy();
});

test("the latest URL waits for an active request without overlapping inference", async () => {
  const clock = fakeClock(), pending = deferredRequests();
  const { app } = setup({ ...clock, fetch: pending.fetch });
  app.selectMode("url");
  app.onURLInput("https://example.com/first.png");
  clock.advance(600); await flush();
  app.onURLInput("https://example.com/skipped.png"); clock.advance(600);
  app.onURLInput("https://example.com/latest.png"); clock.advance(600);
  assert.equal(pending.requests.length, 1);
  pending.requests[0].resolve(); await flush();
  assert.equal(pending.requests.length, 2);
  assert.equal(JSON.parse(pending.requests[1].options.body).url, "https://example.com/latest.png");
  assert.equal(app.busy, true);
  pending.requests[1].resolve(); await flush();
  assert.equal(app.busy, false);
  assert.deepEqual(app.history.map((entry) => entry.sourceURL), ["https://example.com/latest.png", "https://example.com/first.png"]);
  app.destroy();
});

test("a new pasted file during detection is automatically checked after the active file", async () => {
  const pending = deferredRequests();
  const { app } = setup({ fetch: pending.fetch });
  const first = app.selectFile(image("first.png"));
  await flush();
  const file = image("pasted.png");
  await app.onPaste({ clipboardData: { items: [{ kind: "file", type: file.type, getAsFile: () => file }] }, preventDefault() {} });
  assert.equal(pending.requests.length, 1);
  pending.requests[0].resolve(); await first; await flush();
  assert.equal(pending.requests.length, 2);
  assert.equal(pending.requests[1].options.body.get("file").name, "pasted.png");
  pending.requests[1].resolve(); await flush();
  assert.deepEqual(app.history.map((entry) => entry.name), ["pasted.png", "first.png"]);
  assert.equal(app.history[0].kind, "paste");
  app.destroy();
});

test("canceling or destroying clears debounced and waiting work", async () => {
  for (const action of ["cancel", "destroy"]) {
    const clock = fakeClock(), pending = deferredRequests();
    const { app } = setup({ ...clock, fetch: pending.fetch });
    app.selectMode("url");
    app.onURLInput("https://example.com/canceled.png");
    app[action](); clock.advance(600); await flush();
    assert.equal(pending.requests.length, 0);
    if (action === "destroy") continue;
    app.onURLInput("https://example.com/active.png"); clock.advance(600); await flush();
    app.onURLInput("https://example.com/waiting.png"); clock.advance(600);
    app.cancel(); await flush();
    assert.equal(pending.requests.length, 1);
    assert.equal(app.busy, false);
    assert.match(app.history[0].error, /取消/);
    assert.equal(clock.pending, 0);
    app.destroy();
  }
});

test("manual submission and changing mode cancel a pending URL timer", async () => {
  const clock = fakeClock();
  const { app, requests } = setup(clock);
  app.selectMode("url");
  app.onURLInput("https://example.com/image.png");
  await app.detect(); clock.advance(600); await flush();
  assert.equal(requests.length, 1);
  app.onURLInput("https://example.com/other.png");
  app.selectMode("file"); clock.advance(600); await flush();
  assert.equal(requests.length, 1);
  app.destroy();
});

test("out-of-order thumbnail completion cannot submit an older file", async () => {
  const thumbnails = [];
  const { app, requests } = setup({ createImageBitmap: () => new Promise((_resolve, reject) => thumbnails.push(reject)) });
  const first = app.selectFile(image("old.png"));
  const second = app.selectFile(image("new.png"));
  thumbnails[0](new Error("unavailable")); await first;
  assert.equal(requests.length, 0);
  thumbnails[1](new Error("unavailable")); await second;
  assert.equal(requests.length, 1);
  assert.equal(requests[0].options.body.get("file").name, "new.png");
  app.destroy();
});

test("thumbnail completion after switching to URL respects the URL debounce", async () => {
  const clock = fakeClock();
  let rejectThumbnail;
  const { app, requests } = setup({ ...clock, createImageBitmap: () => new Promise((_resolve, reject) => { rejectThumbnail = reject; }) });
  const selection = app.selectFile(image());
  app.selectMode("url");
  app.onURLInput("https://example.com/image.png");
  rejectThumbnail(new Error("unavailable")); await selection;
  assert.equal(requests.length, 0);
  clock.advance(600); await flush();
  assert.equal(requests.length, 1);
  assert.equal(requests[0].options.headers["Content-Type"], "application/json");
  app.destroy();
});
