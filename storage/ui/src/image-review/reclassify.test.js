import test from "node:test";
import assert from "node:assert/strict";
import { createReclassificationApp } from "./reclassify.js";
const model = "a".repeat(64);
function view(status = "idle", extra = {}) { return { available: true, active: false, current_model: model, state: { status, model_sha256: status === "idle" ? "" : model, snapshot: { total: 100 }, failures: [], ...extra } }; }
function setup(views) {
  const requests = []; const timers = new Map(); let timerID = 0;
  const app = createReclassificationApp({
    fetcher: async (url, options) => { requests.push({ url, options }); return { ok: true, json: async () => ({ data: { task: views.shift() } }) }; },
    schedule: (callback) => { timers.set(++timerID, callback); return timerID; }, unschedule: (id) => timers.delete(id),
  }); return { app, requests, timers };
}
test("start requires explicit confirmation and validates interval", async () => {
  const { app, requests } = setup([view(), view("running")]); await app.init();
  await app.start(); assert.equal(requests.length, 1);
  assert.equal(app.interval, 0);
  app.confirmed = true; app.interval = -1; await app.start(); assert.equal(requests.length, 1);
  app.interval = 0; await app.start(); assert.equal(requests.length, 2);
  assert.deepEqual(JSON.parse(requests[1].options.body), { model_sha256: model, interval_ms: 0, confirm_overwrite: true });
  assert.equal(requests[1].options.credentials, "same-origin"); assert.equal(app.canStart, false); app.destroy();
});
test("closing page stops polling without cancelling server work", async () => {
  const { app, requests, timers } = setup([view("running")]); await app.init();
  assert.equal(timers.size, 1); app.destroy();
  assert.equal(timers.size, 0); assert.equal(requests.length, 1); assert.equal(requests[0].options.method, "GET");
});
test("resume rejects changed model, active cleanup and broken checkpoints", async () => {
  const { app } = setup([view("paused")]); await app.init(); assert.equal(app.canResume, true);
  app.task.current_model = "b".repeat(64); assert.equal(app.canResume, false);
  app.task.current_model = model; app.task.active = true; assert.equal(app.canResume, false);
  app.task.active = false; app.state.reason = "checkpoint_unavailable"; assert.equal(app.canResume, false); app.destroy();
});
test("progress counts skipped and failed rows and clamps percentage", async () => {
  const { app } = setup([view("completed", { succeeded: 90, failed: 5, skipped: 5 })]); await app.init();
  assert.equal(app.progress, "100.0"); assert.equal(app.processed, 100); assert.equal(app.canStart, true); app.destroy();
});
test("a stale poll cannot overwrite accepted control state", async () => {
  let resolvePoll;
  let calls = 0;
  const app = createReclassificationApp({ schedule: () => 1, unschedule: () => {}, fetcher: async () => {
    calls++; if (calls === 1) return new Promise((resolve) => { resolvePoll = resolve; });
    return { ok: true, json: async () => ({ data: { task: view("pausing") } }) };
  } });
  const polling = app.refresh(); await app.control("pause");
  resolvePoll({ ok: true, json: async () => ({ data: { task: view("running") } }) }); await polling;
  assert.equal(app.state.status, "pausing"); app.destroy();
});
