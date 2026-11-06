import { imageURL } from "../shared/image.js";
import { detectionRoute } from "./routes.js";
export { imageURL, percent } from "../shared/image.js";

export const HISTORY_KEY = "inferkit.image-filter.history.v1";
export const HISTORY_LIMIT = 50;
export const THUMBNAIL_LIMIT = 40000;
const MAX_STORED_LENGTH = 3 * 1024 * 1024;

export function labelTitle(label) {
  return ({ "0_invalid": "无效图片", "1_valid": "有效图片" })[label] || label || "未知类别";
}

export function readResult(value) {
  if (!value || !Array.isArray(value.labels) || !Array.isArray(value.probabilities)
    || value.labels.length !== 2 || value.probabilities.length !== 2
    || value.labels.some((label) => typeof label !== "string" || !label || label.length > 128)
    || value.probabilities.some((p) => !Number.isFinite(p) || p < 0 || p > 1)
    || Math.abs(value.probabilities.reduce((sum, p) => sum + p, 0) - 1) > 0.001
    || ![0, 1].includes(value.class_id) || value.labels[value.class_id] !== value.label
    || value.probabilities[value.class_id] !== value.confidence
    || !/^[a-f0-9]{64}$/.test(value.sha256)) {
    throw new Error("服务返回的检测结果不完整，请重试。");
  }
  return {
    label: value.label, labels: [...value.labels], probabilities: [...value.probabilities],
    class_id: value.class_id, confidence: value.confidence, sha256: value.sha256,
  };
}

export function normalizeEntry(value) {
  if (!value || typeof value.id !== "string" || value.id.length > 80
    || !Number.isFinite(value.time) || !Number.isFinite(new Date(value.time).getTime()) || !Number.isFinite(value.duration)
    || !["file", "paste", "url"].includes(value.kind)) throw new Error("历史记录格式无效");
  const sourceURL = value.sourceURL ? imageURL(value.sourceURL) : "";
  const thumbnail = typeof value.thumbnail === "string" && value.thumbnail.length <= THUMBNAIL_LIMIT
    && /^data:image\/jpeg;base64,[A-Za-z0-9+/=]+$/.test(value.thumbnail) ? value.thumbnail : "";
  return {
    id: value.id, time: value.time, duration: Math.max(0, value.duration), kind: value.kind,
    backend: detectionRoute(value.backend).id,
    name: String(value.name || "图片").slice(0, 512), sourceURL, thumbnail,
    result: value.result ? readResult(value.result) : null,
    error: value.result ? "" : String(value.error || "检测失败").slice(0, 500),
  };
}

export function loadHistory(storage) {
  try {
    const raw = storage.getItem(HISTORY_KEY);
    if (!raw || raw.length > MAX_STORED_LENGTH) return [];
    const values = JSON.parse(raw);
    if (!Array.isArray(values)) return [];
    const entries = [];
    const ids = new Set();
    for (const value of values.slice(0, HISTORY_LIMIT)) {
      try {
        const entry = normalizeEntry(value);
        if (!ids.has(entry.id)) { entries.push(entry); ids.add(entry.id); }
      } catch { /* Ignore individual damaged records while preserving valid history. */ }
    }
    return entries;
  } catch { return []; }
}

export function saveHistory(storage, entries) {
  // Whitelist fields so credentials, Files and other reactive state never persist.
  try {
    const clean = entries.slice(0, HISTORY_LIMIT).map(normalizeEntry);
    storage.setItem(HISTORY_KEY, JSON.stringify(clean));
    return true;
  } catch { return false; }
}
