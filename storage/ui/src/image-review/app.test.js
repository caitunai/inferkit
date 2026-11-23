import assert from "node:assert/strict";
import test from "node:test";
import { createImageReviewApp } from "./app.js";
import { filtersFromURL, maxReviewJumpPage, maxReviewOffset, offsetForPage, queryParams, reviewPageSize, safeImageURL } from "./query.js";

const labels = ["00_invalid", "01_valid", "03_childish", "04_paper", "05_other_app", "06_professional", "07_stolen", "08_screenshot", "09_sensitive", "10_photo"];
const record = (id = 1) => ({ id, url: `https://example.com/${id}.png`, confidence: 0.8, revision: 0, label: "1_valid" });
function setup(view = "review", responses = []) {
  const requests = [], timers = new Map(), statisticsTimers = new Map(), statisticsRequests = [], statisticsResponses = [];
  let timerID = 0;
  const browser = { location: { search: "" }, AbortController,
    setTimeout(callback, delay) { const id = ++timerID; (delay === 350 ? statisticsTimers : timers).set(id, { callback, delay }); return id; },
    clearTimeout(id) { timers.delete(id); statisticsTimers.delete(id); },
    fetch: async (url, options) => {
    if (url.endsWith("/statistics")) {
      statisticsRequests.push({ url, options });
      const value = statisticsResponses.shift() || { status: 0, data: { statistics: { total: 0, classes: labels.map((label) => ({ label, count: 0 })), generated_at: "2026-11-23T00:00:00Z" } } };
      return { ok: value.status === 0, status: value.status || 200, json: async () => value };
    }
    requests.push({ url, options });
    const response = responses.shift() || { status: 0, data: { page: { items: [], has_more: false } } };
    return { ok: response.status === 0, status: response.status || 200, json: async () => response };
  } };
  const app = createImageReviewApp(browser); app.$el = { dataset: { view, labels: labels.join(" ") } };
  const dialog = { open: false, showModal() { this.open = true; }, close() { this.open = false; } };
  app.$refs = { annotationDialog: dialog }; app.$nextTick = async () => {};
  return { app, requests, browser, dialog, timers, statisticsTimers, statisticsRequests, statisticsResponses };
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

test("adopting the model saves a reviewed label and advances only after success", async () => {
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
    ["已保存：无效图片", () => app.annotateList(app.items[0], "0_invalid")],
    ["已保存：有效图片", () => app.annotateList(app.items[0], "1_valid")],
    ["已采用模型结果：有效图片", () => app.acceptModel(app.items[0])],
  ]) {
    const previousTimer = timers.keys().next().value;
    await action();
    assert.deepEqual(app.toast, { message: label, type: "success" });
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

test("each gallery category saves directly without a dialog or list reload", async () => {
  for (const label of labels) {
    const first = { ...record(), correction: "01_valid", revision: 3 };
    const { app, requests, dialog } = setup("list", [page([first]), { status: 0, data: { saved: true } }]);
    await app.init(); await app.annotateList(first, label);
    assert.deepEqual(JSON.parse(requests[1].options.body), { label, revision: 3 });
    assert.equal(requests.length, 2); assert.equal(dialog.open, false);
    assert.equal(app.items[0].correction, label); assert.equal(app.items[0].label, "1_valid");
    assert.equal(app.annotationSelected(first, label), true); assert.equal(app.annotationClass(first, label)["review-selected"], true);
    app.destroy();
  }
});

test("legacy human labels highlight their matching new classification buttons", () => {
  const { app } = setup("list");
  assert.equal(app.annotationSelected({ correction: "0_invalid" }, "00_invalid"), true);
  assert.equal(app.annotationSelected({ correction: "1_valid" }, "01_valid"), true);
  assert.equal(app.annotationSelected({ correction: "03_childish" }, "01_valid"), false);
  app.destroy();
});

function shortcut(key, extra = {}) {
  return { key, prevented: false, preventDefault() { this.prevented = true; }, target: { closest: () => null }, ...extra };
}

test("number shortcuts save all ten categories and advance after saving", async () => {
  for (const [index, label] of labels.entries()) {
    const { app, requests } = setup("review", [page([record(1)]), { status: 0, data: { saved: true } }, page([record(2)])]);
    await app.init();
    const event = shortcut(String((index + 1) % 10));
    assert.equal(app.labelShortcut(label), event.key);
    await app.handleShortcut(event);
    assert.equal(event.prevented, true);
    assert.equal(JSON.parse(requests[1].options.body).label, label);
    assert.equal(app.current.id, 2); assert.equal(app.reviewedCount, 1);
    app.destroy();
  }
});

test("Enter explicitly adopts a multiclass prediction and marks the record reviewed", async () => {
  const first = { ...record(1), label: "08_screenshot", correction: "01_valid", revision: 4 };
  const { app, requests } = setup("review", [page([first]), { status: 0, data: { saved: true } }, page([record(2)])]);
  await app.init(); await app.handleShortcut(shortcut("Enter"));
  assert.deepEqual(JSON.parse(requests[1].options.body), { label: "08_screenshot", revision: 4 });
  assert.equal(first.correction, "08_screenshot"); assert.equal(first.revision, 5);
  assert.equal(app.current.id, 2); assert.equal(app.toast.message, "已采用模型结果：截图");
  app.destroy();
});

test("shortcuts do not annotate while editing, composing, repeating or using modifiers", async () => {
  const { app, requests } = setup("review", [page([record()])]); await app.init();
  for (const extra of [{ target: { closest: () => ({}) } }, { repeat: true }, { isComposing: true }, { ctrlKey: true }, { metaKey: true }, { altKey: true }, { shiftKey: true }, { defaultPrevented: true }]) {
    for (const key of ["1", "Enter"]) await app.handleShortcut(shortcut(key, extra));
  }
  app.loading = true; await app.handleShortcut(shortcut("Enter")); app.loading = false;
  app.modalRecord = record(); await app.handleShortcut(shortcut("1")); app.modalRecord = null;
  app.view = "list"; await app.handleShortcut(shortcut("1"));
  assert.equal(requests.length, 1); assert.equal(app.reviewedCount, 0); app.destroy();
});

test("rapid shortcut presses cannot duplicate model adoption while a save is pending", async () => {
  const { app, browser } = setup("review", [page([record()])]); await app.init();
  let finish; let writes = 0;
  browser.fetch = async () => {
    writes++; await new Promise((resolve) => { finish = resolve; });
    return { ok: false, status: 409, json: async () => ({ message: "标注冲突" }) };
  };
  const pending = app.handleShortcut(shortcut("Enter"));
  await app.handleShortcut(shortcut("Enter")); await app.handleShortcut(shortcut("3"));
  assert.equal(writes, 1); finish(); await pending;
  assert.equal(app.current.id, 1); assert.equal(app.current.revision, 0); assert.equal(app.reviewedCount, 0);
  app.destroy();
});

test("only the human classification is highlighted, falling back to model predictions when unreviewed", () => {
  const { app } = setup("list");
  for (const [item, expected] of [
    [{ label: "00_invalid" }, "00_invalid"],
    [{ label: "01_valid", correction: "" }, "01_valid"],
    [{ label: "00_invalid", correction: "03_childish" }, "03_childish"],
    [{ label: "10_photo", correction: "01_valid" }, "01_valid"],
    [{ label: "1_valid" }, "01_valid"],
    [{ label: "0_invalid", correction: "1_valid" }, "01_valid"],
  ]) {
    assert.deepEqual(labels.filter((label) => app.annotationSelected(item, label)), [expected]);
    for (const label of labels) assert.deepEqual(app.annotationClass(item, label), { "review-selected": label === expected });
  }
  assert.equal(app.annotationSelected(null, "00_invalid"), false);
  app.destroy();
});

test("failed correction keeps the model highlighted until a human annotation is saved", async () => {
  const first = { ...record(), label: "08_screenshot", correction: "" };
  const { app } = setup("list", [page([first]), { status: 503, message: "保存失败" }, { status: 0, data: { saved: true } }]);
  await app.init(); await app.annotateList(first, "10_photo");
  assert.equal(app.annotationSelected(first, "08_screenshot"), true);
  assert.equal(app.annotationSelected(first, "10_photo"), false);
  await app.annotateList(first, "10_photo");
  assert.equal(app.annotationSelected(first, "10_photo"), true);
  assert.equal(app.annotationSelected(first, "08_screenshot"), false);
  assert.equal(first.label, "08_screenshot");
  app.destroy();
});

const pageParams = (request) => new URL(request.url, "https://example.com").searchParams;

test("page jump offsets include the 20000 boundary and reject overflow or mixed cursors", () => {
  const filters = filtersFromURL("", false);
  assert.equal(reviewPageSize, 24);
  assert.equal(offsetForPage(1), 0);
  assert.equal(maxReviewJumpPage, 834);
  assert.equal(offsetForPage(834), 19992);
  assert.equal(queryParams(filters, null, reviewPageSize, maxReviewOffset).get("offset"), "20000");
  for (const value of [0, -1, 1.5, "1e2", 835, "999999999999999999999999"]) assert.throws(() => offsetForPage(value));
  for (const value of [-1, maxReviewOffset + 1, 1.5, Infinity]) assert.throws(() => queryParams(filters, null, reviewPageSize, value));
  assert.throws(() => queryParams(filters, { id: 1, confidence: .8 }, reviewPageSize, 24));
});

test("jumping preserves filters and resumes cursor browsing before returning to the offset origin", async () => {
  const jumpItems = [record(101), record(102)];
  const { app, browser, requests } = setup("list", [page([record()], true), page(jumpItems, true), page([record(103)], true), page(jumpItems, true), page([record(99)])]);
  browser.location.search = "?label=08_screenshot&min=60&max=90&sort=confidence_asc&review=pending";
  await app.init(); app.jumpPage = "5"; await app.jumpToPage();
  assert.equal(app.pageNumber, 5); assert.equal(app.items[0].id, 101);
  const jumped = pageParams(requests[1]);
  assert.equal(jumped.get("offset"), "96"); assert.equal(jumped.has("after_id"), false);
  for (const [key, value] of [["label", "08_screenshot"], ["min_confidence", "0.6"], ["max_confidence", "0.9"], ["sort", "confidence_asc"], ["review", "pending"]]) assert.equal(jumped.get(key), value);
  await app.nextPage(); assert.equal(app.pageNumber, 6);
  assert.equal(pageParams(requests[2]).get("after_id"), "102"); assert.equal(pageParams(requests[2]).has("offset"), false);
  await app.previousPage(); assert.equal(app.pageNumber, 5); assert.equal(pageParams(requests[3]).get("offset"), "96");
  await app.previousPage(); assert.equal(app.pageNumber, 4); assert.equal(pageParams(requests[4]).get("offset"), "72");
  app.destroy();
});

test("cursor next-page remains available beyond the direct jump limit", async () => {
  const { app, requests } = setup("list", [page([record()], true), page([record(20000)], true), page([record(20001)], true), page([record(20000)], true)]);
  await app.init(); await app.jumpToPage(834);
  assert.equal(pageParams(requests[1]).get("offset"), "19992");
  await app.nextPage(); assert.equal(app.pageNumber, 835);
  assert.equal(app.jumpPage, "834"); assert.equal(pageParams(requests[2]).has("offset"), false);
  assert.equal(pageParams(requests[2]).get("after_id"), "20000");
  await app.previousPage(); assert.equal(app.pageNumber, 834); assert.equal(pageParams(requests[3]).get("offset"), "19992");
  app.destroy();
});

test("invalid page numbers do not clear the visible list or issue requests", async () => {
  const { app, requests } = setup("list", [page([record()], true)]); await app.init();
  for (const value of [0, -1, 1.5, "1e2", 835, "999999999999999999999"]) await app.jumpToPage(value);
  assert.equal(requests.length, 1); assert.equal(app.pageNumber, 1); assert.equal(app.items[0].id, 1);
  assert.match(app.toast.message, /offset/); app.destroy();
});

test("applying new filters resets page jumps and keyset history", async () => {
  const { app, requests } = setup("list", [page([record()], true), page([record(99)], true), page([])]);
  await app.init(); await app.jumpToPage(5); app.filters.label = "10_photo"; await app.applyFilters();
  assert.equal(app.pageNumber, 1); assert.equal(app.jumpPage, "1");
  assert.equal(pageParams(requests[2]).has("offset"), false); assert.equal(pageParams(requests[2]).has("after_id"), false);
  assert.equal(pageParams(requests[2]).get("label"), "10_photo"); app.destroy();
});

test("retrying a failed jump keeps the original offset", async () => {
  const { app, requests } = setup("list", [page([record()], true), { status: 503, message: "读取失败" }, page([record(100)], true)]);
  await app.init(); await app.jumpToPage(5); assert.equal(app.items.length, 0); assert.match(app.error, /读取失败/);
  await app.retryToast(); assert.equal(app.pageNumber, 5); assert.equal(app.items[0].id, 100);
  assert.equal(pageParams(requests[2]).get("offset"), "96"); app.destroy();
});

test("annotating a jumped page preserves its original next-page cursor", async () => {
  const { app, browser, requests } = setup("list", [page([record()], true), page([record(99), record(100)], true), { status: 0, data: { saved: true } }, page([record(101)])]);
  browser.location.search = "?review=pending";
  await app.init(); await app.jumpToPage(3); await app.annotateList(app.items[1], "10_photo"); await app.nextPage();
  assert.equal(app.pageNumber, 4); assert.equal(app.items[0].id, 101);
  assert.equal(pageParams(requests[3]).get("after_id"), "100"); assert.equal(pageParams(requests[3]).has("offset"), false);
  app.destroy();
});

test("human annotation filters survive URLs, list queries and single-image review links", async () => {
  const { app, browser, requests } = setup("list", [page([record()], true), page([record(2)], true)]);
  browser.location.search = "?label=00_invalid&correction=10_photo&min=60&max=90&sort=confidence_asc";
  await app.init();
  assert.equal(app.filters.correction, "10_photo"); assert.equal(app.filters.review, "reviewed");
  assert.equal(pageParams(requests[0]).get("label"), "00_invalid");
  assert.equal(pageParams(requests[0]).get("correction"), "10_photo");
  const link = new URL(app.reviewLink, "https://example.com");
  assert.equal(link.searchParams.get("correction"), "10_photo"); assert.equal(link.searchParams.get("review"), "reviewed");
  await app.nextPage(); assert.equal(pageParams(requests[1]).get("correction"), "10_photo"); app.destroy();
  const review = filtersFromURL("?correction=08_screenshot", true);
  assert.equal(review.review, "reviewed"); assert.equal(queryParams(review).get("correction"), "08_screenshot");
});

test("correction selection implies reviewed records and pending clears the correction draft", async () => {
  const { app, requests } = setup("list", [page([record()]), page([])]); await app.init();
  app.filters.correction = "03_childish"; app.correctionChanged();
  assert.equal(app.filters.review, "reviewed");
  assert.equal(pageParams(requests[0]).has("correction"), false);
  app.filters.review = "pending"; app.reviewStateChanged();
  assert.equal(app.filters.correction, "");
  await app.applyFilters(); assert.equal(pageParams(requests[1]).has("correction"), false); app.destroy();
});

test("human classification is preserved by direct page jumps and cleared without stale cursors", async () => {
  const { app, browser, requests } = setup("list", [page([record()], true), page([record(101)], true), page([])]);
  browser.location.search = "?correction=04_paper";
  await app.init(); await app.jumpToPage(5);
  assert.equal(pageParams(requests[1]).get("offset"), "96"); assert.equal(pageParams(requests[1]).get("correction"), "04_paper");
  app.filters.correction = ""; await app.applyFilters();
  assert.equal(app.pageNumber, 1); assert.equal(pageParams(requests[2]).has("correction"), false);
  assert.equal(pageParams(requests[2]).has("offset"), false); assert.equal(pageParams(requests[2]).has("after_id"), false); app.destroy();
});

test("corrections that leave the selected human category remove all same-URL cards after saving", async () => {
  const first = { ...record(1), correction: "10_photo" };
  const sibling = { ...record(2), url: first.url, correction: "10_photo" };
  const other = { ...record(3), correction: "10_photo" };
  const { app, browser } = setup("list", [page([first, sibling, other]), { status: 0, data: { saved: true } }]);
  browser.location.search = "?correction=10_photo";
  await app.init(); await app.annotateList(first, "08_screenshot");
  assert.deepEqual(app.items.map(({ id }) => id), [3]); assert.equal(sibling.correction, "08_screenshot"); app.destroy();
});

test("matching corrections stay visible and failed corrections keep the filtered cards", async () => {
  const first = { ...record(), correction: "10_photo" };
  const { app, browser } = setup("list", [page([first]), { status: 0, data: { saved: true } }, { status: 409, message: "冲突" }]);
  browser.location.search = "?correction=10_photo";
  await app.init(); await app.annotateList(first, "10_photo"); assert.equal(app.items.length, 1);
  await app.annotateList(first, "08_screenshot");
  assert.equal(app.items.length, 1); assert.equal(first.correction, "10_photo"); app.destroy();
});

const statisticsResponse = (total) => ({ status: 0, data: { statistics: { total, classes: [{ label: "10_photo", count: total }], generated_at: "2026-11-23T00:00:00Z" } } });

test("global distinct statistics load once and are independent of filters and pagination", async () => {
  const { app, statisticsRequests, statisticsResponses } = setup("list", [page([record()], true), page([record(2)], true)]);
  statisticsResponses.push(statisticsResponse(2));
  await app.init(); assert.equal(app.statistics.total, 2); assert.equal(statisticsRequests.length, 1);
  app.filters.label = "00_invalid"; await app.nextPage();
  assert.equal(statisticsRequests.length, 1); assert.equal(app.statisticsClasses[0].count, 2); app.destroy();
});

test("rapid successful annotations debounce one independent statistics refresh", async () => {
  const saved = () => ({ status: 0, data: { saved: true } });
  const { app, statisticsRequests, statisticsResponses, statisticsTimers } = setup("list", [page([record()]), saved(), saved()]);
  await app.init(); await app.annotateList(app.items[0], "10_photo"); await app.acceptModel(app.items[0]);
  assert.equal(statisticsRequests.length, 1); assert.equal(statisticsTimers.size, 1); assert.equal(app.statisticsStale, true);
  statisticsResponses.push(statisticsResponse(1));
  const timer = statisticsTimers.values().next().value;
  assert.equal(timer.delay, 350); await timer.callback();
  assert.equal(app.statistics.total, 1); assert.equal(app.statisticsStale, false); assert.equal(statisticsRequests.length, 2); app.destroy();
});

test("failed statistics cannot turn committed annotations or list loading into failures", async () => {
  const { app, statisticsResponses, statisticsTimers } = setup("list", [page([record()]), { status: 0, data: { saved: true } }]);
  statisticsResponses.push({ status: 503, message: "统计不可用" });
  await app.init(); assert.equal(app.items.length, 1); assert.equal(app.error, ""); assert.match(app.statisticsError, /统计不可用/);
  await app.annotateList(app.items[0], "10_photo");
  statisticsResponses.push({ status: 503, message: "统计读取超时" });
  await statisticsTimers.values().next().value.callback();
  assert.equal(app.items[0].correction, "10_photo"); assert.equal(app.items[0].revision, 1);
  assert.equal(app.toast.type, "success"); assert.equal(app.error, ""); assert.match(app.statisticsError, /超时/); app.destroy();
});

test("failed annotations do not schedule statistics refreshes and destruction clears pending work", async () => {
  const { app, statisticsTimers, statisticsRequests } = setup("list", [page([record()]), { status: 409, message: "版本冲突" }, { status: 0, data: { saved: true } }]);
  await app.init(); await app.annotateList(app.items[0], "10_photo"); assert.equal(statisticsTimers.size, 0);
  await app.annotateList(app.items[0], "10_photo"); assert.equal(statisticsTimers.size, 1);
  app.destroy(); assert.equal(statisticsTimers.size, 0); assert.equal(statisticsRequests.length, 1);
});

test("stale statistics replies cannot overwrite a newer annotation epoch", async () => {
  const { app, browser, statisticsTimers } = setup("list", [page([record()])]); await app.init();
  let finish;
  browser.fetch = async (url) => {
    if (url.endsWith("/statistics")) return new Promise((resolve) => { finish = resolve; });
    return { ok: true, status: 200, json: async () => ({ status: 0, data: { saved: true } }) };
  };
  const pending = app.loadStatistics(); await app.annotateList(app.items[0], "10_photo");
  finish({ ok: true, status: 200, json: async () => statisticsResponse(99) }); await pending;
  assert.equal(app.statistics.total, 0); assert.equal(app.statisticsStale, true); assert.equal(statisticsTimers.size, 1); app.destroy();
});

test("statistics timer failures cannot make an accepted annotation appear failed", async () => {
  const { app, browser } = setup("list", [page([record()]), { status: 0, data: { saved: true } }]); await app.init();
  const schedule = browser.setTimeout;
  browser.setTimeout = (callback, delay) => { if (delay === 350) throw new Error("timers unavailable"); return schedule(callback, delay); };
  await app.annotateList(app.items[0], "10_photo");
  assert.equal(app.items[0].correction, "10_photo"); assert.equal(app.items[0].revision, 1);
  assert.equal(app.toast.type, "success"); assert.equal(app.error, ""); assert.match(app.statisticsError, /标注已保存/); app.destroy();
});
