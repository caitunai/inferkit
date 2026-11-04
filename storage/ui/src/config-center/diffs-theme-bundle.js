import { createThemeCollection } from "@pierre/theming";
import { normalizeTheme } from "shiki/core";

function unwrapDefault(value) {
  return value?.default ?? value;
}

export function createTheme({ name, load, colorScheme, collection, displayName }) {
  return {
    name,
    colorScheme,
    collection,
    displayName,
    load: async () => normalizeTheme(unwrapDefault(await load())),
  };
}

export const pierreThemes = createThemeCollection({
  themes: [
    createTheme({
      name: "pierre-light",
      colorScheme: "light",
      collection: "pierre",
      displayName: "Pierre Light",
      load: () => import("@pierre/theme/pierre-light"),
    }),
  ],
});

// The configuration center exposes one fixed theme, so fallback Shiki themes
// are intentionally unavailable to @pierre/diffs.
export const shikiThemes = createThemeCollection({ themes: [] });
export const themes = createThemeCollection({ themes: [pierreThemes] });
