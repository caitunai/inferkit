import { createAutoSubmit } from "../shared/auto-submit.js";
import { supportedImageFile } from "../shared/image.js";
import { HISTORY_LIMIT, HISTORY_KEY, THUMBNAIL_LIMIT, imageURL, labelTitle, labelState, loadHistory, percent, readResult, saveHistory } from "./history.js";
import { TOKEN_KEY, cleanToken, credentialExpired, loadCredential, saveCredential } from "./credential.js";
import { DETECTION_ROUTES, detectionRoute } from "./routes.js";

async function thumbnail(file, browser) {
  const bitmap = await browser.createImageBitmap(file);
  try {
    const canvas = browser.document.createElement("canvas");
    for (const edge of [480, 320, 160]) {
      const scale = Math.min(1, edge / Math.max(bitmap.width, bitmap.height));
      canvas.width = Math.max(1, Math.round(bitmap.width * scale));
      canvas.height = Math.max(1, Math.round(bitmap.height * scale));
      const ctx = canvas.getContext("2d");
      ctx.fillStyle = "#ffffff";
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
      const data = canvas.toDataURL("image/jpeg", 0.72);
      if (data.length <= THUMBNAIL_LIMIT) return data;
    }
    return "";
  } finally { bitmap.close(); }
}

export function createImageFilterApp(browser = window) {
  let file = null;
  let objectURL = "";
  let request = null;
  let disposed = false;
  let selection = 0;
  const automatic = createAutoSubmit(browser, (page) => page.detect());
  const clearAuto = automatic.cancel;
  const releasePreview = () => {
    if (objectURL) browser.URL.revokeObjectURL(objectURL);
    objectURL = "";
  };
  const storage = () => browser.sessionStorage;

  return {
    mode: "file", url: "", token: "", accessOpen: false, busy: false, dragging: false,
    backend: "default", backendOptions: DETECTION_ROUTES,
    tokenExpiresAt: 0, credentialNotice: "", previewErrors: {},
    fileName: "", fileSize: "", fileKind: "file", preview: "", previewFailed: false,
    error: "", notice: "", history: [], activeID: "", historyFilter: "all",
    maxBytes: 10 * 1024 * 1024, timeout: 32000, pendingThumbnail: "",

    init() {
      this.maxBytes = Number(this.$el.dataset.maxBytes) || this.maxBytes;
      this.timeout = (Number(this.$el.dataset.timeoutMs) || 30000) + 2000;
      try { this.history = loadHistory(storage()); } catch { this.notice = "浏览器未开放会话存储，历史仅保留到本次刷新前。"; }
      this.activeID = this.history[0]?.id || "";
      this.backend = this.history[0]?.backend || "default";
      try {
        const credential = loadCredential(browser.localStorage);
        this.token = credential?.token || "";
        this.tokenExpiresAt = credential?.expiresAt || 0;
      } catch { this.credentialNotice = "浏览器未开放本地存储，Token 仅保留在当前页面。"; }
    },
    destroy() { disposed = true; selection++; automatic.destroy(); request?.abort(); releasePreview(); },
    get active() { return this.history.find((item) => item.id === this.activeID) || null; },
    get selectedRoute() { return detectionRoute(this.backend); },
    get result() { return this.active?.result || null; },
    get successCount() { return this.history.filter((item) => item.result).length; },
    get historyLabels() { return [...new Set(this.history.flatMap((item) => item.result?.labels || []))]; },
    get filteredHistory() {
      return this.history.filter((item) => this.historyFilter === "all"
        || (this.historyFilter === "error" ? !item.result : item.result?.label === this.historyFilter));
    },
    get canSubmit() { return !this.busy && (this.mode === "url" ? !!this.url.trim() : !!this.preview); },
    get tokenExpiryLabel() { return this.tokenExpiresAt ? `已保存，有效至 ${new Date(this.tokenExpiresAt).toLocaleDateString("zh-CN")}` : "默认保存一年；Token 提前过期时同步失效。"; },
    get previewSource() {
      const entry = this.active;
      if (!entry) return "";
      return entry.thumbnail || (entry.result && entry.kind === "url" ? entry.sourceURL : "");
    },
    get probabilities() {
      return this.result?.labels.map((label, index) => ({ label, value: this.result.probabilities[index], selected: index === this.result.class_id })) || [];
    },
    percent, labelTitle, labelState,
    historyImage(entry) { return this.previewErrors[entry.id] ? "" : entry.thumbnail || (entry.result && entry.kind === "url" ? entry.sourceURL : ""); },
    timeLabel(value) { return new Date(value).toLocaleTimeString("zh-CN", { hour12: false }); },
    durationLabel(value) { return value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(2)} s`; },
    sourceLabel(kind) { return ({ file: "本地文件", paste: "粘贴图片", url: "图片 URL" })[kind]; },
    routeName(backend) { return detectionRoute(backend).label; },
    routePath(backend) { return detectionRoute(backend).path; },
    onBackendChange() { clearAuto(); this.error = ""; return this.autoDetect(); },
    selectMode(mode) {
      if (this.mode === mode) return;
      clearAuto(); this.mode = mode; this.error = "";
      if (mode === "url") this.onURLInput(this.url);
      else if (file) return this.autoDetect();
    },
    sourceKey() {
      try {
        const backend = detectionRoute(this.backend).id;
        if (this.mode === "file") return file ? `${backend}:file:${selection}` : "";
        return `${backend}:url:${imageURL(this.url)}`;
      } catch { return ""; }
    },
    onURLInput(value, composing = false) {
      this.url = value; this.error = ""; clearAuto();
      automatic.schedule(this, composing);
    },
    autoDetect() { return automatic.attempt(this); },
    async selectFile(value, kind = "file") {
      if (!value || disposed) return;
      clearAuto();
      this.error = "";
      if (!supportedImageFile(value)) { this.error = "请选择 JPEG、WebP、AVIF 或 PNG 图片。"; return; }
      if (value.size > this.maxBytes) { this.error = `图片超过 ${Math.round(this.maxBytes / 1024 / 1024)} MB 限制。`; return; }
      releasePreview();
      file = value;
      this.mode = "file";
      this.fileKind = kind;
      this.fileName = value.name || "粘贴的图片.png";
      this.fileSize = value.size < 1024 * 1024 ? `${(value.size / 1024).toFixed(1)} KB` : `${(value.size / 1024 / 1024).toFixed(2)} MB`;
      objectURL = browser.URL.createObjectURL(value);
      this.preview = objectURL;
      this.pendingThumbnail = "";
      const version = ++selection;
      try {
        const result = await thumbnail(value, browser);
        if (!disposed && version === selection) this.pendingThumbnail = result;
      } catch { /* Thumbnail generation is optional; server decoding remains authoritative. */ }
      if (!disposed && version === selection && this.mode === "file") return this.autoDetect();
    },
    clearFile() {
      if (this.busy) return;
      clearAuto(); selection++; releasePreview(); file = null;
      this.fileName = ""; this.preview = ""; this.fileSize = ""; this.pendingThumbnail = "";
      if (this.$refs.fileInput) this.$refs.fileInput.value = "";
    },
    onFileInput(event) { const value = event.target.files?.[0]; event.target.value = ""; return this.selectFile(value); },
    onPaste(event) {
      const item = [...(event.clipboardData?.items || [])].find((item) => item.kind === "file" && item.type.startsWith("image/"));
      if (!item) return;
      event.preventDefault();
      return this.selectFile(item.getAsFile(), "paste");
    },
    onDrop(event) { this.dragging = false; return this.selectFile(event.dataTransfer?.files?.[0]); },
    showEntry(id) { if (!this.busy) { this.activeID = id; this.previewFailed = false; } },
    rememberToken(close = true) {
      this.error = "";
      if (!cleanToken(this.token)) { this.error = "请填写 API Token。"; return false; }
      if (credentialExpired(this.token, this.tokenExpiresAt)) {
        this.forgetToken(); this.error = "API Token 已过期，请重新生成。"; this.accessOpen = true; return false;
      }
      this.token = cleanToken(this.token);
      try {
        const credential = saveCredential(browser.localStorage, this.token);
        this.tokenExpiresAt = credential.expiresAt;
        this.credentialNotice = "";
      } catch { this.credentialNotice = "浏览器无法保存 Token，当前页面仍可继续检测。"; }
      if (close && !this.credentialNotice) this.accessOpen = false;
      if (close) this.autoDetect();
      return true;
    },
    forgetToken() {
      automatic.reset();
      this.token = ""; this.tokenExpiresAt = 0; this.credentialNotice = "";
      try { browser.localStorage.removeItem(TOKEN_KEY); } catch { this.credentialNotice = "无法清除浏览器保存的 Token，请检查存储权限。"; }
    },
    persistHistory() {
      let saved = false;
      try { saved = saveHistory(storage(), this.history); } catch { /* Storage can be disabled by the browser. */ }
      if (!saved) this.notice = "检测已完成；会话存储不可用，当前历史仍可查看，但刷新后可能丢失。";
    },
    clearHistory() {
      if (this.busy) return;
      this.history = []; this.activeID = ""; this.previewFailed = false; this.notice = ""; this.previewErrors = {};
      try { storage().removeItem(HISTORY_KEY); } catch { this.notice = "无法更新会话存储，请检查浏览器存储权限。"; }
    },
    cancel() { clearAuto(); selection++; request?.abort(); },
    async detect() {
      if (disposed || !this.canSubmit) return;
      clearAuto();
      this.error = "";
      if (!this.token.trim()) { this.accessOpen = true; this.error = "请在访问设置中填写 API Token，再开始检测。"; return; }
      if (!this.rememberToken(false)) return;
      let sourceURL = "";
      let route;
      try {
        route = detectionRoute(this.backend);
        if (this.mode === "url") sourceURL = imageURL(this.url);
      } catch (error) { this.error = error.message; return; }
      automatic.started(this);
      const version = selection;
      const kind = this.mode === "url" ? "url" : this.fileKind;
      const entry = {
        id: browser.crypto.randomUUID(), time: Date.now(), duration: 0, kind,
        backend: route.id,
        name: sourceURL || this.fileName, sourceURL,
        thumbnail: sourceURL ? "" : this.pendingThumbnail, result: null, error: "",
      };
      let body;
      const headers = { Authorization: `Bearer ${this.token.trim().replace(/^Bearer\s+/i, "")}`, Accept: "application/json" };
      if (sourceURL) { headers["Content-Type"] = "application/json"; body = JSON.stringify({ url: sourceURL }); }
      else { body = new browser.FormData(); body.append("file", file, this.fileName); }
      request = new browser.AbortController();
      this.busy = true;
      let timedOut = false;
      const controller = request;
      const timer = browser.setTimeout(() => { timedOut = true; controller.abort(); }, this.timeout);
      const started = browser.performance.now();
      try {
        const response = await browser.fetch(route.path, { method: "POST", headers, body, signal: controller.signal, credentials: "same-origin" });
        const payload = await response.json().catch(() => null);
        if (!response.ok || payload?.status !== 0) {
          if ([401, 403].includes(response.status)) { this.forgetToken(); this.accessOpen = true; }
          throw new Error(responseMessage(response.status, payload?.data?.code));
        }
        entry.result = readResult(payload.data?.result);
      } catch (error) {
        entry.error = error.name === "AbortError" ? (timedOut ? "检测超时，请稍后重试。" : "检测已取消。")
          : error instanceof TypeError ? "无法连接检测服务，请检查网络后重试。" : error.message;
        this.error = entry.error;
      } finally {
        browser.clearTimeout(timer); request = null;
        if (!disposed) {
          if (kind !== "url" && version === selection && !entry.thumbnail) entry.thumbnail = this.pendingThumbnail;
          entry.duration = browser.performance.now() - started;
          this.history = [entry, ...this.history].slice(0, HISTORY_LIMIT);
          this.previewErrors = Object.fromEntries(this.history.filter((item) => this.previewErrors[item.id]).map((item) => [item.id, true]));
          this.activeID = entry.id; this.previewFailed = false; this.historyFilter = "all";
          this.persistHistory(); this.busy = false;
          automatic.finished(this);
        }
      }
    },
  };
}

function responseMessage(status, code) {
  const specific = {
    image_url_dns_failed: "服务器无法解析图片域名，请检查服务器 DNS 或网络连接。",
  }[code];
  if (specific) return specific;
  return ({
    400: "图片无法解码，或 URL 格式不正确。", 401: "访问凭证已失效，请更新 API Token。",
    403: "访问验证未通过，请检查 API Token 及其权限。", 408: "检测超时，请稍后重试。",
    413: "图片文件或像素尺寸超过服务限制。", 429: "检测服务繁忙，请稍后重试。",
    502: "服务器无法下载图片，请检查地址、网络连接或访问权限。", 503: "检测服务暂不可用，请稍后重试。",
  })[status] || "检测失败，请稍后重试。";
}
