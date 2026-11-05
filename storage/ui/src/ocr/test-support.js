import { createOCRApp } from "./app.js";

export const result = { text: "中文 OCR", width: 200, height: 100, lines: [{ text: "中文 OCR", confidence: 0.98, box: [{ x: 10, y: 10 }, { x: 180, y: 10 }, { x: 180, y: 50 }, { x: 10, y: 50 }] }] };
export const response = (value = result) => ({ ok: true, status: 200, json: async () => ({ status: 0, data: { result: value } }) });
export const imageFile = () => new File(["fixture"], "image.avif", { type: "image/avif" });

export function setup(overrides = {}) {
  let nextID = 0;
  const saved = new Map(), revoked = [], requests = [];
  const browser = {
    localStorage: { getItem: (key) => saved.get(key) ?? null, setItem: (key, value) => saved.set(key, value), removeItem: (key) => saved.delete(key) },
    URL: { createObjectURL: () => `blob:${++nextID}`, revokeObjectURL: (value) => revoked.push(value) },
    crypto: { randomUUID: () => String(++nextID) }, performance: { now: () => 100 },
    setTimeout, clearTimeout, AbortController, FormData,
    navigator: { clipboard: { writeText: async () => {} } },
    fetch: async (url, options) => { requests.push({ url, options }); return response(); },
    ...overrides,
  };
  const app = createOCRApp(browser);
  app.$el = { dataset: {} }; app.$refs = {}; app.init(); app.token = "test-token";
  return { app, browser, saved, revoked, requests };
}
