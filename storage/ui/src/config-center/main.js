import Alpine from "alpinejs";
import {
  buildComparisonSourceRows,
  buildEnvironmentDisplayTree,
  buildParentEnvironmentTree,
  cloneConfigValue,
  ConfigTypeMemory,
  defaultConfigValue,
  defaultComparisonSources,
} from "./config-value.js";
import {
  CONFIG_FORMATS,
  formatConfig,
  mergeConfigState,
  moveArrayDescriptionItem,
  pathToPointer,
  removeArrayDescriptionItem,
  removeDescriptionSubtree,
  remapDescriptionPrefix,
} from "./config-format.js";
import { calculateDropdownPosition } from "./dropdown-position.js";
import "./main.css";

const API_ROOT = "/config-center/api";
const DISPLAY_FORMAT_STORAGE_KEY = "config-center.display-format";
let diffsModulePromise;

class ConfigTreeEditor {
  constructor(root, value, descriptions, onChange, onError, typeMemory) {
    this.root = root;
    this.value = value;
    this.descriptions = descriptions;
    this.onChange = onChange;
    this.onError = onError;
    this.typeMemory = typeMemory;
    this.descriptionMemory = new Map();
    this.activeDescriptionControl = null;
    this.activeDropdownControl = null;
    this.descriptionSequence = 0;
    this.handleDescriptionPointerDown = (event) => {
      if (this.activeDescriptionControl?.wrapper.contains(event.target)) return;
      this.closeDescriptionPopover();
    };
    this.handleDescriptionKeyDown = (event) => {
      if (event.key !== "Escape" || !this.activeDescriptionControl) return;
      event.preventDefault();
      this.closeDescriptionPopover(true);
    };
    this.handleDropdownViewportChange = () => this.positionActiveDropdown();
    document.addEventListener("pointerdown", this.handleDescriptionPointerDown);
    document.addEventListener("keydown", this.handleDescriptionKeyDown);
    window.addEventListener("resize", this.handleDropdownViewportChange);
    window.addEventListener("scroll", this.handleDropdownViewportChange, true);
  }

  render(value, descriptions = this.descriptions) {
    this.closeDescriptionPopover();
    this.closeDropdown();
    this.value = value;
    this.descriptions = descriptions;
    this.root.replaceChildren();
    const rootNode = document.createElement("div");
    rootNode.className = "config-root";
    this.renderCollection(rootNode, [], value, true);
    this.root.append(rootNode);
  }

  renderCollection(container, path, value, isRoot = false) {
    const entries = Array.isArray(value) ? value.map((item, index) => [index, item]) : Object.entries(value);
    const collection = document.createElement("div");
    collection.className = isRoot ? "config-collection" : "config-collection config-collection-nested";
    collection.dataset.depth = String(path.length % 6);

    if (entries.length === 0) {
      const empty = document.createElement("div");
      empty.className = "config-empty";
      empty.textContent = Array.isArray(value) ? "数组为空" : "对象暂无字段";
      collection.append(empty);
    }

    entries.forEach(([key, child], index) => {
      collection.append(this.renderNode(path, key, child, Array.isArray(value), index, entries.length));
    });

    const addButton = this.button(Array.isArray(value) ? "＋ 添加数组项" : "＋ 添加字段", "config-add");
    addButton.addEventListener("click", () => {
      const target = this.at(path);
      if (Array.isArray(target)) {
        target.push("");
      } else {
        const key = this.nextKey(target);
        target[key] = "";
      }
      this.commit();
    });
    collection.append(addButton);
    container.append(collection);
  }

  renderNode(parentPath, key, value, parentIsArray, index, length) {
    const path = [...parentPath, key];
    const type = this.typeMemory.resolve(path, value);
    const wrapper = document.createElement("div");
    wrapper.className = "config-node";
    const row = document.createElement("div");
    row.className = "config-row";
    row.classList.toggle("config-row-long-text", type === "long_text");

    if (parentIsArray) {
      const indexLabel = document.createElement("span");
      indexLabel.className = "config-index";
      indexLabel.textContent = String(Number(key) + 1);
      row.append(indexLabel);
    } else {
      const keyInput = document.createElement("input");
      keyInput.className = "config-key";
      keyInput.value = key;
      keyInput.maxLength = 128;
      keyInput.setAttribute("aria-label", "配置键名");
      keyInput.addEventListener("change", () => this.renameKey(parentPath, key, keyInput.value, keyInput));
      row.append(keyInput);
    }

    const typeOptions = [
      ["object", "对象"], ["array", "数组"], ["string", "字符串"],
      ["long_text", "长文本"], ["bool", "布尔值"], ["number", "数字"],
    ].map(([optionValue, label]) => ({ value: optionValue, label }));
    const typeDropdown = this.dropdown({
      value: type,
      options: typeOptions,
      className: "config-type-dropdown",
      ariaLabel: "配置值类型",
      onChange: (nextType) => {
        const previousType = this.typeMemory.resolve(path, this.at(path));
        if (previousType === nextType) return;
        this.typeMemory.remember(path, previousType, this.at(path));
        this.rememberDescriptionSubtree(path, previousType);
        this.typeMemory.select(path, nextType);
        this.replace(path, this.typeMemory.restore(path, nextType, defaultConfigValue(nextType)));
        this.descriptions = removeDescriptionSubtree(this.descriptions, path, true);
        this.restoreDescriptionSubtree(path, nextType);
        this.commit();
      },
    });
    row.append(typeDropdown);

    row.append(this.renderValueInput(path, value, type));

    const actions = document.createElement("div");
    actions.className = "config-actions";
    actions.append(this.renderDescriptionControl(path, key));
    if (parentIsArray) {
      const up = this.button("↑", "config-action", "上移");
      up.disabled = index === 0;
      up.addEventListener("click", () => this.moveArrayItem(parentPath, index, index - 1));
      const down = this.button("↓", "config-action", "下移");
      down.disabled = index === length - 1;
      down.addEventListener("click", () => this.moveArrayItem(parentPath, index, index + 1));
      actions.append(up, down);
    }
    const remove = this.button("×", "config-action config-remove", "删除");
    remove.addEventListener("click", () => {
      const parent = this.at(parentPath);
      if (Array.isArray(parent)) {
        this.descriptions = removeArrayDescriptionItem(
          this.descriptions,
          parentPath,
          Number(key),
          parent.length,
        );
        parent.splice(Number(key), 1);
      } else {
        this.descriptions = removeDescriptionSubtree(this.descriptions, path);
        delete parent[key];
      }
      this.commit();
    });
    actions.append(remove);
    row.append(actions);
    wrapper.append(row);

    if (isCollection(value)) {
      const nested = document.createElement("div");
      nested.className = "config-nested";
      nested.dataset.depth = String(path.length % 6);
      this.renderCollection(nested, path, value);
      wrapper.append(nested);
    }
    return wrapper;
  }

  renderDescriptionControl(path, key) {
    const pointer = pathToPointer(path);
    const currentDescription = this.descriptions[pointer] ?? "";
    const wrapper = document.createElement("div");
    wrapper.className = "config-description-control";

    const trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "config-action config-description-trigger";
    trigger.classList.toggle("config-description-trigger-has-value", Boolean(currentDescription.trim()));
    trigger.title = currentDescription.trim() || "添加配置说明";
    trigger.setAttribute("aria-label", `${currentDescription.trim() ? "编辑" : "添加"}配置项 ${String(key)} 的说明`);
    trigger.setAttribute("aria-haspopup", "dialog");
    trigger.setAttribute("aria-expanded", "false");
    trigger.append(this.descriptionIcon());

    const popover = document.createElement("div");
    const popoverID = `config-description-popover-${++this.descriptionSequence}`;
    popover.id = popoverID;
    popover.className = "config-description-popover";
    popover.hidden = true;
    popover.setAttribute("role", "dialog");
    popover.setAttribute("aria-label", `配置项 ${String(key)} 的说明`);
    trigger.setAttribute("aria-controls", popoverID);

    const header = document.createElement("div");
    header.className = "config-description-popover-header";
    const title = document.createElement("strong");
    title.textContent = "配置说明";
    const close = this.button("×", "config-description-close", "关闭说明编辑框");
    header.append(title, close);

    const description = document.createElement("textarea");
    description.className = "config-description-input";
    description.rows = 4;
    description.maxLength = 2000;
    description.placeholder = "说明这个配置项的用途、取值约束或注意事项（可选）";
    description.setAttribute("aria-label", `配置项 ${String(key)} 的说明内容`);
    description.value = currentDescription;

    const count = document.createElement("span");
    count.className = "config-description-count";
    count.textContent = `${description.value.length}/2000`;

    const control = { wrapper, trigger, popover, description };
    trigger.addEventListener("click", () => {
      if (this.activeDescriptionControl === control) {
        this.closeDescriptionPopover(true);
        return;
      }
      this.openDescriptionPopover(control);
    });
    close.addEventListener("click", () => this.closeDescriptionPopover(true));
    description.addEventListener("input", () => {
      const nextDescription = description.value;
      if (nextDescription.trim()) this.descriptions[pointer] = nextDescription;
      else delete this.descriptions[pointer];
      const hasDescription = Boolean(nextDescription.trim());
      trigger.classList.toggle("config-description-trigger-has-value", hasDescription);
      trigger.title = hasDescription ? nextDescription.trim() : "添加配置说明";
      trigger.setAttribute("aria-label", `${hasDescription ? "编辑" : "添加"}配置项 ${String(key)} 的说明`);
      count.textContent = `${nextDescription.length}/2000`;
      this.commit(false);
    });

    popover.append(header, description, count);
    wrapper.append(trigger, popover);
    return wrapper;
  }

  descriptionIcon() {
    const icon = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    icon.setAttribute("viewBox", "0 0 24 24");
    icon.setAttribute("aria-hidden", "true");
    icon.classList.add("config-description-icon");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", "M6.75 4.75h10.5a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2H11l-4.5 3v-3.1a2 2 0 0 1-1.75-1.98V6.75a2 2 0 0 1 2-2Z");
    icon.append(path);
    return icon;
  }

  openDescriptionPopover(control) {
    this.closeDescriptionPopover();
    this.activeDescriptionControl = control;
    control.popover.hidden = false;
    control.trigger.setAttribute("aria-expanded", "true");
    control.wrapper.classList.add("config-description-control-open");
    control.description.focus({ preventScroll: true });
    control.description.setSelectionRange(control.description.value.length, control.description.value.length);
  }

  closeDescriptionPopover(restoreFocus = false) {
    const control = this.activeDescriptionControl;
    if (!control) return;
    control.popover.hidden = true;
    control.trigger.setAttribute("aria-expanded", "false");
    control.wrapper.classList.remove("config-description-control-open");
    this.activeDescriptionControl = null;
    if (restoreFocus && control.trigger.isConnected) control.trigger.focus({ preventScroll: true });
  }

  destroy() {
    this.closeDescriptionPopover();
    this.closeDropdown();
    document.removeEventListener("pointerdown", this.handleDescriptionPointerDown);
    document.removeEventListener("keydown", this.handleDescriptionKeyDown);
    window.removeEventListener("resize", this.handleDropdownViewportChange);
    window.removeEventListener("scroll", this.handleDropdownViewportChange, true);
  }

  renderValueInput(path, value, type) {
    if (type === "object" || type === "array") {
      const summary = document.createElement("span");
      summary.className = "config-summary";
      const size = type === "array" ? value.length : Object.keys(value).length;
      summary.textContent = `${size} ${type === "array" ? "项" : "个字段"}`;
      return summary;
    }
    if (type === "bool") {
      return this.dropdown({
        value: String(value),
        options: [
          { value: "true", label: "true" },
          { value: "false", label: "false" },
        ],
        className: "config-value-dropdown",
        ariaLabel: "布尔值",
        onChange: (nextValue) => {
          this.replace(path, nextValue === "true");
          this.commit();
        },
      });
    }
    const input = document.createElement(type === "long_text" ? "textarea" : "input");
    input.className = "config-value";
    if (type === "number") {
      input.type = "number";
      input.step = "any";
    } else {
      if (type === "long_text") {
        input.classList.add("config-value-long-text");
        input.rows = 4;
        input.placeholder = "输入支持换行的长文本";
        input.setAttribute("aria-label", "长文本配置值");
      } else {
        input.type = "text";
      }
      input.maxLength = 65536;
    }
    input.value = String(value);
    input.addEventListener("input", () => {
      if (type === "string" || type === "long_text") {
        this.replace(path, input.value);
        this.onChange(cloneConfigValue(this.value), { ...this.descriptions }, false);
      }
    });
    input.addEventListener("change", () => {
      if (type === "number") {
        const parsed = Number(input.value);
        if (!Number.isFinite(parsed)) {
          this.onError("请输入有效的数字");
          input.value = String(this.at(path));
          return;
        }
        if (Number.isInteger(parsed) && !Number.isSafeInteger(parsed)) {
          this.onError("整数部分超出了 JavaScript 可安全表示的范围");
          input.value = String(this.at(path));
          return;
        }
        this.replace(path, parsed);
      } else {
        this.replace(path, input.value);
      }
      this.commit(false);
    });
    return input;
  }

  renameKey(parentPath, oldKey, proposedKey, input) {
    const key = proposedKey.trim();
    const parent = this.at(parentPath);
    if (!key || key.length > 128) {
      this.onError("键名不能为空且不能超过 128 个字符");
      input.value = oldKey;
      return;
    }
    if (key !== oldKey && Object.hasOwn(parent, key)) {
      this.onError(`键名「${key}」已存在`);
      input.value = oldKey;
      return;
    }
    if (key === oldKey) return;
    const entries = Object.entries(parent).map(([entryKey, entryValue]) => entryKey === oldKey ? [key, entryValue] : [entryKey, entryValue]);
    Object.keys(parent).forEach((entryKey) => delete parent[entryKey]);
    entries.forEach(([entryKey, entryValue]) => { parent[entryKey] = entryValue; });
    this.descriptions = remapDescriptionPrefix(
      this.descriptions,
      [...parentPath, oldKey],
      [...parentPath, key],
    );
    this.commit();
  }

  moveArrayItem(parentPath, from, to) {
    const parent = this.at(parentPath);
    if (to < 0 || to >= parent.length) return;
    this.descriptions = moveArrayDescriptionItem(this.descriptions, parentPath, from, to);
    const [item] = parent.splice(from, 1);
    parent.splice(to, 0, item);
    this.commit();
  }

  at(path) {
    return path.reduce((current, segment) => current[segment], this.value);
  }

  replace(path, value) {
    const parent = this.at(path.slice(0, -1));
    parent[path.at(-1)] = value;
  }

  commit(render = true) {
    const snapshot = cloneConfigValue(this.value);
    const descriptionSnapshot = { ...this.descriptions };
    this.value = snapshot;
    this.descriptions = descriptionSnapshot;
    this.onChange(snapshot, descriptionSnapshot, render);
    if (render) this.render(snapshot, descriptionSnapshot);
  }

  rememberDescriptionSubtree(path, type) {
    const pointer = pathToPointer(path);
    const subtree = Object.fromEntries(Object.entries(this.descriptions).filter(([candidate]) => (
      candidate.startsWith(`${pointer}/`)
    )));
    this.descriptionMemory.set(`${JSON.stringify(path)}:${type}`, subtree);
  }

  restoreDescriptionSubtree(path, type) {
    const subtree = this.descriptionMemory.get(`${JSON.stringify(path)}:${type}`);
    if (subtree) Object.assign(this.descriptions, subtree);
  }

  nextKey(object) {
    let counter = 1;
    while (Object.hasOwn(object, `new_key_${counter}`)) counter += 1;
    return `new_key_${counter}`;
  }

  button(text, className, title = "") {
    const button = document.createElement("button");
    button.type = "button";
    button.className = className;
    button.textContent = text;
    if (title) button.title = title;
    return button;
  }

  dropdown({ value, options, className, ariaLabel, onChange }) {
    const wrapper = document.createElement("div");
    wrapper.className = `config-dropdown ${className}`;

    const trigger = this.button("", "config-dropdown-trigger");
    trigger.setAttribute("aria-label", ariaLabel);
    trigger.setAttribute("aria-haspopup", "listbox");
    trigger.setAttribute("aria-expanded", "false");
    const label = document.createElement("span");
    label.className = "config-dropdown-label";
    label.textContent = options.find((option) => option.value === value)?.label ?? value;
    const arrow = document.createElement("span");
    arrow.className = "dropdown-arrow";
    arrow.setAttribute("aria-hidden", "true");
    trigger.append(label, arrow);

    const menu = document.createElement("div");
    menu.className = "config-dropdown-menu";
    menu.setAttribute("role", "listbox");
    menu.hidden = true;
    const menuScroll = document.createElement("div");
    menuScroll.className = "select-menu-scroll";
    menu.append(menuScroll);

    const control = { wrapper, trigger, menu, menuScroll };
    const close = () => this.closeDropdown(control);
    const open = () => this.openDropdown(control);

    options.forEach((option) => {
      const item = this.button(option.label, "config-dropdown-option");
      item.setAttribute("role", "option");
      item.setAttribute("aria-selected", String(option.value === value));
      if (option.value === value) item.classList.add("config-dropdown-option-selected");
      item.addEventListener("click", () => {
        close();
        if (option.value !== value) onChange(option.value);
      });
      menuScroll.append(item);
    });

    trigger.addEventListener("click", () => menu.hidden ? open() : close());
    wrapper.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      close();
      trigger.focus();
    });
    wrapper.addEventListener("focusout", (event) => {
      if (!wrapper.contains(event.relatedTarget)) close();
    });
    wrapper.append(trigger, menu);
    return wrapper;
  }

  openDropdown(control) {
    this.closeDropdown();
    this.activeDropdownControl = control;
    control.menu.hidden = false;
    control.naturalMenuHeight = control.menu.scrollHeight;
    control.trigger.setAttribute("aria-expanded", "true");
    control.wrapper.classList.add("config-dropdown-open");
    this.positionActiveDropdown();
  }

  closeDropdown(control = this.activeDropdownControl) {
    if (!control) return;
    control.menu.hidden = true;
    control.trigger.setAttribute("aria-expanded", "false");
    control.wrapper.classList.remove("config-dropdown-open", "config-dropdown-up");
    control.menuScroll.style.removeProperty("max-height");
    if (this.activeDropdownControl === control) this.activeDropdownControl = null;
  }

  positionActiveDropdown() {
    const control = this.activeDropdownControl;
    if (!control) return;
    if (!control.trigger.isConnected) {
      this.closeDropdown(control);
      return;
    }
    const triggerRect = control.trigger.getBoundingClientRect();
    const position = calculateDropdownPosition({
      triggerTop: triggerRect.top,
      triggerBottom: triggerRect.bottom,
      viewportHeight: window.innerHeight,
      menuHeight: control.naturalMenuHeight,
    });
    control.wrapper.classList.toggle("config-dropdown-up", position.opensAbove);
    control.menuScroll.style.maxHeight = `${Math.max(48, position.maxHeight - 10)}px`;
  }
}

function configCenterApp() {
  let treeEditor = null;
  let diffViewer = null;
  const typeMemory = new ConfigTypeMemory();
  const toastTimers = new Set();

  return {
    namespaces: [], selectedNamespaceID: 0, loadingNamespaces: true, namespaceMenu: false,
    environments: [], selectedID: 0, publishIDs: [], inheritanceChain: [],
    draft: {}, draftDescriptions: {}, inherited: {}, inheritedDescriptions: {},
    finalConfig: {}, finalDescriptions: {}, publishedRelease: null,
    loadingEnvironments: true, loadingConfig: false, saving: false, publishing: false,
    publishedLoading: false, dirty: false, tab: "draft",
    publishedVersions: [], publishedVersion: 0, publishedVersionMenu: false,
    displayFormat: "json", formatMenu: "",
    configFormats: CONFIG_FORMATS,
    releases: [], comparisonLoading: false, comparisonError: "", comparisonMenu: "",
    comparisonLeft: "", comparisonRight: "", comparisonLeftData: null, comparisonRightData: null,
    namespaceModal: false, namespaceSaving: false,
    environmentModal: false, environmentSaving: false, parentSelectorOpen: false,
    sidebarHidden: false, confirming: false,
    toasts: [], toastSequence: 0,
    namespaceForm: { id: 0, name: "", slug: "", description: "", api_key: "", api_key_configured: false },
    environmentForm: { id: 0, name: "", slug: "", description: "", parent_id: 0 },
    confirmation: {
      open: false, kind: "", title: "", message: "", reason: "", confirmLabel: "确认", tone: "primary", targetID: 0, targetNamespaceID: 0, targets: [], urlMode: "push",
    },

    async init() {
      try {
        this.sidebarHidden = window.localStorage.getItem("config-center.sidebar-hidden") === "true";
        const savedFormat = window.localStorage.getItem(DISPLAY_FORMAT_STORAGE_KEY);
        if (CONFIG_FORMATS.some((format) => format.value === savedFormat)) {
          this.displayFormat = savedFormat;
        }
      } catch {
        this.sidebarHidden = false;
      }
      this.$watch("tab", (tab) => {
        if (tab === "draft") this.$nextTick(() => this.renderEditor());
        if (tab === "compare") this.$nextTick(() => this.renderComparisonDiff());
      });
      await this.loadNamespaces();
      if (this.namespaces.length > 0) {
        const requestedNamespaceID = this.namespaceIDFromURL();
        const initialNamespaceID = this.namespaces.some((namespace) => namespace.id === requestedNamespaceID)
          ? requestedNamespaceID
          : this.namespaces[0].id;
        await this.selectNamespace(initialNamespaceID, true, "replace", this.environmentIDFromURL());
      } else {
        this.loadingEnvironments = false;
      }
    },
    destroy() {
      toastTimers.forEach((timer) => window.clearTimeout(timer));
      toastTimers.clear();
      treeEditor?.destroy();
      treeEditor = null;
      diffViewer?.cleanUp();
      diffViewer = null;
      typeMemory.clear();
    },
    handleBeforeUnload(event) {
      if (!this.dirty) return;
      event.preventDefault();
      event.returnValue = "";
    },
    async handlePopState() {
      const requestedNamespaceID = this.namespaceIDFromURL();
      const requestedID = this.environmentIDFromURL();
      if (requestedNamespaceID !== this.selectedNamespaceID) {
        if (!this.namespaces.some((namespace) => namespace.id === requestedNamespaceID)) {
          this.syncWorkspaceURL(this.selectedNamespaceID, this.selectedID, "replace");
          return;
        }
        await this.selectNamespace(requestedNamespaceID, false, "none", requestedID);
        return;
      }
      if (!this.environments.some((environment) => environment.id === requestedID)) {
        this.syncWorkspaceURL(this.selectedNamespaceID, this.selectedID, "replace");
        return;
      }
      await this.selectEnvironment(requestedID, false, "none");
    },
    setSidebarHidden(hidden) {
      this.sidebarHidden = hidden;
      try {
        window.localStorage.setItem("config-center.sidebar-hidden", String(hidden));
      } catch {
        // The layout still works when browser storage is unavailable.
      }
      this.$nextTick(() => this.renderEditor());
    },

    get selectedNamespace() { return this.namespaces.find((item) => item.id === this.selectedNamespaceID) ?? null; },
    get selectedEnvironment() { return this.environments.find((item) => item.id === this.selectedID) ?? null; },
    get runtimeAPIEndpoint() {
      const namespace = this.selectedNamespace?.slug ?? "{namespace}";
      const environment = this.selectedEnvironment?.slug ?? "{environment}";
      return `${API_ROOT}/runtime/${namespace}/${environment}?format=${this.displayFormat}`;
    },
    get runtimeAPIURL() { return `${window.location.origin}${this.runtimeAPIEndpoint}`; },
    get runtimeCurlExample() {
      return `curl --fail --show-error \\\n  -H "X-API-Key: <namespace-api-key>" \\\n  "${this.runtimeAPIURL}"`;
    },
    get availableParents() { return this.environments.filter((item) => item.id !== this.environmentForm.id && !this.isDescendant(item.id, this.environmentForm.id)); },
    get availableParentRows() { return buildParentEnvironmentTree(this.availableParents); },
    get selectedParentLabel() {
      if (!this.environmentForm.parent_id) return "无（创建为根环境）";
      const environment = this.environments.find((item) => item.id === this.environmentForm.parent_id);
      return environment ? `${environment.name}（${environment.slug}）` : "请选择父环境";
    },
    get mergedDraftState() {
      return mergeConfigState(this.inherited, this.inheritedDescriptions, this.draft, this.draftDescriptions);
    },
    get formattedFinal() {
      const merged = this.mergedDraftState;
      return formatConfig(merged.config, merged.descriptions, this.displayFormat);
    },
    get formattedPublished() {
      return this.publishedRelease
        ? formatConfig(this.publishedRelease.config, this.publishedRelease.descriptions, this.displayFormat)
        : "";
    },
    get finalConfigEmpty() { return configIsEmpty(this.mergedDraftState.config); },
    get publishedConfigEmpty() { return this.publishedRelease ? configIsEmpty(this.publishedRelease.config) : false; },
    get comparisonConfigsEmpty() {
      return Boolean(
        this.comparisonLeftData
        && this.comparisonRightData
        && configIsEmpty(this.comparisonLeftData.config)
        && configIsEmpty(this.comparisonRightData.config),
      );
    },
    get comparisonSourceRows() { return buildComparisonSourceRows(this.environments, this.releases); },
    get comparisonSourceOptions() { return this.comparisonSourceRows.flatMap((row) => row.sources); },
    get environmentTreeRows() { return buildEnvironmentDisplayTree(this.environments); },

    async loadNamespaces() {
      this.loadingNamespaces = true;
      try {
        const data = await api("/namespaces");
        this.namespaces = data.namespaces;
      } catch (error) {
        this.notify(error.message, "error");
      } finally {
        this.loadingNamespaces = false;
      }
    },

    async selectNamespace(id, force = false, urlMode = "push", preferredEnvironmentID = 0) {
      if (id === this.selectedNamespaceID && !force) {
        this.namespaceMenu = false;
        return;
      }
      if (this.dirty && !force) {
        this.openConfirmation({
          kind: "discardNamespace",
          title: "放弃未保存的修改？",
          message: "切换命名空间会丢失当前编辑器中尚未保存的覆盖项。",
          confirmLabel: "放弃并切换",
          tone: "danger",
          targetNamespaceID: id,
          targetID: preferredEnvironmentID,
          urlMode,
        });
        return;
      }
      this.namespaceMenu = false;
      this.selectedNamespaceID = id;
      this.selectedID = 0;
      this.publishIDs = [];
      this.inheritanceChain = [];
      this.releases = [];
      this.dirty = false;
      this.resetEnvironmentViews();
      await this.loadEnvironments();
      const initialEnvironmentID = this.environments.some((environment) => environment.id === preferredEnvironmentID)
        ? preferredEnvironmentID
        : this.environments[0]?.id ?? 0;
      if (initialEnvironmentID) {
        await this.selectEnvironment(initialEnvironmentID, true, urlMode);
      } else if (urlMode !== "none") {
        this.syncWorkspaceURL(id, 0, urlMode);
      }
    },

    resetEnvironmentViews() {
      this.tab = "draft";
      this.publishedRelease = null;
      this.publishedVersions = [];
      this.publishedVersion = 0;
      this.comparisonLeft = "";
      this.comparisonRight = "";
      this.comparisonLeftData = null;
      this.comparisonRightData = null;
      this.comparisonError = "";
      this.comparisonMenu = "";
    },

    async loadEnvironments() {
      if (!this.selectedNamespaceID) {
        this.environments = [];
        this.loadingEnvironments = false;
        return;
      }
      this.loadingEnvironments = true;
      try {
        const data = await api(`/namespaces/${this.selectedNamespaceID}/environments`);
        this.environments = data.environments;
        this.publishIDs = this.publishIDs.filter((id) => this.environments.some((item) => item.id === id));
      } catch (error) {
        this.notify(error.message, "error");
      } finally {
        this.loadingEnvironments = false;
      }
    },

    async selectEnvironment(id, force = false, urlMode = "push") {
      if (id === this.selectedID && !force) return;
      if (this.dirty && !force) {
        this.openConfirmation({
          kind: "discard",
          title: "放弃未保存的修改？",
          message: "切换环境会丢失当前编辑器中尚未保存的覆盖项。",
          confirmLabel: "放弃并切换",
          tone: "danger",
          targetID: id,
          urlMode,
        });
        return;
      }
      this.selectedID = id;
      this.resetEnvironmentViews();
      const loaded = await this.loadDraft();
      if (loaded && urlMode !== "none") this.syncWorkspaceURL(this.selectedNamespaceID, id, urlMode);
    },

    async loadDraft() {
      this.loadingConfig = true;
      typeMemory.clear();
      let loaded = false;
      try {
        const data = await api(`${this.namespaceAPI()}/environments/${this.selectedID}/config`);
        this.draft = cloneConfigValue(data.draft);
        this.draftDescriptions = { ...(data.draft_descriptions ?? {}) };
        this.inherited = cloneConfigValue(data.inherited);
        this.inheritedDescriptions = { ...(data.inherited_descriptions ?? {}) };
        this.finalConfig = cloneConfigValue(data.final);
        this.finalDescriptions = { ...(data.final_descriptions ?? {}) };
        this.inheritanceChain = data.inheritance_chain;
        this.dirty = false;
        loaded = true;
      } catch (error) {
        this.notify(error.message, "error");
      } finally {
        this.loadingConfig = false;
      }
      if (loaded) {
        await this.$nextTick();
        this.renderEditor();
      }
      return loaded;
    },

    renderEditor() {
      if (!this.$refs.treeEditor || this.tab !== "draft") return;
      if (!treeEditor || treeEditor.root !== this.$refs.treeEditor) {
        treeEditor?.destroy();
        treeEditor = new ConfigTreeEditor(
          this.$refs.treeEditor,
          this.draft,
          this.draftDescriptions,
          (value, descriptions) => {
            this.draft = value;
            this.draftDescriptions = descriptions;
            this.dirty = true;
          },
          (message) => this.notify(message, "error"),
          typeMemory,
        );
      }
      treeEditor.render(cloneConfigValue(this.draft), { ...this.draftDescriptions });
    },

    async saveDraft() {
      if (!this.dirty || this.saving) return;
      this.saving = true;
      try {
        const data = await api(`${this.namespaceAPI()}/environments/${this.selectedID}/config`, {
          method: "PUT",
          body: { config: this.draft, descriptions: this.draftDescriptions },
        });
        this.inherited = cloneConfigValue(data.inherited);
        this.inheritedDescriptions = { ...(data.inherited_descriptions ?? {}) };
        this.finalConfig = cloneConfigValue(data.final);
        this.finalDescriptions = { ...(data.final_descriptions ?? {}) };
        this.inheritanceChain = data.inheritance_chain;
        this.dirty = false;
        await this.loadEnvironments();
        this.notify("草稿已保存");
      } catch (error) {
        this.notify(error.message, "error");
      } finally {
        this.saving = false;
      }
    },

    openNamespaceModal(namespace = null) {
      this.namespaceMenu = false;
      this.namespaceForm = namespace
        ? { id: namespace.id, name: namespace.name, slug: namespace.slug, description: namespace.description, api_key: "", api_key_configured: Boolean(namespace.api_key_configured) }
        : { id: 0, name: "", slug: "", description: "", api_key: "", api_key_configured: false };
      this.namespaceModal = true;
    },
    closeNamespaceModal() {
      if (this.namespaceSaving) return;
      this.namespaceModal = false;
    },
    async saveNamespace() {
      this.namespaceSaving = true;
      const id = this.namespaceForm.id;
      try {
        const body = {
          name: this.namespaceForm.name,
          slug: this.namespaceForm.slug,
          description: this.namespaceForm.description,
          api_key: this.namespaceForm.api_key,
        };
        const data = await api(id ? `/namespaces/${id}` : "/namespaces", { method: id ? "PUT" : "POST", body });
        this.namespaceModal = false;
        await this.loadNamespaces();
        if (id) {
          this.selectedNamespaceID = data.namespace.id;
          this.syncWorkspaceURL(this.selectedNamespaceID, this.selectedID, "replace");
        } else {
          await this.selectNamespace(data.namespace.id);
        }
        this.notify(id ? "命名空间已更新" : "命名空间已创建");
      } catch (error) {
        this.notify(error.message, "error");
      } finally {
        this.namespaceSaving = false;
      }
    },
    deleteSelectedNamespace() {
      const namespace = this.selectedNamespace;
      if (!namespace) return;
      this.namespaceModal = false;
      this.openConfirmation({
        kind: "deleteNamespace",
        title: `删除命名空间「${namespace.name}」？`,
        message: `命名空间下的 ${namespace.environment_count} 个配置环境、所有草稿和已发布版本都将被永久删除。`,
        confirmLabel: "确认删除命名空间",
        tone: "danger",
        targetNamespaceID: namespace.id,
        targets: [{ ...namespace, slug: namespace.slug }],
      });
    },
    async performDeleteNamespace(namespaceID) {
      try {
        await api(`/namespaces/${namespaceID}`, { method: "DELETE" });
        this.selectedNamespaceID = 0;
        this.selectedID = 0;
        this.environments = [];
        this.publishIDs = [];
        this.dirty = false;
        this.resetEnvironmentViews();
        await this.loadNamespaces();
        if (this.namespaces.length) await this.selectNamespace(this.namespaces[0].id, true, "replace");
        else this.syncWorkspaceURL(0, 0, "replace");
        this.notify("命名空间及其配置环境已删除");
      } catch (error) {
        this.notify(error.message, "error");
      }
    },

    openEnvironmentModal(environment = null) {
      if (!this.selectedNamespaceID) {
        this.openNamespaceModal();
        return;
      }
      if (environment) {
        this.environmentForm = { id: environment.id, name: environment.name, slug: environment.slug, description: environment.description, parent_id: environment.parent_id };
      } else {
        this.environmentForm = { id: 0, name: "", slug: "", description: "", parent_id: 0 };
      }
      this.parentSelectorOpen = false;
      this.environmentModal = true;
    },
    closeEnvironmentModal() {
      if (this.environmentSaving) return;
      this.parentSelectorOpen = false;
      this.environmentModal = false;
    },
    selectParentEnvironment(id) {
      this.environmentForm.parent_id = id;
      this.parentSelectorOpen = false;
    },

    async saveEnvironment() {
      this.environmentSaving = true;
      const id = this.environmentForm.id;
      try {
        const body = { name: this.environmentForm.name, slug: this.environmentForm.slug, description: this.environmentForm.description, parent_id: Number(this.environmentForm.parent_id) || 0 };
        const path = id ? `${this.namespaceAPI()}/environments/${id}` : `${this.namespaceAPI()}/environments`;
        const data = await api(path, { method: id ? "PUT" : "POST", body });
        this.environmentModal = false;
        await Promise.all([this.loadEnvironments(), this.loadNamespaces()]);
        await this.selectEnvironment(data.environment.id, true);
        this.notify(id ? "环境已更新" : "环境已创建");
      } catch (error) {
        this.notify(error.message, "error");
      } finally {
        this.environmentSaving = false;
      }
    },

    deleteSelectedEnvironment() {
      const environment = this.selectedEnvironment;
      if (!environment) return;
      this.openConfirmation({
        kind: "delete",
        title: `删除环境「${environment.name}」？`,
        message: "该环境及其全部发布记录将被永久删除。存在子环境继承时，服务器会拒绝本次操作。",
        confirmLabel: "确认删除",
        tone: "danger",
        targetID: environment.id,
        targets: [environment],
      });
    },

    async performDeleteEnvironment(environmentID) {
      try {
        await api(`${this.namespaceAPI()}/environments/${environmentID}`, { method: "DELETE" });
        this.selectedID = 0;
        await Promise.all([this.loadEnvironments(), this.loadNamespaces()]);
        if (this.environments.length) await this.selectEnvironment(this.environments[0].id, true, "replace");
        else this.syncWorkspaceURL(this.selectedNamespaceID, 0, "replace");
        this.notify("环境已删除");
      } catch (error) {
        this.notify(error.message, "error");
      }
    },

    publishSelected() {
      if (this.publishIDs.length === 0) return;
      if (this.dirty) {
        this.notify("请先保存当前环境草稿再发布", "error");
        return;
      }
      const targets = this.environments.filter((environment) => this.publishIDs.includes(environment.id));
      this.openConfirmation({
        kind: "publish",
        title: `发布 ${this.publishIDs.length} 个环境？`,
        message: "将为以下环境创建新的不可变完整配置快照。",
        confirmLabel: "确认发布",
        tone: "primary",
        targets,
      });
    },

    async performPublish(reason) {
      this.publishing = true;
      try {
        const data = await api(`${this.namespaceAPI()}/publish`, {
          method: "POST",
          body: { environment_ids: this.publishIDs, reason: reason.trim() },
        });
        this.releases = [];
        await this.loadEnvironments();
        if (this.tab === "published") await this.loadPublishedVersions();
        this.notify(`发布成功，批次 ${data.publication.batch_id}`);
        return true;
      } catch (error) {
        this.notify(error.message, "error");
        return false;
      } finally {
        this.publishing = false;
      }
    },

    openConfirmation({ kind, title, message, confirmLabel, tone, targetID = 0, targetNamespaceID = 0, targets = [], urlMode = "push" }) {
      this.confirmation = {
        open: true,
        kind,
        title,
        message,
        reason: "",
        confirmLabel,
        tone,
        targetID,
        targetNamespaceID,
        urlMode,
        targets: targets.map((environment) => ({
          id: environment.id,
          name: environment.name,
          slug: environment.slug,
        })),
      };
      this.$nextTick(() => {
        if (kind === "publish") this.$refs.publishReason?.focus();
        else this.$refs.confirmationButton?.focus();
      });
    },
    closeConfirmation() {
      if (this.confirming) return;
      if ((this.confirmation.kind === "discard" || this.confirmation.kind === "discardNamespace") && this.confirmation.urlMode === "none") {
        this.syncWorkspaceURL(this.selectedNamespaceID, this.selectedID, "replace");
      }
      this.confirmation.open = false;
    },
    async confirmPendingAction() {
      if (this.confirming) return;
      const { kind, targetID, targetNamespaceID, urlMode, reason } = this.confirmation;
      if (kind === "publish" && !reason.trim()) {
        this.notify("请填写发布原因", "error");
        this.$refs.publishReason?.focus();
        return;
      }
      this.confirming = true;
      try {
        if (kind === "delete") await this.performDeleteEnvironment(targetID);
        if (kind === "deleteNamespace") await this.performDeleteNamespace(targetNamespaceID);
        if (kind === "publish" && !(await this.performPublish(reason))) return;
        if (kind === "discard") await this.selectEnvironment(targetID, true, urlMode);
        if (kind === "discardNamespace") await this.selectNamespace(targetNamespaceID, true, urlMode, targetID);
        this.confirmation.open = false;
      } finally {
        this.confirming = false;
      }
    },

    async openPublishedTab() { this.tab = "published"; await this.loadPublishedVersions(); },
    async loadPublishedVersions() {
      this.publishedLoading = true;
      try {
        const data = await api(`${this.namespaceAPI()}/environments/${this.selectedID}/releases`);
        this.publishedVersions = data.releases;
        this.publishedVersion = this.publishedVersions[0]?.version ?? 0;
        if (!this.publishedVersion) {
          this.publishedRelease = null;
          return;
        }
        await this.loadPublished(this.publishedVersion, false);
      } catch (error) {
        if (error.status === 404) this.publishedRelease = null;
        else this.notify(error.message, "error");
      } finally {
        this.publishedLoading = false;
      }
    },
    async loadPublished(version, manageLoading = true) {
      if (!version) return;
      if (manageLoading) this.publishedLoading = true;
      this.publishedVersionMenu = false;
      try {
        const data = await api(`${this.namespaceAPI()}/environments/${this.selectedID}/releases/${version}`);
        this.publishedVersion = Number(version);
        this.publishedRelease = data.release;
      } catch (error) {
        this.notify(error.message, "error");
      } finally {
        if (manageLoading) this.publishedLoading = false;
      }
    },

    selectFormat(format) {
      if (!CONFIG_FORMATS.some((item) => item.value === format)) return;
      this.displayFormat = format;
      this.formatMenu = "";
      try {
        window.localStorage.setItem(DISPLAY_FORMAT_STORAGE_KEY, format);
      } catch {
        // Format selection remains active when browser storage is unavailable.
      }
      if (this.tab === "compare") this.$nextTick(() => this.renderComparisonDiff());
    },
    formatLabel(format) {
      return CONFIG_FORMATS.find((item) => item.value === format)?.label ?? "JSON";
    },
    async openCompareTab() {
      this.tab = "compare";
      if (!this.releases.length) {
        try {
          const data = await api(`${this.namespaceAPI()}/releases`);
          this.releases = data.releases;
        } catch (error) {
          this.notify(error.message, "error");
        }
      }
      if (!this.comparisonLeft || !this.comparisonRight) {
        const defaults = defaultComparisonSources(this.environments, this.selectedID, this.releases);
        this.comparisonLeft = defaults.left;
        this.comparisonRight = defaults.right;
      }
      await this.loadComparison();
    },
    selectComparisonSource(side, value) {
      if (side === "left") this.comparisonLeft = value;
      else this.comparisonRight = value;
      this.comparisonMenu = "";
      this.loadComparison();
    },
    comparisonSourceLabel(value) {
      return this.comparisonSourceOptions.find((option) => option.value === value)?.fullLabel ?? "选择对比来源";
    },
    comparisonRowSelected(row, side) {
      const selected = side === "left" ? this.comparisonLeft : this.comparisonRight;
      return row.sources.some((source) => source.value === selected);
    },
    async loadComparison() {
      if (!this.comparisonLeft || !this.comparisonRight) return;
      this.comparisonLoading = true;
      this.comparisonError = "";
      try {
        const [left, right] = await Promise.all([
          this.loadComparisonSource(this.comparisonLeft),
          this.loadComparisonSource(this.comparisonRight),
        ]);
        this.comparisonLeftData = left;
        this.comparisonRightData = right;
        await this.$nextTick();
        this.renderComparisonDiff();
      } catch (error) {
        this.comparisonError = error.message;
        this.notify(error.message, "error");
      } finally {
        this.comparisonLoading = false;
      }
    },
    async loadComparisonSource(source) {
      const [kind, environmentID, version] = source.split(":");
      if (kind === "draft") {
        const data = await api(`${this.namespaceAPI()}/environments/${environmentID}/final`);
        return { config: data.config, descriptions: data.descriptions ?? {} };
      }
      const data = await api(`${this.namespaceAPI()}/environments/${environmentID}/releases/${version}`);
      return { config: data.release.config, descriptions: data.release.descriptions ?? {} };
    },
    async renderComparisonDiff() {
      if (!this.$refs.diffViewer || this.tab !== "compare" || !this.comparisonLeftData || !this.comparisonRightData) return;
      if (this.comparisonConfigsEmpty) {
        diffViewer?.cleanUp();
        diffViewer = null;
        this.$refs.diffViewer.replaceChildren();
        return;
      }
      diffsModulePromise ??= import("@pierre/diffs");
      const { FileDiff } = await diffsModulePromise;
      const root = this.$refs.diffViewer;
      if (!root || this.tab !== "compare") return;
      diffViewer?.cleanUp();
      diffViewer = null;
      root.replaceChildren();
      const extension = CONFIG_FORMATS.find((format) => format.value === this.displayFormat)?.extension ?? "json";
      const fileName = `config.${extension}`;
      const oldFile = {
        name: fileName,
        contents: formatConfig(
          this.comparisonLeftData.config,
          this.comparisonLeftData.descriptions,
          this.displayFormat,
        ),
      };
      const newFile = {
        name: fileName,
        contents: formatConfig(
          this.comparisonRightData.config,
          this.comparisonRightData.descriptions,
          this.displayFormat,
        ),
      };
      diffViewer = new FileDiff({
        diffStyle: "split",
        diffIndicators: "bars",
        expandUnchanged: true,
        hunkSeparators: "line-info-basic",
        overflow: "wrap",
        theme: "pierre-light",
        themeType: "light",
      });
      diffViewer.render({ oldFile, newFile, containerWrapper: root });
    },

    environmentName(id) { return this.environments.find((item) => item.id === id)?.name ?? "未知环境"; },
    namespaceAPI() { return `/namespaces/${this.selectedNamespaceID}`; },
    namespaceIDFromURL() {
      const value = new URL(window.location.href).searchParams.get("namespace");
      const id = Number(value);
      return Number.isSafeInteger(id) && id > 0 ? id : 0;
    },
    environmentIDFromURL() {
      const value = new URL(window.location.href).searchParams.get("environment");
      const id = Number(value);
      return Number.isSafeInteger(id) && id > 0 ? id : 0;
    },
    syncWorkspaceURL(namespaceID, environmentID, mode = "push") {
      const url = new URL(window.location.href);
      if (namespaceID > 0) url.searchParams.set("namespace", String(namespaceID));
      else url.searchParams.delete("namespace");
      if (environmentID > 0) url.searchParams.set("environment", String(environmentID));
      else url.searchParams.delete("environment");
      if (url.href === window.location.href) return;
      const state = namespaceID > 0 ? { namespaceID, environmentID } : {};
      if (mode === "replace") window.history.replaceState(state, "", url);
      else window.history.pushState(state, "", url);
    },
    isDescendant(candidateID, ancestorID) {
      if (!ancestorID) return false;
      let current = this.environments.find((item) => item.id === candidateID);
      while (current?.parent_id) {
        if (current.parent_id === ancestorID) return true;
        current = this.environments.find((item) => item.id === current.parent_id);
      }
      return false;
    },
    formatDate(value) { return value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : ""; },
    async copyText(text) {
      try { await navigator.clipboard.writeText(text); this.notify("已复制到剪贴板"); }
      catch { this.notify("复制失败，请检查浏览器权限", "error"); }
    },
    notify(message, type = "success") {
      const id = ++this.toastSequence;
      this.toasts.push({ id, message, type });
      const timer = window.setTimeout(() => {
        this.toasts = this.toasts.filter((toast) => toast.id !== id);
        toastTimers.delete(timer);
      }, 3500);
      toastTimers.add(timer);
    },
  };
}

async function api(path, options = {}) {
  const response = await fetch(`${API_ROOT}${path}`, {
    method: options.method ?? "GET",
    headers: { Accept: "application/json", "Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest" },
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  let payload;
  try { payload = await response.json(); }
  catch { payload = { message: "服务返回了无法识别的响应" }; }
  if (!response.ok || payload.status !== 0) {
    const error = new Error(payload.message || `请求失败 (${response.status})`);
    error.status = response.status;
    throw error;
  }
  return payload.data;
}

function isCollection(value) { return Array.isArray(value) || (value !== null && typeof value === "object"); }
function configIsEmpty(config) {
  return !config || Object.keys(config).length === 0;
}
window.configCenterApp = configCenterApp;
window.Alpine = Alpine;
Alpine.start();
