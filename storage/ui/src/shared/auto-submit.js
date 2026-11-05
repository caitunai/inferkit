// Coordinates one active request and at most one latest-source continuation.
// Pass the Alpine receiver through unchanged so mutations stay reactive.
export function createAutoSubmit(browser, submit, delay = 600) {
  let timer = null;
  let pending = false;
  let lastSource = "";
  let disposed = false;
  const cancel = () => {
    browser.clearTimeout(timer);
    timer = null;
    pending = false;
  };
  const attempt = (page) => {
    const source = page.sourceKey();
    if (disposed || !source || source === lastSource) return;
    if (page.busy) { pending = true; return; }
    pending = false;
    return submit(page);
  };
  return {
    cancel,
    attempt,
    schedule(page, composing = false) {
      cancel();
      if (disposed || composing || !page.sourceKey()) return;
      timer = browser.setTimeout(() => { timer = null; attempt(page); }, delay);
    },
    started(page) { lastSource = page.sourceKey(); },
    finished(page) { if (pending) return attempt(page); },
    reset() { cancel(); lastSource = ""; },
    destroy() { disposed = true; cancel(); },
  };
}
