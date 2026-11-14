import { cursorOf, filtersFromURL, queryParams, reviewHref, safeImageURL } from "./query.js";
import { labelTitle, percent } from "../image-filter/history.js";

const successToastDuration = 3000;

export function createImageReviewApp(browser = window) {
  let controller = null, disposed = false, toastTimer = null;
  const request = async (path, options = {}) => {
    const response = await browser.fetch(`/image-review/api${path}`, {
      credentials: "same-origin", ...options,
      headers: { Accept: "application/json", "X-Requested-With": "XMLHttpRequest", ...options.headers },
    });
    const payload = await response.json().catch(() => null);
    if (!response.ok || payload?.status !== 0) {
      const error = new Error(response.status === 401 ? "登录已失效，请刷新页面重新登录。" : payload?.message || "记录服务暂不可用，请重试。");
      error.status = response.status; throw error;
    }
    return payload.data;
  };
  return {
    view: "list", labels: [], filters: {}, activeFilters: {}, items: [], current: null, cursors: [null], reviewCursor: null,
    loading: false, saving: false, hasMore: false, error: "", toast: { message: "", type: "success" }, reviewedCount: 0, brokenImages: {},
    modalRecord: null, modalError: "", pageCursor: null,
    async init() {
      this.view = this.$el.dataset.view;
      this.labels = (this.$el.dataset.labels || "").trim().split(/\s+/).filter(Boolean);
      this.filters = filtersFromURL(browser.location.search, this.view === "review");
      this.activeFilters = { ...this.filters };
      const id = new URLSearchParams(browser.location.search).get("id");
      if (this.view === "review" && id && /^[1-9]\d*$/.test(id)) return this.reloadCurrent(id);
      return this.applyFilters();
    },
    destroy() { disposed = true; controller?.abort(); this.dismissToast(); this.$refs?.annotationDialog?.close(); },
    notify(message, type = "success") {
      if (disposed) return;
      this.dismissToast();
      this.toast = { message, type };
      if (type === "success") toastTimer = browser.setTimeout(() => this.dismissToast(), successToastDuration);
    },
    dismissToast() {
      if (toastTimer !== null) browser.clearTimeout(toastTimer);
      toastTimer = null; this.toast.message = "";
    },
    reportError(message, field = "error") { this[field] = message; this.notify(message, "error"); },
    retryToast() {
      if (this.busy) return;
      this.dismissToast();
      if (this.modalRecord) return this.reloadAnnotation();
      if (this.view === "list") return this.loadPage(this.cursors.at(-1));
      return this.current ? this.reloadCurrent() : this.loadPage(this.reviewCursor);
    },
    get busy() { return this.loading || this.saving; },
    get reviewLink() { return reviewHref(this.activeFilters); },
    get pageNumber() { return this.cursors.length; },
    get currentImage() { return this.current && !this.brokenImages[this.current.id] ? safeImageURL(this.current.url) : ""; },
    get modalImage() { return this.modalRecord ? this.imageSource(this.modalRecord) : ""; },
    get selectionImage() { return this.view === "review" ? this.currentImage : this.modalImage; },
    percent, labelTitle, safeImageURL,
    annotationSelected(record, label) {
      const selected = record?.correction || record?.label;
      const normalized = ({ "0_invalid": "00_invalid", "1_valid": "01_valid" })[selected] || selected;
      return !!selected && normalized === label;
    },
    annotationClass(record, label) {
      return { "review-selected": this.annotationSelected(record, label) };
    },
    labelShortcut(label) { const index = this.labels.indexOf(label); return index < 0 || index > 9 ? "" : String((index + 1) % 10); },
    handleShortcut(event) {
      if (disposed || this.view !== "review" || this.busy || !this.current || this.modalRecord) return;
      if (event.defaultPrevented || event.repeat || event.isComposing || event.ctrlKey || event.metaKey || event.altKey || event.shiftKey) return;
      if (event.target?.closest?.("input, select, textarea, button, a, [contenteditable]:not([contenteditable='false']), [role='textbox'], dialog")) return;
      if (event.key === "Enter") { event.preventDefault(); return this.acceptModel(); }
      if (!/^[0-9]$/.test(event.key) || !this.currentImage) return;
      const label = this.labels[(Number(event.key) + 9) % 10];
      if (label) { event.preventDefault(); return this.annotate(label); }
    },
    imageSource(record) { return this.brokenImages[record.id] ? "" : safeImageURL(record.url); },
    dateLabel(value) { return new Date(value).toLocaleString("zh-CN"); },
    async applyFilters() {
      if (this.saving || this.modalRecord || disposed) return;
      try { queryParams(this.filters); } catch (error) { this.reportError(error.message); return; }
      this.activeFilters = { ...this.filters }; this.cursors = [null]; this.reviewCursor = null;
      this.reviewedCount = 0; this.dismissToast();
      return this.loadPage(null);
    },
    async loadPage(cursor) {
      if (disposed) return;
      if (this.toast.type === "error") this.dismissToast();
      controller?.abort();
      const activeController = new browser.AbortController(); controller = activeController;
      this.loading = true; this.error = ""; this.hasMore = false;
      if (this.view === "review") this.current = null;
      else this.items = [];
      try {
        const params = queryParams(this.activeFilters, cursor, this.view === "review" ? 1 : 24);
        const data = await request(`/results?${params}`, { signal: activeController.signal });
        if (disposed || controller !== activeController) return;
        this.brokenImages = {};
        if (this.view === "review") this.current = data.page.items[0] || null;
        else this.items = data.page.items;
        this.pageCursor = cursorOf(data.page.items.at(-1));
        this.hasMore = data.page.has_more;
      } catch (error) {
        if (!disposed && controller === activeController && error.name !== "AbortError") this.reportError(error.message);
      } finally {
        if (!disposed && controller === activeController) { this.loading = false; controller = null; }
      }
    },
    async nextPage() {
      if (this.busy || !this.hasMore) return;
      this.cursors.push(this.pageCursor);
      return this.loadPage(this.cursors.at(-1));
    },
    async previousPage() {
      if (this.busy || this.cursors.length <= 1) return;
      this.cursors.pop(); return this.loadPage(this.cursors.at(-1));
    },
    async nextImage() {
      if (this.busy || !this.current) return;
      this.reviewCursor = cursorOf(this.current);
      return this.loadPage(this.reviewCursor);
    },
    reloadCurrent(id = this.current?.id) { return this.reloadRecord(id, "current"); },
    async openAnnotation(record) {
      if (this.busy || disposed) return;
      this.modalRecord = record; this.modalError = "";
      await this.$nextTick();
      if (disposed || this.modalRecord?.id !== record.id) return;
      const dialog = this.$refs.annotationDialog;
      if (!dialog.open) dialog.showModal();
    },
    closeAnnotation() {
      if (this.saving) return;
      if (this.loading) { controller?.abort(); controller = null; this.loading = false; }
      this.$refs.annotationDialog.close();
      this.modalRecord = null; this.modalError = "";
    },
    reloadAnnotation() { return this.reloadRecord(this.modalRecord?.id, "modalRecord"); },
    async reloadRecord(id, field) {
      if (this.busy || !id || disposed) return;
      const errorField = field === "modalRecord" ? "modalError" : "error";
      const activeController = new browser.AbortController(); controller = activeController;
      this.loading = true; this[errorField] = ""; this.dismissToast();
      try {
        const data = await request(`/results/${id}`, { signal: activeController.signal });
        if (disposed || controller !== activeController) return;
        const previous = this.items.find((item) => item.id === id);
        if (previous && previous.url === data.record.url) {
          this.syncAnnotation(previous, data.record.correction, data.record.revision - previous.revision);
        }
        this[field] = data.record;
        this.items = this.items.map((item) => item.id === id ? data.record : item);
        delete this.brokenImages[id];
      } catch (error) {
        if (!disposed && controller === activeController && error.name !== "AbortError") this.reportError(error.message, errorField);
      } finally {
        if (!disposed && controller === activeController) { this.loading = false; controller = null; }
      }
    },
    chooseLabel(label) {
      return this.view === "review" ? this.annotate(label) : this.annotateList(this.modalRecord, label);
    },
    acceptModel(record = this.current) {
      if (!record) return;
      const options = { adoptModel: true };
      return this.view === "review" ? this.annotate(record.label, options) : this.annotateList(record, record.label, options);
    },
    async annotate(label, options) {
      if (!await this.saveAnnotation(this.current, label, options)) return;
      this.reviewedCount++;
      await this.nextImage();
      if (this.error) this.reportError(`标注已保存，下一张图片加载失败：${this.error}`);
    },
    async annotateList(record, label, options) {
      if (!await this.saveAnnotation(record, label, options)) return;
      if (this.modalRecord?.id === record.id) this.closeAnnotation();
      if (this.activeFilters.review === "pending") this.items = this.items.filter((item) => item.url !== record.url);
    },
    syncAnnotation(record, label, revisionDelta = 1) {
      for (const item of this.items) {
        if (item.url === record.url && item.id !== record.id) { item.correction = label; item.revision += revisionDelta; }
      }
      record.correction = label; record.revision += revisionDelta;
    },
    async saveAnnotation(record, label, options = {}) {
      if (this.busy || !record || disposed) return false;
      const errorField = this.modalRecord ? "modalError" : "error";
      this.saving = true; this[errorField] = ""; this.dismissToast();
      const activeController = new browser.AbortController(); controller = activeController;
      try {
        await request(`/results/${record.id}/annotation`, {
          method: "PUT", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ label, revision: record.revision }), signal: activeController.signal,
        });
        if (disposed) return false;
        this.syncAnnotation(record, label);
        this.notify(`${options.adoptModel ? "已采用模型结果" : "已保存"}：${labelTitle(label)}`);
        return true;
      } catch (error) {
        if (!disposed && error.name !== "AbortError") this.reportError(error.message, errorField);
        return false;
      } finally { if (!disposed) { this.saving = false; controller = null; } }
    },
  };
}
