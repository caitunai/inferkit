import test from "node:test";
import assert from "node:assert/strict";
import { setup, response, imageFile } from "./test-support.js";
import { fakeClock, flush } from "../image-filter/test-support.js";

function pendingFetch() {
  const requests = [];
  return {
    requests,
    fetch(url, options) {
      return new Promise((resolve, reject) => {
        requests.push({ url, options, resolve: () => resolve(response()) });
        options.signal.addEventListener("abort", () => reject(new DOMException("canceled", "AbortError")), { once: true });
      });
    },
  };
}

test("file picker and drag/drop each immediately recognize the selected image", async () => {
  const { app, requests } = setup();
  const target = { files: [imageFile()], value: "image.avif" };
  await app.onFileInput({ target }); assert.equal(target.value, "");
  await app.onDrop({ dataTransfer: { files: [imageFile()] } });
  assert.equal(requests.length, 2); assert.ok(requests.every((request) => request.url === "/api/image/ocr"));
  app.destroy();
});

test("URL input debounces incomplete and composing edits and suppresses duplicates", async () => {
  const clock = fakeClock(); const { app, requests } = setup(clock);
  app.selectMode("url");
  for (const value of ["", "https://", "https:/example.com/a", "file:///a.png", "https://user:pass@example.com/a"]){
    app.onURLInput(value); clock.advance(600); await flush();
  }
  app.onURLInput("https://example.com/a.png", true); clock.advance(600); await flush();
  assert.equal(requests.length, 0);
  app.onURLInput("https://example.com/first.png"); clock.advance(400);
  app.onURLInput(" https://example.com/final.png "); clock.advance(599); await flush();
  assert.equal(requests.length, 0);
  clock.advance(1); await flush();
  assert.equal(requests.length, 1); assert.equal(JSON.parse(requests[0].options.body).url, "https://example.com/final.png");
  app.onURLInput(app.url); clock.advance(600); await flush(); assert.equal(requests.length, 1);
  await app.recognize(); assert.equal(requests.length, 2);
  app.destroy(); assert.equal(clock.pending, 0);
});

test("saving a missing credential resumes automatic recognition once", async () => {
  const { app, requests } = setup(); app.token = "";
  await app.selectFile(imageFile()); assert.equal(requests.length, 0); assert.equal(app.accessOpen, true);
  app.token = "new-token"; assert.equal(app.rememberToken(), true); await flush();
  assert.equal(requests.length, 1); assert.equal(requests[0].options.headers.Authorization, "Bearer new-token");
  assert.ok(app.result); app.destroy();
});

test("new pasted images queue only the latest source behind the active request", async () => {
  const pending = pendingFetch(); const { app } = setup({ fetch: pending.fetch });
  const first = app.selectFile(new File(["a"], "first.png", { type: "image/png" }));
  await app.selectFile(new File(["b"], "skipped.png", { type: "image/png" }));
  const pasted = new File(["c"], "latest.png", { type: "image/png" });
  await app.onPaste({ clipboardData: { items: [{ kind: "file", type: "image/png", getAsFile: () => pasted }] }, preventDefault() {} });
  assert.equal(pending.requests.length, 1);
  pending.requests[0].resolve(); await first; await flush();
  assert.equal(pending.requests.length, 2); assert.equal(pending.requests[1].options.body.get("file").name, "latest.png");
  assert.equal(app.busy, true);
  pending.requests[1].resolve(); await flush();
  assert.deepEqual(app.history.map((entry) => entry.name), ["latest.png", "first.png"]);
  assert.equal(app.history[0].kind, "paste"); app.destroy();
});

test("latest URL waits for its debounce and never overlaps active OCR", async () => {
  const clock = fakeClock(), pending = pendingFetch(); const { app } = setup({ ...clock, fetch: pending.fetch });
  app.selectMode("url"); app.onURLInput("https://example.com/first.png"); clock.advance(600); await flush();
  app.onURLInput("https://example.com/skipped.png"); clock.advance(600);
  app.onURLInput("https://example.com/latest.png"); clock.advance(599);
  pending.requests[0].resolve(); await flush(); assert.equal(pending.requests.length, 1);
  clock.advance(1); await flush(); assert.equal(pending.requests.length, 2);
  assert.equal(JSON.parse(pending.requests[1].options.body).url, "https://example.com/latest.png");
  pending.requests[1].resolve(); await flush(); app.destroy();
});

test("cancel and destruction clear queued and debounced recognition", async () => {
  for (const action of ["cancel", "destroy"]){
    const clock = fakeClock(), pending = pendingFetch(); const { app } = setup({ ...clock, fetch: pending.fetch });
    app.selectMode("url"); app.onURLInput("https://example.com/a.png"); app[action](); clock.advance(600); await flush();
    assert.equal(pending.requests.length, 0);
    if (action === "destroy") continue;
    app.onURLInput("https://example.com/b.png"); clock.advance(600); await flush();
    app.onURLInput("https://example.com/c.png"); clock.advance(600);
    app.cancel(); await flush(); assert.equal(pending.requests.length, 1); assert.equal(app.busy, false);
    assert.equal(clock.pending, 0); app.destroy();
  }
});

test("manual retry and source mode changes cancel the pending debounce", async () => {
  const clock = fakeClock(); const { app, requests } = setup(clock);
  app.selectMode("url"); app.onURLInput("https://example.com/manual.png"); await app.recognize();
  clock.advance(600); await flush(); assert.equal(requests.length, 1);
  app.onURLInput("https://example.com/canceled.png"); app.selectMode("file"); clock.advance(600); await flush();
  assert.equal(requests.length, 1); app.destroy();
});
