import assert from "node:assert/strict";
import test from "node:test";

import { pierreThemes, shikiThemes } from "./diffs-theme-bundle.js";
import { bundledLanguages } from "./shiki-bundle.js";

test("configuration diffs expose only supported syntax languages", () => {
  assert.deepEqual(Object.keys(bundledLanguages).sort(), [
    "dotenv",
    "ini",
    "json",
    "toml",
    "yaml",
  ]);
});

test("configuration diffs expose only the selected Pierre theme", async () => {
  assert.deepEqual(pierreThemes.getThemeNames(), ["pierre-light"]);
  assert.deepEqual(shikiThemes.getThemeNames(), []);

  const theme = await pierreThemes.getTheme("pierre-light").load();
  assert.equal(theme.name, "pierre-light");
  assert.equal(theme.type, "light");
});
