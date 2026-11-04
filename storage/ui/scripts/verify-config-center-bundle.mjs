import { readFile } from "node:fs/promises";
import { basename } from "node:path";

const manifest = JSON.parse(await readFile(new URL("../dist/manifest.json", import.meta.url), "utf8"));
const allowedLanguages = new Set(["dotenv", "ini", "json", "toml", "yaml"]);
const languageEntries = Object.keys(manifest).filter((entry) => entry.includes("node_modules/@shikijs/langs/"));
const bundledLanguages = new Set(languageEntries.map((entry) => basename(entry, ".mjs")));
const unexpectedLanguages = [...bundledLanguages].filter((language) => !allowedLanguages.has(language));
const missingLanguages = [...allowedLanguages].filter((language) => !bundledLanguages.has(language));
const shikiThemeEntries = Object.keys(manifest).filter((entry) => entry.includes("node_modules/@shikijs/themes/"));
const pierreThemeEntries = Object.keys(manifest).filter((entry) => entry.includes("node_modules/@pierre/theme/"));
const bundledPierreThemes = new Set(pierreThemeEntries.map((entry) => basename(entry, ".mjs")));

if (unexpectedLanguages.length > 0 || missingLanguages.length > 0) {
  throw new Error([
    `unexpected Shiki languages: ${unexpectedLanguages.join(", ") || "none"}`,
    `missing Shiki languages: ${missingLanguages.join(", ") || "none"}`,
  ].join("; "));
}

if (shikiThemeEntries.length > 0 || bundledPierreThemes.size !== 1 || !bundledPierreThemes.has("pierre-light")) {
  throw new Error([
    `unexpected Shiki theme count: ${shikiThemeEntries.length}`,
    `Pierre themes: ${[...bundledPierreThemes].join(", ") || "none"}`,
  ].join("; "));
}

console.log(`Verified ${bundledLanguages.size} syntax languages and the pierre-light theme.`);
