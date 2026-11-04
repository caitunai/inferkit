import { cursorOf, filtersFromURL, queryParams, reviewHref, safeImageURL } from "./query.js";
import { labelTitle, percent } from "../image-filter/history.js";

export function createImageReviewApp(browser = window) {
  let controller = null, disposed = false;
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
    view: "list", filters: {}, activeFilters: {}, items: [], current: null, cursors: [null], reviewCursor: null,
    loading: false, saving: false, hasMore: false, error: "", notice: "", reviewedCount: 0, brokenImages: {},
    modalRecord: null, modalError: "", pageCursor: null,
    async init() {
      this.view = this.$el.dataset.view;
      this.filters = filtersFromURL(browser.location.search, this.view === "review");
      this.activeFilters = { ...this.filters };
      const id = new URLSearchParams(browser.location.search).get("id");
      if (this.view === "review" && id && /^[1-9]\d*$/.test(id)) return this.reloadCurrent(id);
      return this.applyFilters();
    },
    destroy() { disposed = true; controller?.abort(); this.$refs?.annotationDialog?.close(); },
    get busy() { return this.loading || this.saving; },
    get reviewLink() { return reviewHref(this.activeFilters); },
    get pageNumber() { return this.cursors.length; },
    get currentImage() { return this.current && !this.brokenImages[this.current.id] ? safeImageURL(this.current.url) : ""; },
    get modalImage() { return this.modalRecord ? this.imageSource(this.modalRecord) : ""; },
    get selectionImage() { return this.view === "review" ? this.currentImage : this.modalImage; },
    percent, labelTitle, safeImageURL,
    imageSource(record) { return this.brokenImages[record.id] ? "" : safeImageURL(record.url); },
    dateLabel(value) { return new Date(value).toLocaleString("zh-CN"); },
    async applyFilters() {
      if (this.saving || this.modalRecord || disposed) return;
      try { queryParams(this.filters); } catch (error) { this.error = error.message; return; }
      this.activeFilters = { ...this.filters }; this.cursors = [null]; this.reviewCursor = null;
      this.reviewedCount = 0; this.notice = "";
      return this.loadPage(null);
    },
    async loadPage(cursor) {
      if (disposed) return;
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
        if (!disposed && controller === activeController && error.name !== "AbortError") this.error = error.message;
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
      this.loading = true; this[errorField] = "";
      try {
        const data = await request(`/results/${id}`, { signal: activeController.signal });
        if (disposed || controller !== activeController) return;
        this[field] = data.record;
        this.items = this.items.map((item) => item.id === id ? data.record : item);
        delete this.brokenImages[id];
      } catch (error) {
        if (!disposed && controller === activeController && error.name !== "AbortError") this[errorField] = error.message;
      } finally {
        if (!disposed && controller === activeController) { this.loading = false; controller = null; }
      }
    },
    chooseLabel(label) {
      return this.view === "review" ? this.annotate(label) : this.annotateList(this.modalRecord, label);
    },
    acceptModel(record = this.current) {
      if (!record) return;
      return this.view === "review" ? this.annotate(record.label) : this.annotateList(record, record.label);
    },
    async annotate(label) {
      if (!await this.saveAnnotation(this.current, label)) return;
      this.reviewedCount++;
      await this.nextImage();
      if (this.error) this.error = `标注已保存，下一张图片加载失败：${this.error}`;
    },
    async annotateList(record, label) {
      if (!await this.saveAnnotation(record, label)) return;
      if (this.modalRecord?.id === record.id) this.closeAnnotation();
      if (this.activeFilters.review === "pending") this.items = this.items.filter((item) => item.id !== record.id);
    },
    async saveAnnotation(record, label) {
      if (this.busy || !record || disposed) return false;
      const errorField = this.modalRecord ? "modalError" : "error";
      this.saving = true; this[errorField] = ""; this.notice = "";
      const activeController = new browser.AbortController(); controller = activeController;
      try {
        await request(`/results/${record.id}/annotation`, {
          method: "PUT", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ label, revision: record.revision }), signal: activeController.signal,
        });
        if (disposed) return false;
        record.correction = label; record.revision++;
        this.notice = `已保存：${labelTitle(label)}`;
        return true;
      } catch (error) {
        if (!disposed && error.name !== "AbortError") this[errorField] = error.message;
        return false;
      } finally { if (!disposed) { this.saving = false; controller = null; } }
    },
  };
}
