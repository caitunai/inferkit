import {
  createBundledHighlighter,
  createCssVariablesTheme,
  createSingletonShorthands,
  getTokenStyleObject,
  stringifyTokenStyle,
} from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";
import { createOnigurumaEngine } from "shiki/engine/oniguruma";

// @pierre/diffs resolves languages through Shiki's bundledLanguages map. Keep
// this app-specific bundle explicit so Vite does not emit every Shiki grammar.
export const bundledLanguages = Object.freeze({
  dotenv: () => import("@shikijs/langs/dotenv"),
  ini: () => import("@shikijs/langs/ini"),
  json: () => import("@shikijs/langs/json"),
  toml: () => import("@shikijs/langs/toml"),
  yaml: () => import("@shikijs/langs/yaml"),
});

const bundledThemes = Object.freeze({});

export const createHighlighter = createBundledHighlighter({
  langs: bundledLanguages,
  themes: bundledThemes,
  engine: () => createJavaScriptRegexEngine(),
});

export const { codeToHtml } = createSingletonShorthands(createHighlighter);

export {
  createCssVariablesTheme,
  createJavaScriptRegexEngine,
  createOnigurumaEngine,
  getTokenStyleObject,
  stringifyTokenStyle,
};
