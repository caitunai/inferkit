import assert from "node:assert/strict";
import test from "node:test";
import { createImageReviewApp } from "./app.js";
import { filtersFromURL, queryParams, safeImageURL } from "./query.js";

const record = (id = 1) => ({ id, url: `https://example.com/${id}.png`, confidence: 0.8, revision: 0, label: "1_valid" });
function setup(view = "review", responses = []) {
  const requests = [];
  const browser = { location: { search: "" }, AbortController, fetch: async (url, options) => {
    requests.push({ url, options });
    const response = responses.shift() || { status: 0, data: { page: { items: [], has_more: false } } };
    return { ok: response.status === 0, status: response.status || 200, json: async () => response };
  } };
  const app = createImageReviewApp(browser); app.$el = { dataset: { view } };
  return { app, requests, browser };
}
const page = (items, hasMore = false) => ({ status: 0, data: { page: { items, has_more: hasMore } } });

test("filters convert percent bounds and preserve confidence cursors without rounding", () => {
  const filters = filtersFromURL("?min=60&max=95&sort=confidence_asc&label=1_valid", true);
  const params = queryParams(filters, { id: 7, confidence: 0.800000011920929 }, 1);
  assert.equal(params.get("min_confidence"), "0.6");
  assert.equal(params.get("max_confidence"), "0.95");
  assert.equal(params.get("after_confidence"), "0.800000011920929");
  assert.equal(params.get("review"), "pending");
  for (const value of [{ ...filters, min: "NaN" }, { ...filters, min: 99, max: 50 }, { ...filters, sort: "id;drop table" }]) assert.throws(() => queryParams(value));
  assert.equal(safeImageURL("javascript:alert(1)"), "");
  assert.equal(safeImageURL("https://user:secret@example.com/a"), "");
});

test("saving an annotation preserves model data and automatically loads the next image", async () => {
  const first = record(1), second = record(2);
  const { app, requests } = setup("review", [page([first], true), { status: 0, data: { saved: true } }, page([second])]);
  await app.init(); await app.annotate("0_invalid");
  assert.equal(app.current.id, 2);
  assert.equal(app.reviewedCount, 1);
  assert.deepEqual(JSON.parse(requests[1].options.body), { label: "0_invalid", revision: 0 });
  assert.equal(requests[1].options.headers["X-Requested-With"], "XMLHttpRequest");
  assert.equal(requests[1].options.credentials, "same-origin");
  assert.equal(new URL(requests[2].url, "https://example.com").searchParams.get("after_id"), "1");
  assert.equal(first.label, "1_valid");
  app.destroy();
});

test("conflicting or failed annotations remain on the current image", async () => {
  for (const status of [409, 503]) {
    const { app, requests } = setup("review", [page([record()]), { status, message: "保存失败" }]);
    await app.init(); await app.annotate("0_invalid");
    assert.equal(app.current.id, 1);
    assert.equal(app.reviewedCount, 0);
    assert.equal(requests.length, 2);
    assert.equal(app.error, "保存失败");
    app.destroy();
  }
});

test("a failed next-image read does not turn a committed annotation into a failed save", async () => {
  const { app } = setup("review", [page([record()]), { status: 0, data: { saved: true } }, { status: 503, message: "加载失败" }, page([record(2)])]);
  await app.init(); await app.annotate("1_valid");
  assert.equal(app.reviewedCount, 1);
  assert.match(app.notice, /已保存/);
  assert.match(app.error, /标注已保存/);
  assert.equal(app.current, null);
  await app.loadPage(app.reviewCursor);
  assert.equal(app.current.id, 2);
  app.destroy();
});

test("skipping and paging never send an annotation write", async () => {
  const { app, requests } = setup("list", [page([record(1)], true), page([record(2)]), page([record(1)], true)]);
  await app.init(); await app.nextPage();
  assert.equal(app.pageNumber, 2);
  await app.previousPage();
  assert.equal(app.pageNumber, 1);
  assert.ok(requests.every(({ options }) => !options.method));
  app.destroy();
  const review = setup("review", [page([record(1)]), page([record(2)])]);
  await review.app.init(); await review.app.nextImage();
  assert.equal(review.app.current.id, 2);
  assert.ok(review.requests.every(({ options }) => !options.method));
  review.app.destroy();
});

test("draft filter edits do not change the active annotation range until applied", async () => {
  const { app, requests } = setup("review", [page([record()]), page([record(2)]), page([])]);
  await app.init(); app.filters.min = "90";
  await app.nextImage();
  assert.equal(new URL(requests[1].url, "https://example.com").searchParams.get("min_confidence"), "0");
  await app.applyFilters();
  assert.equal(new URL(requests[2].url, "https://example.com").searchParams.get("min_confidence"), "0.9");
  assert.equal(app.current, null);
  app.destroy();
});

test("double clicks cannot issue concurrent annotation writes", async () => {
  const { app, browser } = setup("review", [page([record()])]);
  await app.init();
  let calls = 0, finish;
  browser.fetch = async () => { calls++; await new Promise((resolve) => { finish = resolve; }); return { ok: false, status: 409, json: async () => ({ message: "冲突" }) }; };
  const pending = app.annotate("1_valid");
  await app.annotate("0_invalid");
  assert.equal(calls, 1);
  finish(); await pending;
  assert.equal(app.saving, false);
  app.destroy();
});
