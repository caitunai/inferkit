import { createImageFilterApp } from "./app.js";

export const prediction = { label: "1_valid", labels: ["0_invalid", "1_valid"], probabilities: [0.1, 0.9], class_id: 1, confidence: 0.9, sha256: "a".repeat(64) };
export const successResponse = () => ({ ok: true, status: 200, json: async () => ({ status: 0, data: { result: prediction } }) });
export const flush = () => new Promise((resolve) => setImmediate(resolve));

export function setup(overrides = {}) {
  const saved = new Map(), localSaved = new Map(), requests = [], revoked = [];
  let nextID = 0;
  const browser = {
    sessionStorage: { getItem: (key) => saved.get(key) ?? null, setItem: (key, value) => saved.set(key, value), removeItem: (key) => saved.delete(key) },
    localStorage: { getItem: (key) => localSaved.get(key) ?? null, setItem: (key, value) => localSaved.set(key, value), removeItem: (key) => localSaved.delete(key) },
    URL: { createObjectURL: () => "blob:test", revokeObjectURL: (value) => revoked.push(value) },
    createImageBitmap: async () => { throw new Error("thumbnail unavailable"); },
    crypto: { randomUUID: () => String(++nextID) }, performance: { now: () => 100 },
    setTimeout, clearTimeout, AbortController, FormData,
    fetch: async (url, options) => { requests.push({ url, options }); return successResponse(); },
    ...overrides,
  };
  const app = createImageFilterApp(browser);
  app.$el = { dataset: {} }; app.$refs = {}; app.init(); app.token = "test-token";
  return { app, browser, saved, localSaved, requests, revoked };
}

export function fakeClock() {
  let time = 0, nextID = 0;
  const timers = new Map();
  return {
    setTimeout(callback, delay) { const id = ++nextID; timers.set(id, { callback, at: time + delay }); return id; },
    clearTimeout(id) { timers.delete(id); },
    advance(milliseconds) {
      time += milliseconds;
      for (const [id, timer] of [...timers]) {
        if (timer.at <= time) { timers.delete(id); timer.callback(); }
      }
    },
    get pending() { return timers.size; },
  };
}

export function deferredRequests() {
  const requests = [];
  return {
    requests,
    fetch(url, options) {
      return new Promise((resolve, reject) => {
        requests.push({ url, options, resolve: () => resolve(successResponse()) });
        options.signal.addEventListener("abort", () => reject(new DOMException("canceled", "AbortError")), { once: true });
      });
    },
  };
}
