// Keep the historic storage key so existing image-detection credentials still work.
export const TOKEN_KEY = "inferkit.image-filter.token.v1";
export const TOKEN_LIFETIME_MS = 365 * 24 * 60 * 60 * 1000;
const MAX_TOKEN_LENGTH = 16384;

export function cleanToken(value) {
  return String(value || "").trim().replace(/^Bearer\s+/i, "").trim();
}

// The JWT expiry only limits browser retention. Authentication and signature
// verification remain exclusively on the server.
function jwtExpiry(token) {
  try {
    const parts = token.split(".");
    if (parts.length !== 3) return null;
    const encoded = parts[1].replaceAll("-", "+").replaceAll("_", "/");
    const claims = JSON.parse(atob(encoded.padEnd(Math.ceil(encoded.length / 4) * 4, "=")));
    return Number.isFinite(claims.exp) ? claims.exp * 1000 : null;
  } catch { return null; }
}

function recordFor(value, now) {
  const token = cleanToken(value);
  if (!token || token.length > MAX_TOKEN_LENGTH) throw new Error("请填写有效的 API Token。");
  const expiresAt = Math.min(now + TOKEN_LIFETIME_MS, jwtExpiry(token) ?? Infinity);
  if (expiresAt <= now) throw new Error("API Token 已过期，请重新生成。");
  return { token, savedAt: now, expiresAt };
}

export function loadCredential(storage, now = Date.now()) {
  const raw = storage.getItem(TOKEN_KEY);
  if (!raw) return null;
  try {
    if (raw.length > MAX_TOKEN_LENGTH * 2) throw new Error("oversized credential record");
    const value = JSON.parse(raw);
    if (!value || !Number.isFinite(value.savedAt) || value.savedAt < 0 || value.savedAt > now
      || !Number.isFinite(value.expiresAt) || value.expiresAt <= now) throw new Error("invalid credential record");
    const record = recordFor(value.token, value.savedAt);
    record.expiresAt = Math.min(record.expiresAt, value.expiresAt);
    if (record.expiresAt <= now) throw new Error("expired credential record");
    return record;
  } catch {
    storage.removeItem(TOKEN_KEY);
    return null;
  }
}

export function saveCredential(storage, value, now = Date.now()) {
  const record = recordFor(value, now);
  const previous = loadCredential(storage, now);
  // Reusing a token must not turn its one-year retention into sliding expiry.
  if (previous?.token === record.token) {
    record.savedAt = previous.savedAt;
    record.expiresAt = Math.min(record.expiresAt, previous.expiresAt);
  }
  storage.setItem(TOKEN_KEY, JSON.stringify(record));
  return record;
}

export function credentialExpired(value, expiresAt, now = Date.now()) {
  const deadline = Math.min(expiresAt || Infinity, jwtExpiry(cleanToken(value)) ?? Infinity);
  return deadline <= now;
}
