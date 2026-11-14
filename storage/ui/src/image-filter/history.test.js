import assert from "node:assert/strict";
import test from "node:test";
import { HISTORY_KEY, HISTORY_LIMIT, imageURL, loadHistory, saveHistory, readResult, labelTitle, labelState } from "./history.js";

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

test("ten-class results retain noncontiguous business IDs through history storage", () => {
  const labels = ["00_invalid", "01_valid", "03_childish", "04_paper", "05_other_app", "06_professional", "07_stolen", "08_screenshot", "09_sensitive", "10_photo"];
  const mapping = labels.map((label, index) => ({ index, class: label, label_id: index < 2 ? index : index + 1, name: labelTitle(label) }));
  const session = storage();
  for (let index = 0; index < labels.length; index++) {
    const value = { ...result, label: labels[index], labels, class_id: index, label_id: mapping[index].label_id,
      class_name: mapping[index].name, class_mapping: mapping, label_profile: "artwork_10class_v1",
      probabilities: labels.map((_, i) => i === index ? 1 : 0), confidence: 1 };
    assert.deepEqual(readResult(value), value);
    assert.equal(saveHistory(session, [{ ...entry(), result: value }]), true);
    assert.deepEqual(loadHistory(session)[0].result, value);
    assert.throws(() => readResult({ ...value, class_id: 10 }));
    assert.throws(() => readResult({ ...value, label_id: 99 }));
    assert.throws(() => readResult({ ...value, probabilities: value.probabilities.slice(1) }));
  }
  assert.equal(labelState("01_valid"), "valid");
  assert.equal(labelState("00_invalid"), "invalid");
  assert.equal(labelState("10_photo"), "category");
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
