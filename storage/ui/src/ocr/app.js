import { createAutoSubmit } from "../shared/auto-submit.js";
import { TOKEN_KEY, cleanToken, credentialExpired, loadCredential, saveCredential } from "../shared/credential.js";
import { imageURL, percent, supportedImageFile } from "../shared/image.js";
import { entryWeight, readOCRResult } from "./result.js";

export const HISTORY_LIMIT = 20;
export const HISTORY_BYTES = 32 * 1024 * 1024;

export function createOCRApp(browser = window) {
  let file = null;
  let inputURL = "";
  let request = null;
  let timer = null;
  let disposed = false;
  let selection = 0;
  const automatic = createAutoSubmit(browser, (page) => page.recognize());
  const clearAuto = automatic.cancel;
  const objectURLs = new Set();
  const makePreview = (value) => {
    const url = browser.URL.createObjectURL(value);
    objectURLs.add(url);
    return url;
  };
  const release = (url) => {
    if (objectURLs.delete(url)) browser.URL.revokeObjectURL(url);
  };

  return {
    mode: "file", url: "", token: "", tokenExpiresAt: 0, accessOpen: false,
    credentialNotice: "", busy: false, dragging: false, fileName: "", fileSize: "", fileKind: "file",
    preview: "", inputPreviewFailed: false, previewErrors: {}, error: "", notice: "",
    history: [], activeID: "", resultMode: "text", selectedLine: -1, showBoxes: true,
    maxBytes: 10 * 1024 * 1024, timeout: 62000,

    init() {
      this.maxBytes = Number(this.$el.dataset.maxBytes) || this.maxBytes;
      this.timeout = (Number(this.$el.dataset.timeoutMs) || 60000) + 2000;
      try {
        const credential = loadCredential(browser.localStorage);
        this.token = credential?.token || "";
        this.tokenExpiresAt = credential?.expiresAt || 0;
      } catch { this.credentialNotice = "浏览器无法保存访问凭证，Token 仅用于当前页面。"; }
    },
    destroy() {
      disposed = true;
      automatic.destroy();
      request?.abort();
      browser.clearTimeout(timer);
      for (const url of objectURLs) browser.URL.revokeObjectURL(url);
      objectURLs.clear();
      file = null;
    },
    get active() { return this.history.find((entry) => entry.id === this.activeID) || null; },
    get result() { return this.active?.result || null; },
    get activePreview() { return this.active && !this.previewErrors[this.active.id] ? this.active.preview : ""; },
    get canSubmit() { return !this.busy && (this.mode === "url" ? !!this.url.trim() : !!this.preview); },
    get maxSizeLabel() { return `${Math.round(this.maxBytes / 1024 / 1024)} MB`; },
    get tokenExpiryLabel() { return this.tokenExpiresAt ? `已保存，有效至 ${new Date(this.tokenExpiresAt).toLocaleDateString("zh-CN")}` : "与图片检测共用 API Token。"; },
    percent,
    timeLabel(value) { return new Date(value).toLocaleTimeString("zh-CN", { hour12: false }); },
    durationLabel(value) { return value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(2)} s`; },
    sourceLabel(kind) { return ({ file: "文件上传", paste: "剪贴板", url: "图片链接" })[kind] || "图片"; },
    points(box) { return box.map((point) => `${point.x},${point.y}`).join(" "); },
    historyImage(entry) { return this.previewErrors[entry.id] ? "" : entry.preview; },
    selectMode(mode) {
      if (this.mode === mode || disposed) return;
      clearAuto(); this.mode = mode; this.error = "";
      if (mode === "url") this.onURLInput(this.url);
      else if (file) return this.autoRecognize();
    },
    sourceKey() {
      if (this.mode === "file") return file ? `file:${selection}` : "";
      try { return `url:${imageURL(this.url)}`; } catch { return ""; }
    },
    onURLInput(value, composing = false) {
      this.url = value; this.error = ""; clearAuto();
      if (this.mode === "url") automatic.schedule(this, composing);
    },
    autoRecognize() { return automatic.attempt(this); },
    selectFile(value, kind = "file") {
      if (!value || disposed) return;
      clearAuto(); this.error = "";
      if (!supportedImageFile(value)) { this.error = "请选择 JPEG、WebP、PNG 或 AVIF 图片。"; return; }
      if (!value.size || value.size > this.maxBytes) { this.error = `图片不能为空，且不能超过 ${this.maxSizeLabel}。`; return; }
      release(inputURL);
      file = value;
      inputURL = makePreview(value);
      this.preview = inputURL;
      this.mode = "file";
      this.fileKind = kind;
      this.fileName = value.name || "粘贴的图片.png";
      this.fileSize = value.size < 1024 * 1024 ? `${(value.size / 1024).toFixed(1)} KB` : `${(value.size / 1024 / 1024).toFixed(2)} MB`;
      this.inputPreviewFailed = false;
      selection++;
      return this.autoRecognize();
    },
    onFileInput(event) { const run = this.selectFile(event.target.files?.[0]); event.target.value = ""; return run; },
    onPaste(event) {
      if (disposed) return;
      const item = [...(event.clipboardData?.items || [])].find((value) => value.kind === "file" && value.type.startsWith("image/"));
      if (item) { event.preventDefault(); return this.selectFile(item.getAsFile(), "paste"); }
    },
    onDrop(event) { this.dragging = false; return this.selectFile(event.dataTransfer?.files?.[0]); },
    clearFile() {
      if (this.busy) return;
      clearAuto(); selection++;
      release(inputURL); inputURL = ""; file = null;
      this.preview = ""; this.fileName = ""; this.fileSize = ""; this.inputPreviewFailed = false;
      if (this.$refs.fileInput) this.$refs.fileInput.value = "";
    },
    rememberToken(resume = true) {
      this.error = "";
      this.token = cleanToken(this.token);
      if (!this.token) { this.error = "请填写 API Token。"; this.accessOpen = true; return false; }
      if (credentialExpired(this.token, this.tokenExpiresAt)) {
        this.forgetToken(); this.error = "API Token 已过期，请重新生成。"; this.accessOpen = true; return false;
      }
      try {
        const credential = saveCredential(browser.localStorage, this.token);
        this.tokenExpiresAt = credential.expiresAt;
        this.credentialNotice = "";
      } catch { this.credentialNotice = "浏览器无法保存 Token，当前页面仍可继续识别。"; }
      this.accessOpen = false;
      if (resume) this.autoRecognize();
      return true;
    },
    forgetToken() {
      automatic.reset();
      this.token = ""; this.tokenExpiresAt = 0;
      try { browser.localStorage.removeItem(TOKEN_KEY); } catch { this.credentialNotice = "无法清除保存的 Token，请检查浏览器存储权限。"; }
    },
    showEntry(id) { if (!this.busy) { this.activeID = id; this.selectedLine = -1; this.notice = ""; } },
    addHistory(entry) {
      if (entryWeight(entry) > HISTORY_BYTES) { release(entry.preview); entry.preview = ""; entry.previewBytes = 0; }
      this.history.unshift(entry);
      let bytes = this.history.reduce((sum, item) => sum + entryWeight(item), 0);
      while (this.history.length > HISTORY_LIMIT || bytes > HISTORY_BYTES) {
        const removed = this.history.pop();
        bytes -= entryWeight(removed); release(removed.preview); delete this.previewErrors[removed.id];
      }
      this.activeID = entry.id; this.selectedLine = -1;
    },
    clearHistory() {
      if (this.busy) return;
      for (const entry of this.history) release(entry.preview);
      this.history = []; this.activeID = ""; this.selectedLine = -1; this.previewErrors = {}; this.notice = "";
    },
    cancel() { clearAuto(); request?.abort(); },
    async copyText() {
      if (!this.result?.text) return;
      try { await browser.navigator.clipboard.writeText(this.result.text); if (!disposed) this.notice = "识别文本已复制。"; }
      catch { if (!disposed) this.notice = "浏览器不允许自动复制，请在结果中选择并复制文本。"; }
    },
    async recognize() {
      if (disposed || this.busy) return;
      clearAuto();
      if (!this.canSubmit || !this.rememberToken(false)) return;
      let sourceURL = "";
      try { if (this.mode === "url") sourceURL = imageURL(this.url); }
      catch (error) { this.error = error.message; return; }
      automatic.started(this);
      const entry = {
        id: browser.crypto.randomUUID(), time: Date.now(), duration: 0,
        kind: sourceURL ? "url" : this.fileKind, name: sourceURL || this.fileName,
        preview: sourceURL || makePreview(file), previewBytes: sourceURL ? 0 : file.size, result: null, error: "",
      };
      const headers = { Authorization: `Bearer ${this.token}`, Accept: "application/json" };
      let body;
      if (sourceURL) { headers["Content-Type"] = "application/json"; body = JSON.stringify({ url: sourceURL }); }
      else { body = new browser.FormData(); body.append("file", file, this.fileName); }
      const controller = new browser.AbortController();
      request = controller; this.busy = true; this.error = ""; this.notice = "";
      let timedOut = false;
      timer = browser.setTimeout(() => { timedOut = true; controller.abort(); }, this.timeout);
      const start = browser.performance.now();
      try {
        const response = await browser.fetch("/api/image/ocr", { method: "POST", headers, body, signal: controller.signal, cache: "no-store" });
        const payload = await response.json().catch(() => null);
        if (controller.signal.aborted) throw new DOMException("Aborted", "AbortError");
        if (!response.ok || payload?.status !== 0) {
          if ([401, 403].includes(response.status)) { this.forgetToken(); this.accessOpen = true; }
          const messages = {
            401: "API Token 无效或已过期，请检查访问设置。", 403: "API Token 无效或已过期，请检查访问设置。",
            408: "识别超时，请稍后重试。", 413: "图片或文字数量超过服务限制，请换一张较小的图片。",
            429: "服务正在处理其他图片，请稍后重试。", 502: "图片链接下载失败，请检查地址或改为上传文件。",
            503: "OCR 服务暂时不可用，请稍后重试。",
          };
          throw new Error(messages[response.status] || payload?.message || "OCR 请求失败，请重试。");
        }
        entry.result = readOCRResult(payload.data?.result);
      } catch (error) {
        entry.error = controller.signal.aborted ? (timedOut ? "识别超时，请稍后重试。" : "已取消识别。") : error.message || "无法连接 OCR 服务。";
      } finally {
        browser.clearTimeout(timer); timer = null; request = null;
        if (disposed) { release(entry.preview); return; }
        entry.duration = Math.max(0, browser.performance.now() - start);
        this.addHistory(entry); this.busy = false;
        automatic.finished(this);
      }
    },
  };
}
