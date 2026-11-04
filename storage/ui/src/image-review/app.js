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
    async init() {
      this.view = this.$el.dataset.view;
      this.filters = filtersFromURL(browser.location.search, this.view === "review");
      this.activeFilters = { ...this.filters };
      const id = new URLSearchParams(browser.location.search).get("id");
      if (this.view === "review" && id && /^[1-9]\d*$/.test(id)) return this.reloadCurrent(id);
      return this.applyFilters();
    },
    destroy() { disposed = true; controller?.abort(); },
    get busy() { return this.loading || this.saving; },
    get reviewLink() { return reviewHref(this.activeFilters); },
    get pageNumber() { return this.cursors.length; },
    get currentImage() { return this.current && !this.brokenImages[this.current.id] ? safeImageURL(this.current.url) : ""; },
    percent, labelTitle, safeImageURL,
    reviewHref(record) { return reviewHref(this.activeFilters, record.id); },
    imageSource(record) { return this.brokenImages[record.id] ? "" : safeImageURL(record.url); },
    dateLabel(value) { return new Date(value).toLocaleString("zh-CN"); },
    async applyFilters() {
      if (this.saving || disposed) return;
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
        this.hasMore = data.page.has_more;
      } catch (error) {
        if (!disposed && controller === activeController && error.name !== "AbortError") this.error = error.message;
      } finally {
        if (!disposed && controller === activeController) { this.loading = false; controller = null; }
      }
    },
    async nextPage() {
      if (this.busy || !this.hasMore) return;
      this.cursors.push(cursorOf(this.items.at(-1)));
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
    async reloadCurrent(id = this.current?.id) {
      if (this.busy || !id || disposed) return;
      this.loading = true; this.error = "";
      const activeController = new browser.AbortController(); controller = activeController;
      try {
        const data = await request(`/results/${id}`, { signal: activeController.signal });
        if (!disposed) { this.current = data.record; this.brokenImages = {}; }
      } catch (error) { if (!disposed && error.name !== "AbortError") this.error = error.message; }
      finally { if (!disposed) { this.loading = false; controller = null; } }
    },
    async annotate(label) {
      if (this.busy || !this.current || disposed) return;
      const record = this.current;
      this.saving = true; this.error = "";
      const activeController = new browser.AbortController(); controller = activeController;
      let saved = false;
      try {
        await request(`/results/${record.id}/annotation`, {
          method: "PUT", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ label, revision: record.revision }), signal: activeController.signal,
        });
        if (!disposed) { this.reviewedCount++; this.notice = `已保存：${labelTitle(label)}`; saved = true; }
      } catch (error) { if (!disposed && error.name !== "AbortError") this.error = error.message; }
      finally { if (!disposed) { this.saving = false; controller = null; } }
      if (saved) {
        await this.nextImage();
        if (this.error) this.error = `标注已保存，下一张图片加载失败：${this.error}`;
      }
    },
  };
}
