import assert from "node:assert/strict";
import test from "node:test";
import { createImageReviewApp } from "./app.js";
import { filtersFromURL, queryParams, safeImageURL } from "./query.js";

const record = (id = 1) => ({ id, url: `https://example.com/${id}.png`, confidence: 0.8, revision: 0, label: "1_valid" });
function setup(view = "review", responses = []) {
  const requests = [], timers = new Map();
  let timerID = 0;
  const browser = { location: { search: "" }, AbortController,
    setTimeout(callback, delay) { const id = ++timerID; timers.set(id, { callback, delay }); return id; },
    clearTimeout(id) { timers.delete(id); },
    fetch: async (url, options) => {
    requests.push({ url, options });
    const response = responses.shift() || { status: 0, data: { page: { items: [], has_more: false } } };
    return { ok: response.status === 0, status: response.status || 200, json: async () => response };
  } };
  const app = createImageReviewApp(browser); app.$el = { dataset: { view } };
  const dialog = { open: false, showModal() { this.open = true; }, close() { this.open = false; } };
  app.$refs = { annotationDialog: dialog }; app.$nextTick = async () => {};
  return { app, requests, browser, dialog, timers };
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
  const { app, timers } = setup("review", [page([record()]), { status: 0, data: { saved: true } }, { status: 503, message: "加载失败" }, page([record(2)])]);
  await app.init(); await app.annotate("1_valid");
  assert.equal(app.reviewedCount, 1);
  assert.match(app.toast.message, /标注已保存/);
  assert.equal(app.toast.type, "error");
  assert.equal(timers.size, 0);
  assert.match(app.error, /标注已保存/);
  assert.equal(app.current, null);
  await app.loadPage(app.reviewCursor);
  assert.equal(app.current.id, 2);
  assert.equal(app.toast.message, "");
  app.destroy();
});

test("list paging only reads records", async () => {
  const { app, requests } = setup("list", [page([record(1)], true), page([record(2)]), page([record(1)], true)]);
  await app.init(); await app.nextPage();
  assert.equal(app.pageNumber, 2);
  await app.previousPage();
  assert.equal(app.pageNumber, 1);
  assert.ok(requests.every(({ options }) => !options.method));
  app.destroy();
});

test("skipping adopts the model prediction and advances only after saving", async () => {
  const first = record(1);
  const { app, requests } = setup("review", [page([first]), { status: 0, data: { saved: true } }, page([record(2)])]);
  await app.init(); await app.acceptModel();
  assert.deepEqual(JSON.parse(requests[1].options.body), { label: "1_valid", revision: 0 });
  assert.equal(first.correction, first.label);
  assert.equal(first.revision, 1);
  assert.equal(app.current.id, 2);
  assert.equal(app.reviewedCount, 1);
  app.destroy();
});

test("failed model adoption preserves the current record and revision", async () => {
  const first = record();
  const { app, requests } = setup("review", [page([first]), { status: 503, message: "保存失败" }]);
  await app.init(); await app.acceptModel();
  assert.equal(app.current, first);
  assert.equal(first.correction, undefined);
  assert.equal(first.revision, 0);
  assert.equal(app.reviewedCount, 0);
  assert.equal(requests.length, 2);
  app.destroy();
});

test("list annotations open locally in a dialog and update the card without reloading", async () => {
  const first = record();
  const { app, requests, dialog } = setup("list", [page([first]), { status: 0, data: { saved: true } }]);
  await app.init(); await app.openAnnotation(first);
  assert.equal(dialog.open, true);
  assert.equal(requests.length, 1);
  assert.equal(app.modalRecord.id, first.id);
  await app.chooseLabel("0_invalid");
  assert.equal(dialog.open, false);
  assert.equal(app.modalRecord, null);
  assert.equal(app.items[0].correction, "0_invalid");
  assert.equal(app.items[0].label, "1_valid");
  assert.equal(app.items[0].confidence, 0.8);
  assert.equal(app.items[0].revision, 1);
  assert.equal(requests.length, 2);
  assert.deepEqual(JSON.parse(requests[1].options.body), { label: "0_invalid", revision: 0 });
  app.destroy();
});

test("closing the annotation dialog does not write", async () => {
  const { app, requests, dialog } = setup("list", [page([record()])]);
  await app.init(); await app.openAnnotation(app.items[0]); app.closeAnnotation();
  assert.equal(dialog.open, false);
  assert.equal(app.modalRecord, null);
  assert.equal(requests.length, 1);
  app.destroy();
});

test("quick adoption updates the card without fetching another record", async () => {
  const { app, requests } = setup("list", [page([record()]), { status: 0, data: { saved: true } }]);
  await app.init(); await app.acceptModel(app.items[0]);
  assert.equal(app.items[0].correction, "1_valid");
  assert.equal(app.items[0].revision, 1);
  assert.equal(requests.length, 2);
  app.destroy();
});

test("card results use one success toast whose timeout resets after each save", async () => {
  const saved = () => ({ status: 0, data: { saved: true } });
  const { app, timers } = setup("list", [page([record()]), saved(), saved(), saved()]);
  await app.init();
  for (const [label, action] of [
    ["无效图片", () => app.annotateList(app.items[0], "0_invalid")],
    ["有效图片", () => app.annotateList(app.items[0], "1_valid")],
    ["有效图片", () => app.acceptModel(app.items[0])],
  ]) {
    const previousTimer = timers.keys().next().value;
    await action();
    assert.deepEqual(app.toast, { message: `已保存：${label}`, type: "success" });
    assert.equal(timers.size, 1);
    assert.equal(timers.has(previousTimer), false);
  }
  const timer = timers.values().next().value;
  assert.equal(timer.delay, 3000);
  timer.callback();
  assert.equal(app.toast.message, "");
  assert.equal(timers.size, 0);
  assert.equal(app.items[0].revision, 3);
  app.destroy();
});

test("failed saves keep an error toast with a reload action", async () => {
  const first = record();
  const { app, requests, timers } = setup("list", [page([first]), { status: 409, message: "版本冲突" },
    page([{ ...first, revision: 3, correction: "1_valid" }])]);
  await app.init(); await app.annotateList(first, "0_invalid");
  assert.deepEqual(app.toast, { message: "版本冲突", type: "error" });
  assert.equal(timers.size, 0);
  assert.equal(first.revision, 0);
  await app.retryToast();
  assert.equal(app.toast.message, "");
  assert.equal(app.error, "");
  assert.equal(app.items[0].revision, 3);
  assert.equal(requests.length, 3);
  assert.equal(requests[2].options.method, undefined);
  app.destroy();
});

test("dismissal and destruction clear pending toast timers", async () => {
  const { app, timers } = setup("list");
  app.notify("已保存");
  app.dismissToast();
  assert.equal(app.toast.message, "");
  assert.equal(timers.size, 0);
  app.notify("已保存");
  app.destroy();
  assert.equal(app.toast.message, "");
  assert.equal(timers.size, 0);
  app.notify("迟到的提示");
  assert.equal(app.toast.message, "");
  assert.equal(timers.size, 0);
});

test("direct card labels overwrite same-URL annotations and preserve predictions", async () => {
  const first = record(1), sibling = { ...record(2), url: first.url, label: "0_invalid", revision: 4 };
  const other = record(3);
  const saved = () => ({ status: 0, data: { saved: true } });
  const { app, requests, dialog } = setup("list", [page([first, sibling, other]), saved(), saved(), saved()]);
  await app.init();
  await app.annotateList(first, "0_invalid");
  await app.annotateList(sibling, "1_valid");
  await app.acceptModel(sibling);
  assert.equal(dialog.open, false);
  assert.equal(requests.length, 4);
  assert.deepEqual(requests.slice(1).map(({ options }) => JSON.parse(options.body)), [
    { label: "0_invalid", revision: 0 }, { label: "1_valid", revision: 5 }, { label: "0_invalid", revision: 6 },
  ]);
  assert.equal(first.correction, "0_invalid");
  assert.equal(sibling.correction, "0_invalid");
  assert.equal(first.revision, 3);
  assert.equal(sibling.revision, 7);
  assert.equal(first.label, "1_valid");
  assert.equal(sibling.label, "0_invalid");
  assert.equal(first.confidence, 0.8);
  assert.equal(other.correction, undefined);
  assert.equal(other.revision, 0);
  app.destroy();
});

test("failed direct card writes preserve all same-URL labels and versions", async () => {
  for (const status of [409, 503]) {
    const first = record(1), sibling = { ...record(2), url: first.url };
    const { app, dialog } = setup("list", [page([first, sibling]), { status, message: "保存失败" }]);
    await app.init(); await app.annotateList(first, "0_invalid");
    assert.equal(app.error, "保存失败");
    assert.equal(dialog.open, false);
    for (const item of app.items) {
      assert.equal(item.correction, undefined);
      assert.equal(item.revision, 0);
    }
    app.destroy();
  }
});

test("pending review removes every same-URL card after a direct label", async () => {
  const first = record(1), sibling = { ...record(2), url: first.url };
  const { app, browser } = setup("list", [page([first, sibling, record(3)]), { status: 0, data: { saved: true } }]);
  browser.location.search = "?review=pending";
  await app.init(); await app.annotateList(first, "0_invalid");
  assert.deepEqual(app.items.map(({ id }) => id), [3]);
  assert.equal(app.pageCursor.id, 3);
  app.destroy();
});

test("pending cards are removed after saving while pagination keeps the original cursor", async () => {
  const { app, browser, requests } = setup("list", [page([record(1), record(2)], true), { status: 0, data: { saved: true } }, page([record(3)])]);
  browser.location.search = "?review=pending";
  await app.init(); await app.acceptModel(app.items[1]);
  assert.deepEqual(app.items.map(({ id }) => id), [1]);
  await app.nextPage();
  assert.equal(new URL(requests[2].url, "https://example.com").searchParams.get("after_id"), "2");
  assert.equal(app.items[0].id, 3);
  app.destroy();
});

test("modal conflicts show a toast and reload the latest revision before retry", async () => {
  const updated = { ...record(), revision: 3, correction: "1_valid" };
  const sibling = { ...record(2), url: updated.url, revision: 5 };
  const { app, requests, dialog } = setup("list", [page([record(), sibling]), { status: 409, message: "版本冲突" },
    { status: 0, data: { record: updated } }, { status: 0, data: { saved: true } }]);
  await app.init(); await app.openAnnotation(app.items[0]); await app.chooseLabel("0_invalid");
  assert.equal(dialog.open, true);
  assert.equal(app.modalError, "版本冲突");
  assert.deepEqual(app.toast, { message: "版本冲突", type: "error" });
  assert.equal(app.items[0].revision, 0);
  await app.retryToast();
  assert.equal(app.toast.message, "");
  assert.equal(sibling.revision, 8);
  assert.equal(sibling.correction, "1_valid");
  await app.chooseLabel("0_invalid");
  assert.deepEqual(JSON.parse(requests[3].options.body), { label: "0_invalid", revision: 3 });
  assert.equal(app.items[0].revision, 4);
  assert.equal(sibling.revision, 9);
  assert.equal(sibling.correction, "0_invalid");
  assert.equal(dialog.open, false);
  app.destroy();
});

test("modal saving blocks dismissal and duplicate card actions", async () => {
  const { app, browser, dialog } = setup("list", [page([record()])]);
  await app.init(); await app.openAnnotation(app.items[0]);
  let calls = 0, finish;
  browser.fetch = async () => { calls++; await new Promise((resolve) => { finish = resolve; }); return { ok: true, status: 200, json: async () => ({ status: 0, data: { saved: true } }) }; };
  const pending = app.chooseLabel("0_invalid");
  app.closeAnnotation(); await app.acceptModel(app.items[0]); await app.chooseLabel("1_valid");
  assert.equal(dialog.open, true);
  assert.equal(calls, 1);
  finish(); await pending;
  assert.equal(dialog.open, false);
  assert.equal(app.items[0].correction, "0_invalid");
  app.destroy();
});

test("closing a reloading dialog cancels its read and ignores a late response", async () => {
  const { app, browser } = setup("list", [page([record(1), record(2)])]);
  await app.init(); await app.openAnnotation(app.items[0]);
  let finish, signal;
  browser.fetch = async (_url, options) => {
    signal = options.signal;
    await new Promise((resolve) => { finish = resolve; });
    return { ok: true, status: 200, json: async () => ({ status: 0, data: { record: { ...record(1), revision: 7 } } }) };
  };
  const pending = app.reloadAnnotation();
  app.closeAnnotation();
  assert.equal(signal.aborted, true);
  assert.equal(app.loading, false);
  await app.openAnnotation(app.items[1]);
  finish(); await pending;
  assert.equal(app.modalRecord.id, 2);
  assert.equal(app.items[0].revision, 0);
  app.destroy();
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
