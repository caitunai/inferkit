const endpoint = "/image-review/api/reclassification";
const statusNames = { idle: "尚未开始", running: "逐张处理中", pausing: "正在暂停", paused: "已暂停", cancelling: "正在取消", cancelled: "已取消", completed: "已完成" };
const reasons = { service_restarted: "服务已重启，请检查后手动继续。", worker_stopped: "任务已停止，可从断点继续。", database_unavailable: "数据库或任务锁不可用，请检查服务日志。", checkpoint_unavailable: "进度文件写入失败，请检查持久卷后重启服务。", consecutive_failures: "连续失败过多，已自动暂停，请检查图片来源。" };
const failureNames = { content_changed: "图片内容已变化，跳过", record_changed: "原记录已变化，跳过", detection_failed: "下载或检测失败" };

export function createReclassificationApp({ fetcher = globalThis.fetch, schedule = globalThis.setTimeout, unschedule = globalThis.clearTimeout } = {}) {
  let timer;
  let request;
  let generation = 0;
  let stopped = false;
  return {
    task: null, busy: false, error: "", confirmed: false, interval: 0,
    get state() { return this.task?.state ?? { status: "idle", snapshot: {}, failures: [] }; },
    get statusLabel() { return statusNames[this.state.status] ?? this.state.status; },
    get reasonLabel() { return reasons[this.state.reason] ?? this.state.reason ?? ""; },
    get processed() { return (this.state.succeeded || 0) + (this.state.failed || 0) + (this.state.skipped || 0); },
    get progress() { const total = this.state.snapshot.total || 0; return total ? Math.min(100, this.processed / total * 100).toFixed(1) : "0.0"; },
    get canStart() { return !!this.task?.available && !this.task.active && ["idle", "completed", "cancelled"].includes(this.state.status); },
    get modelChanged() { return !!this.state.model_sha256 && this.state.model_sha256 !== this.task?.current_model; },
    get canResume() { return !!this.task?.available && !this.task.active && this.state.status === "paused" && !this.modelChanged && this.state.reason !== "checkpoint_unavailable"; },
    get canCancel() { return !!this.task?.available && ["running", "pausing", "paused"].includes(this.state.status); },
    async init() { await this.refresh(); },
    destroy() { stopped = true; generation++; unschedule(timer); request?.abort(); },
    later() { if (!stopped) timer = schedule(() => this.refresh(), 2500); },
    async refresh() {
      unschedule(timer);
      if (stopped || this.busy) return;
      const version = ++generation;
      request?.abort();
      request = new AbortController();
      try {
        const task = await this.send("", undefined, request.signal);
        if (version === generation && !stopped) { this.task = task; this.error = ""; }
      } catch (error) { if (version === generation && !stopped && error.name !== "AbortError") this.error = error.message; }
      finally { if (version === generation) this.later(); }
    },
    async send(path, body, signal) {
      const response = await fetcher(`${endpoint}${path}`, {
        method: body === undefined ? "GET" : "POST", credentials: "same-origin", cache: "no-store", signal,
        headers: { "X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json" },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      let payload;
      try { payload = await response.json(); } catch { throw new Error("任务接口返回异常，请检查登录状态和服务日志。"); }
      if (!response.ok || !payload.data?.task) throw new Error(payload.message || `请求失败 (${response.status})`);
      return payload.data.task;
    },
    async mutate(path, body) {
      if (this.busy || stopped) return;
      this.busy = true; generation++; unschedule(timer); request?.abort();
      try { this.task = await this.send(path, body); this.error = ""; this.confirmed = false; }
      catch (error) { this.error = error.message; }
      finally { this.busy = false; this.later(); }
    },
    async start() {
      const interval = Number(this.interval);
      if (!this.canStart || !this.confirmed) return;
      if (!Number.isInteger(interval) || interval < 0 || interval > 60000) { this.error = "处理间隔须为 0–60000 毫秒的整数。"; return; }
      await this.mutate("/start", { model_sha256: this.task.current_model, interval_ms: interval, confirm_overwrite: true });
    },
    async control(action) { await this.mutate("/control", { action }); },
    failureLabel(code) { return failureNames[code] ?? code; },
    dateLabel(value) { return value ? new Date(value).toLocaleString() : "—"; },
  };
}
