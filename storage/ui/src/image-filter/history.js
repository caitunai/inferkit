import { imageURL } from "../shared/image.js";
import { detectionRoute } from "./routes.js";
export { imageURL, percent } from "../shared/image.js";

export const HISTORY_KEY = "inferkit.image-filter.history.v1";
export const HISTORY_LIMIT = 50;
export const THUMBNAIL_LIMIT = 40000;
const MAX_STORED_LENGTH = 3 * 1024 * 1024;

export function labelTitle(label) {
  return ({ "0_invalid": "无效图片", "1_valid": "有效图片", "00_invalid": "无效", "01_valid": "有效",
    "03_childish": "幼稚", "04_paper": "纸绘", "05_other_app": "其它绘画APP", "06_professional": "专业",
    "07_stolen": "盗图", "08_screenshot": "截图", "09_sensitive": "敏感图", "10_photo": "普通照片" })[label] || label || "未知类别";
}

export function labelState(label) {
  if (["0_invalid", "00_invalid"].includes(label)) return "invalid";
  if (["1_valid", "01_valid"].includes(label)) return "valid";
  return "category";
}

function classMetadata(value) {
  const result = {};
  if (value.label_profile !== undefined) {
    if (typeof value.label_profile !== "string" || value.label_profile.length > 128) throw new Error("类别配置无效");
    result.label_profile = value.label_profile;
  }
  if (value.label_id !== undefined) {
    if (!Number.isInteger(value.label_id) || value.label_id < 0 || value.label_id > 2147483647) throw new Error("类别编号无效");
    result.label_id = value.label_id;
  }
  if (value.class_name !== undefined) {
    if (typeof value.class_name !== "string" || !value.class_name.trim() || value.class_name.length > 128) throw new Error("类别名称无效");
    result.class_name = value.class_name;
  }
  if (value.class_mapping !== undefined) {
    const mapping = value.class_mapping;
    if (!Array.isArray(mapping) || mapping.length !== value.labels.length
      || new Set(mapping.map((c) => c?.label_id)).size !== mapping.length
      || mapping.some((c, i) => !c || c.index !== i || c.class !== value.labels[i]
        || !Number.isInteger(c.label_id) || c.label_id < 0 || c.label_id > 2147483647
        || typeof c.name !== "string" || !c.name.trim() || c.name.length > 128)
      || mapping[value.class_id].label_id !== value.label_id || mapping[value.class_id].name !== value.class_name) throw new Error("类别映射不一致");
    result.class_mapping = mapping.map((c) => ({ index: c.index, class: c.class, label_id: c.label_id, name: c.name }));
  }
  return result;
}

export function readResult(value) {
  if (!value || !Array.isArray(value.labels) || !Array.isArray(value.probabilities)
    || value.labels.length < 2 || value.labels.length > 256 || value.probabilities.length !== value.labels.length
    || new Set(value.labels).size !== value.labels.length
    || value.labels.some((label) => typeof label !== "string" || !label || label.length > 128)
    || value.probabilities.some((p) => !Number.isFinite(p) || p < 0 || p > 1)
    || Math.abs(value.probabilities.reduce((sum, p) => sum + p, 0) - 1) > 0.001
    || !Number.isInteger(value.class_id) || value.class_id < 0 || value.class_id >= value.labels.length || value.labels[value.class_id] !== value.label
    || value.probabilities[value.class_id] !== value.confidence
    || !/^[a-f0-9]{64}$/.test(value.sha256)) {
    throw new Error("服务返回的检测结果不完整，请重试。");
  }
  return {
    label: value.label, labels: [...value.labels], probabilities: [...value.probabilities],
    class_id: value.class_id, confidence: value.confidence, sha256: value.sha256,
    ...classMetadata(value),
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
