import assert from "node:assert/strict";
import test from "node:test";
import { HISTORY_KEY, HISTORY_LIMIT, imageURL, loadHistory, saveHistory, readResult } from "./history.js";

function storage() {
  const data = new Map();
  return { getItem: (key) => data.get(key) ?? null, setItem: (key, value) => data.set(key, value), removeItem: (key) => data.delete(key) };
}
const result = { label: "1_valid", labels: ["0_invalid", "1_valid"], probabilities: [0.1, 0.9], class_id: 1, confidence: 0.9, sha256: "a".repeat(64) };
const entry = (id = "one") => ({ id, kind: "file", time: 1000, duration: 40, name: "image.png", result, thumbnail: "", sourceURL: "" });

test("history survives refresh and never serializes credentials or original files", () => {
  const session = storage();
  assert.equal(saveHistory(session, [{ ...entry(), token: "secret", file: { secret: true } }]), true);
  assert.deepEqual(loadHistory(session), [{ ...entry(), backend: "default", error: "" }]);
  assert.doesNotMatch(session.getItem(HISTORY_KEY), /secret|token/);
  session.removeItem(HISTORY_KEY);
  assert.deepEqual(loadHistory(session), []);
});
test("history is bounded and corrupted records do not discard good ones", () => {
  const session = storage();
  saveHistory(session, Array.from({ length: 80 }, (_, i) => entry(String(i))));
  assert.equal(loadHistory(session).length, HISTORY_LIMIT);
  session.setItem(HISTORY_KEY, JSON.stringify([{}, entry(), entry(), { ...entry("date"), time: Number.MAX_VALUE }, { ...entry("unsafe"), sourceURL: "javascript:alert(1)" }]));
  assert.equal(loadHistory(session).length, 1);
  session.setItem(HISTORY_KEY, "not json");
  assert.deepEqual(loadHistory(session), []);
});
test("storage failures do not throw or change a successful classification", () => {
  const unavailable = { getItem() { throw new Error("disabled"); }, setItem() { throw new Error("quota"); } };
  assert.deepEqual(loadHistory(unavailable), []);
  assert.equal(saveHistory(unavailable, [entry()]), false);
  assert.equal(entry().result.label, "1_valid");
});
test("URL validation excludes unsafe schemes and credential-bearing URLs", () => {
  assert.equal(imageURL(" https://example.com/image.png "), "https://example.com/image.png");
  for (const value of ["javascript:alert(1)", "file:///tmp/image.png", "https://user:pass@example.com/a", "https://example.com/a#hash", "not a URL"]) assert.throws(() => imageURL(value));
});
test("malformed prediction payloads never become successful history records", () => {
  assert.deepEqual(readResult(result), result);
  for (const invalid of [{ ...result, class_id: 0 }, { ...result, probabilities: [NaN, 1] }, { ...result, probabilities: [0.8, 0.8] }]) assert.throws(() => readResult(invalid));
});

test("backend history preserves explicit routes and upgrades old entries", () => {
  const session = storage();
  session.setItem(HISTORY_KEY, JSON.stringify([
    { ...entry("go"), backend: "go" }, { ...entry("native"), backend: "libvips" }, entry("legacy"),
    { ...entry("unsafe"), backend: "https://example.com/api/image/filter" },
  ]));
  const values = loadHistory(session);
  assert.deepEqual(values.map((value) => value.backend), ["go", "libvips", "default"]);
  assert.equal(saveHistory(session, values), true);
  assert.deepEqual(loadHistory(session), values);
});
