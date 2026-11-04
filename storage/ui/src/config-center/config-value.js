export function cloneConfigValue(value) {
  if (Array.isArray(value)) {
    return value.map((item) => cloneConfigValue(item));
  }
  if (isPlainConfigObject(value)) {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, cloneConfigValue(item)]),
    );
  }
  return value;
}

export function deepMerge(base, override) {
  if (!isPlainConfigObject(base) || !isPlainConfigObject(override)) {
    return cloneConfigValue(override);
  }

  const result = cloneConfigValue(base);
  Object.entries(override).forEach(([key, value]) => {
    result[key] = Object.hasOwn(result, key)
      ? deepMerge(result[key], value)
      : cloneConfigValue(value);
  });
  return result;
}

export function buildEnvironmentTree(environments) {
  const knownIDs = new Set(environments.map((environment) => environment.id));
  const children = new Map();
  environments.forEach((environment) => {
    const parentID = knownIDs.has(environment.parent_id) ? environment.parent_id : 0;
    const siblings = children.get(parentID) ?? [];
    siblings.push(environment);
    children.set(parentID, siblings);
  });
  children.forEach((siblings) => {
    siblings.sort((left, right) => left.name.localeCompare(right.name, "zh-CN"));
  });

  const rows = [];
  const visited = new Set();
  const appendChildren = (parentID, ancestorContinuations = []) => {
    const siblings = children.get(parentID) ?? [];
    siblings.forEach((environment, index) => {
      if (visited.has(environment.id)) return;
      visited.add(environment.id);
      const isLast = index === siblings.length - 1;
      rows.push({
        environment,
        depth: ancestorContinuations.length,
        ancestorContinuations,
        isLast,
        hasChildren: (children.get(environment.id)?.length ?? 0) > 0,
        hasPrevious: rows.length > 0,
      });
      appendChildren(environment.id, [...ancestorContinuations, !isLast]);
    });
  };

  appendChildren(0);
  return rows;
}

export function buildEnvironmentDisplayTree(environments) {
  if (environments.length === 0) return [];

  const environmentRows = buildEnvironmentTree(environments).map((row) => ({
    ...row,
    depth: row.depth + 1,
    ancestorContinuations: [false, ...row.ancestorContinuations],
    hasPrevious: true,
  }));

  return [environmentRootRow(), ...environmentRows];
}

export function buildParentEnvironmentTree(environments) {
  const rows = buildEnvironmentDisplayTree(environments);
  return rows.length ? rows : [environmentRootRow()];
}

export function formatEnvironmentTreeLabel(row) {
  const ancestorLines = row.ancestorContinuations
    .slice(0, -1)
    .map((continues) => continues ? "│ " : "  ")
    .join("");
  const branch = row.depth === 0 ? "● " : row.isLast ? "└─● " : "├─● ";
  return `${ancestorLines}${branch}${row.environment.name}（${row.environment.slug}）`;
}

export function buildComparisonSourceRows(environments, releases) {
  const releasesByEnvironment = new Map();
  releases.forEach((release) => {
    const environmentReleases = releasesByEnvironment.get(release.environment_id) ?? [];
    environmentReleases.push(release);
    releasesByEnvironment.set(release.environment_id, environmentReleases);
  });
  releasesByEnvironment.forEach((environmentReleases) => {
    environmentReleases.sort((left, right) => right.version - left.version);
  });

  return buildEnvironmentDisplayTree(environments).map((row) => {
    const environmentReleases = releasesByEnvironment.get(row.environment.id) ?? [];
    const showDraft = row.environment.has_draft !== false || environmentReleases.length === 0;
    return {
      ...row,
      sources: row.virtual ? [] : [
        ...(showDraft ? [{
          value: `draft:${row.environment.id}`,
          label: "当前草稿",
          fullLabel: `${row.environment.name}（${row.environment.slug}） · 当前草稿`,
        }] : []),
        ...environmentReleases.map((release) => ({
          value: `release:${row.environment.id}:${release.version}`,
          label: `已发布 v${release.version}`,
          fullLabel: `${row.environment.name}（${row.environment.slug}） · 已发布 v${release.version}`,
          release,
        })),
      ],
    };
  });
}

export function defaultComparisonSources(environments, currentEnvironmentID, releases) {
  const current = environments.find((environment) => environment.id === currentEnvironmentID);
  if (!current) return { left: "", right: "" };

  const right = preferredComparisonSource(current, releases);
  if (current.parent_id) {
    const parent = environments.find((environment) => environment.id === current.parent_id);
    return { left: preferredComparisonSource(parent, releases), right };
  }

  const siblings = environments
    .filter((environment) => environment.parent_id === current.parent_id && environment.id !== current.id)
    .sort((left, rightEnvironment) => left.name.localeCompare(rightEnvironment.name, "zh-CN"));
  if (siblings.length) {
    return { left: preferredComparisonSource(siblings[0], releases), right };
  }

  return { left: right, right };
}

function preferredComparisonSource(environment, releases) {
  if (!environment) return "";
  const latestRelease = releases
    .filter((release) => release.environment_id === environment.id)
    .sort((left, right) => right.version - left.version)[0];
  if (environment.has_draft !== false || !latestRelease) return `draft:${environment.id}`;
  return `release:${environment.id}:${latestRelease.version}`;
}

export class ConfigTypeMemory {
  constructor() {
    this.values = new Map();
    this.types = new Map();
  }

  clear() {
    this.values.clear();
    this.types.clear();
  }

  remember(path, type, value) {
    const pathKey = JSON.stringify(path);
    const rememberedTypes = this.values.get(pathKey) ?? new Map();
    rememberedTypes.set(type, cloneConfigValue(value));
    this.values.set(pathKey, rememberedTypes);
  }

  restore(path, type, fallback) {
    const rememberedTypes = this.values.get(JSON.stringify(path));
    return rememberedTypes?.has(type)
      ? cloneConfigValue(rememberedTypes.get(type))
      : cloneConfigValue(fallback);
  }

  select(path, type) {
    this.types.set(JSON.stringify(path), type);
  }

  resolve(path, value) {
    const storedType = configValueType(value);
    if (storedType !== "string") return storedType;

    const selectedType = this.types.get(JSON.stringify(path));
    if (selectedType === "string" || selectedType === "long_text") return selectedType;
    return value.includes("\n") || value.includes("\r") ? "long_text" : "string";
  }
}

export function configValueType(value) {
  if (Array.isArray(value)) return "array";
  if (value !== null && typeof value === "object") return "object";
  if (typeof value === "boolean") return "bool";
  if (typeof value === "number") return "number";
  return "string";
}

export function defaultConfigValue(type) {
  return { object: {}, array: [], string: "", long_text: "", bool: false, number: 0 }[type];
}

function isPlainConfigObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function environmentRootRow() {
  return {
    environment: {
      id: "config-root",
      name: "配置根环境",
      slug: "",
      parent_id: 0,
    },
    depth: 0,
    ancestorContinuations: [],
    isLast: true,
    hasChildren: true,
    hasPrevious: false,
    virtual: true,
  };
}
