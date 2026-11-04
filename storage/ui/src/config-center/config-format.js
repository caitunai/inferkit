import { cloneConfigValue } from "./config-value.js";

export const CONFIG_FORMATS = [
  { value: "json", label: "JSON", extension: "json" },
  { value: "yaml", label: "YAML", extension: "yaml" },
  { value: "toml", label: "TOML", extension: "toml" },
  { value: "env", label: ".ENV", extension: "env" },
  { value: "ini", label: "INI", extension: "ini" },
];

export function pathToPointer(path) {
  return path.map((segment) => `/${String(segment).replaceAll("~", "~0").replaceAll("/", "~1")}`).join("");
}

export function remapDescriptionPrefix(descriptions, oldPath, newPath) {
  const oldPrefix = pathToPointer(oldPath);
  const newPrefix = pathToPointer(newPath);
  const result = {};
  Object.entries(descriptions).forEach(([pointer, description]) => {
    if (pointer === oldPrefix || pointer.startsWith(`${oldPrefix}/`)) {
      result[`${newPrefix}${pointer.slice(oldPrefix.length)}`] = description;
    } else {
      result[pointer] = description;
    }
  });
  return result;
}

export function removeDescriptionSubtree(descriptions, path, keepRoot = false) {
  const prefix = pathToPointer(path);
  return Object.fromEntries(Object.entries(descriptions).filter(([pointer]) => (
    pointer !== prefix && !pointer.startsWith(`${prefix}/`)
  ) || (keepRoot && pointer === prefix)));
}

export function moveArrayDescriptionItem(descriptions, parentPath, from, to) {
  if (from === to) return { ...descriptions };
  const fromPath = [...parentPath, from];
  const toPath = [...parentPath, to];
  const temporaryPath = [...parentPath, "__config_move__"];
  return remapDescriptionPrefix(
    remapDescriptionPrefix(
      remapDescriptionPrefix(descriptions, fromPath, temporaryPath),
      toPath,
      fromPath,
    ),
    temporaryPath,
    toPath,
  );
}

export function removeArrayDescriptionItem(descriptions, parentPath, removedIndex, previousLength) {
  let result = removeDescriptionSubtree(descriptions, [...parentPath, removedIndex]);
  for (let index = removedIndex + 1; index < previousLength; index += 1) {
    result = remapDescriptionPrefix(result, [...parentPath, index], [...parentPath, index - 1]);
  }
  return result;
}

export function mergeConfigState(baseConfig, baseDescriptions, overrideConfig, overrideDescriptions) {
  return mergeValue(
    cloneConfigValue(baseConfig),
    { ...baseDescriptions },
    cloneConfigValue(overrideConfig),
    { ...overrideDescriptions },
    [],
  );
}

function mergeValue(base, descriptions, override, overrideDescriptions, path) {
  const pointer = pathToPointer(path);
  if (!isObject(base) || !isObject(override)) {
    const nextDescriptions = removeDescriptionSubtree(descriptions, path);
    copyDescriptionSubtree(nextDescriptions, overrideDescriptions, pointer);
    return { config: cloneConfigValue(override), descriptions: nextDescriptions };
  }

  const result = cloneConfigValue(base);
  const nextDescriptions = { ...descriptions };
  if (pointer && Object.hasOwn(overrideDescriptions, pointer)) {
    nextDescriptions[pointer] = overrideDescriptions[pointer];
  }
  Object.entries(override).forEach(([key, value]) => {
    const childPath = [...path, key];
    if (Object.hasOwn(result, key)) {
      const merged = mergeValue(result[key], nextDescriptions, value, overrideDescriptions, childPath);
      result[key] = merged.config;
      Object.keys(nextDescriptions).forEach((descriptionPointer) => delete nextDescriptions[descriptionPointer]);
      Object.assign(nextDescriptions, merged.descriptions);
    } else {
      result[key] = cloneConfigValue(value);
      copyDescriptionSubtree(nextDescriptions, overrideDescriptions, pathToPointer(childPath));
    }
  });
  return { config: result, descriptions: nextDescriptions };
}

function copyDescriptionSubtree(target, source, prefix) {
  Object.entries(source).forEach(([pointer, description]) => {
    if (!prefix || pointer === prefix || pointer.startsWith(`${prefix}/`)) {
      target[pointer] = description;
    }
  });
}

export function formatConfig(config, descriptions = {}, format = "json") {
  const plainConfig = cloneConfigValue(config);
  const plainDescriptions = { ...descriptions };
  switch (format) {
    case "yaml": return formatYAML(plainConfig, plainDescriptions);
    case "toml": return formatTOML(plainConfig, plainDescriptions);
    case "env": return formatENV(plainConfig, plainDescriptions);
    case "ini": return formatINI(plainConfig, plainDescriptions);
    default:
      return JSON.stringify({ config: plainConfig, descriptions: plainDescriptions }, null, 2);
  }
}

function formatYAML(config, descriptions) {
  return `${yamlObject(config, descriptions, [], 0).join("\n")}\n`;
}

function yamlObject(object, descriptions, path, indent) {
  const lines = [];
  Object.entries(object).forEach(([key, value]) => {
    const childPath = [...path, key];
    appendDescription(lines, descriptions[pathToPointer(childPath)], indent, "# ");
    const prefix = `${" ".repeat(indent)}${yamlKey(key)}:`;
    if (isObject(value)) {
      lines.push(prefix);
      lines.push(...yamlObject(value, descriptions, childPath, indent + 2));
    } else if (Array.isArray(value)) {
      if (value.length === 0) lines.push(`${prefix} []`);
      else {
        lines.push(prefix);
        lines.push(...yamlArray(value, descriptions, childPath, indent + 2));
      }
    } else {
      lines.push(`${prefix} ${yamlScalar(value)}`);
    }
  });
  return lines;
}

function yamlArray(array, descriptions, path, indent) {
  const lines = [];
  array.forEach((value, index) => {
    const childPath = [...path, index];
    appendDescription(lines, descriptions[pathToPointer(childPath)], indent, "# ");
    const prefix = `${" ".repeat(indent)}-`;
    if (isObject(value)) {
      lines.push(prefix);
      lines.push(...yamlObject(value, descriptions, childPath, indent + 2));
    } else if (Array.isArray(value)) {
      if (value.length === 0) lines.push(`${prefix} []`);
      else {
        lines.push(prefix);
        lines.push(...yamlArray(value, descriptions, childPath, indent + 2));
      }
    } else {
      lines.push(`${prefix} ${yamlScalar(value)}`);
    }
  });
  return lines;
}

function yamlKey(key) {
  return /^[A-Za-z_][A-Za-z0-9_-]*$/.test(key) ? key : JSON.stringify(key);
}

function yamlScalar(value) {
  if (typeof value === "string") return JSON.stringify(value);
  return String(value);
}

function formatTOML(config, descriptions) {
  const lines = descriptionIndex(descriptions);
  appendTOMLTable(lines, config, []);
  return `${lines.join("\n").trimEnd()}\n`;
}

function appendTOMLTable(lines, object, path) {
  const scalars = Object.entries(object).filter(([, value]) => !isObject(value));
  const tables = Object.entries(object).filter(([, value]) => isObject(value));
  if (path.length) {
    if (lines.length && lines.at(-1) !== "") lines.push("");
    lines.push(`[${path.map(tomlKey).join(".")}]`);
  }
  scalars.forEach(([key, value]) => lines.push(`${tomlKey(key)} = ${tomlValue(value)}`));
  tables.forEach(([key, value]) => appendTOMLTable(lines, value, [...path, key]));
}

function tomlKey(key) {
  return /^[A-Za-z0-9_-]+$/.test(key) ? key : JSON.stringify(key);
}

function tomlValue(value) {
  if (Array.isArray(value)) return `[${value.map(tomlValue).join(", ")}]`;
  if (isObject(value)) {
    return `{ ${Object.entries(value).map(([key, child]) => `${tomlKey(key)} = ${tomlValue(child)}`).join(", ")} }`;
  }
  if (typeof value === "string") return JSON.stringify(value);
  return String(value);
}

function formatENV(config, descriptions) {
  const lines = descriptionIndex(descriptions);
  flattenValues(config).forEach(({ path, value }) => {
    const key = path.map((segment) => String(segment).replace(/[^A-Za-z0-9]+/g, "_").toUpperCase()).join("__");
    lines.push(`${key}=${envValue(value)}`);
  });
  return `${lines.join("\n").trimEnd()}\n`;
}

function envValue(value) {
  if (typeof value === "string") return JSON.stringify(value);
  if (Array.isArray(value) || isObject(value)) return JSON.stringify(value);
  return String(value);
}

function formatINI(config, descriptions) {
  const lines = descriptionIndex(descriptions, "; ");
  const rootValues = Object.entries(config).filter(([, value]) => !isObject(value));
  rootValues.forEach(([key, value]) => lines.push(`${key}=${iniValue(value)}`));
  Object.entries(config).filter(([, value]) => isObject(value)).forEach(([section, value]) => {
    if (lines.length && lines.at(-1) !== "") lines.push("");
    lines.push(`[${section}]`);
    flattenValues(value).forEach(({ path, value: child }) => lines.push(`${path.join(".")}=${iniValue(child)}`));
  });
  return `${lines.join("\n").trimEnd()}\n`;
}

function iniValue(value) {
  if (typeof value === "string") return JSON.stringify(value);
  if (Array.isArray(value) || isObject(value)) return JSON.stringify(value);
  return String(value);
}

function flattenValues(value, path = [], result = []) {
  if (isObject(value)) {
    Object.entries(value).forEach(([key, child]) => flattenValues(child, [...path, key], result));
  } else {
    result.push({ path, value });
  }
  return result;
}

function descriptionIndex(descriptions, marker = "# ") {
  const entries = Object.entries(descriptions).sort(([left], [right]) => left.localeCompare(right));
  if (!entries.length) return [];
  return [
    `${marker}配置项描述`,
    ...entries.flatMap(([pointer, description]) => description.split("\n").map((line) => `${marker}${pointer}: ${line}`)),
    "",
  ];
}

function appendDescription(lines, description, indent, marker) {
  if (!description) return;
  description.split("\n").forEach((line) => lines.push(`${" ".repeat(indent)}${marker}${line}`));
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
