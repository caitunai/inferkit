import assert from "node:assert/strict";
import test from "node:test";
import { TOKEN_KEY, TOKEN_LIFETIME_MS, credentialExpired, loadCredential, saveCredential } from "./credential.js";

const now = 1800000000000;
function storage() {
  const data = new Map();
  return { getItem: (key) => data.get(key) ?? null, setItem: (key, value) => data.set(key, value), removeItem: (key) => data.delete(key) };
}
const jwt = (expiresAt) => `e30.${Buffer.from(JSON.stringify({ exp: expiresAt / 1000 })).toString("base64url")}.signature`;

test("credential retention defaults to one year and expires automatically", () => {
  const store = storage();
  const saved = saveCredential(store, "Bearer test-token", now);
  assert.equal(saved.token, "test-token");
  assert.equal(saved.expiresAt, now + TOKEN_LIFETIME_MS);
  assert.deepEqual(loadCredential(store, now + 1000), saved);
  assert.equal(loadCredential(store, now + TOKEN_LIFETIME_MS), null);
  assert.equal(store.getItem(TOKEN_KEY), null);
});

test("reusing the same credential does not extend its original retention", () => {
  const store = storage();
  const original = saveCredential(store, "test-token", now);
  assert.deepEqual(saveCredential(store, "test-token", now + 86400000), original);
  const replaced = saveCredential(store, "other-token", now + 86400000);
  assert.equal(replaced.expiresAt, now + 86400000 + TOKEN_LIFETIME_MS);
});

test("JWT expiry is honored earlier than local retention and expired tokens cannot be saved", () => {
  const store = storage();
  const token = jwt(now + 3600000);
  assert.equal(saveCredential(store, token, now).expiresAt, now + 3600000);
  assert.equal(credentialExpired(token, 0, now + 3600000), true);
  assert.equal(loadCredential(store, now + 3600000), null);
  assert.throws(() => saveCredential(store, token, now + 3600000), /过期/);
  assert.equal(saveCredential(store, jwt(now + 2 * TOKEN_LIFETIME_MS), now).expiresAt, now + TOKEN_LIFETIME_MS);
});

test("corrupt storage is removed and excessive retention is bounded", () => {
  const store = storage();
  store.setItem(TOKEN_KEY, "invalid JSON");
  assert.equal(loadCredential(store, now), null);
  assert.equal(store.getItem(TOKEN_KEY), null);
  store.setItem(TOKEN_KEY, JSON.stringify({ token: "test", savedAt: now, expiresAt: now + 10 * TOKEN_LIFETIME_MS }));
  assert.equal(loadCredential(store, now).expiresAt, now + TOKEN_LIFETIME_MS);
});
