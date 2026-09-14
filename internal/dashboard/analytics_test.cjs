"use strict";
const assert = require("node:assert/strict");
const { test } = require("node:test");
const metrics = require("./analytics.js");
const fs = require("node:fs");
const vm = require("node:vm");

function loadApp(globals = {}) {
  // Minimal DOM adapter: an innerHTML assignment fails immediately, so this
  // exercises our rendering path rather than merely searching source strings.
  class Element {
    constructor(tag) {
      this.tagName = tag;
      this.children = [];
      this.attributes = {};
      this.dataset = {};
      this.handlers = {};
      this.textContent = "";
    }
    append(...children) {
      this.children.push(...children);
    }
    replaceChildren(...children) {
      this.children = children;
    }
    setAttribute(name, value) {
      this.attributes[name] = value;
    }
    addEventListener(name, callback) {
      this.handlers[name] = callback;
    }
    set innerHTML(value) {
      throw new Error("Untrusted error details must never use innerHTML");
    }
  }
  const nodes = new Map();
  const context = vm.createContext({
    ClaudexMetrics: metrics,
    Intl,
    Node: Element,
    document: {
      createElement: (tag) => new Element(tag),
      getElementById: (id) => {
        if (!nodes.has(id)) nodes.set(id, new Element("div"));
        return nodes.get(id);
      },
      querySelectorAll: () => [],
      querySelector: () => new Element("main"),
    },
    ResizeObserver: class {
      observe() {}
    },
    window: { addEventListener() {}, location: { hash: "" } },
    ...globals,
  });
  vm.runInContext(fs.readFileSync(require.resolve("./app.js"), "utf8"), context);
  return { context, nodes };
}

test("nearest-rank percentiles preserve zero and missing observations", () => {
  assert.equal(metrics.percentile([], 0.95), null);
  assert.equal(metrics.percentile([0], 0.99), 0);
  const values = [3, 1, 2, 100];
  assert.equal(metrics.percentile(values, 0.5), 2);
  assert.equal(metrics.percentile(values, 0.95), 100);
  assert.deepEqual(values, [3, 1, 2, 100]);
  assert.equal(metrics.ratio(0, 0), null);
  assert.equal(metrics.ratio(0, 10), 0);
});

test("window matches minute buckets and uses completion rather than start", () => {
  const snapshot = {
    generated: "2026-09-14T12:00:30Z",
    history: ["11:45", "11:46", "11:59", "12:00"].map((time) => ({
      time: `2026-09-14T${time}:00Z`,
    })),
    recent: [
      {
        started: "2026-09-14T10:00:00Z",
        completed: "2026-09-14T12:00:00Z",
        duration_ms: 1000,
        first_output_ms: 0,
      },
      { completed: "2026-09-14T11:46:00Z", duration_ms: 5000 },
      {
        completed: "2026-09-14T11:45:59Z",
        duration_ms: 999999,
        first_output_ms: 99,
      },
    ],
  };
  const sample = metrics.sample(snapshot, 15);
  assert.equal(sample.records.length, 2);
  assert.equal(sample.history.length, 3);
  assert.equal(sample.firstCount, 1);
  assert.equal(sample.firstP50, 0);
  assert.equal(sample.p95, 5000);
  assert.deepEqual(sample.histogram, [1, 1, 0, 0, 0, 0, 0, 0]);
});

test("histogram boundaries include each duration exactly once", () => {
  const values = [
    0, 1000, 1001, 5000, 15000, 30000, 60000, 120000, 300000, 300001,
  ];
  const sample = metrics.sample(
    {
      generated: "2026-09-14T12:00:00Z",
      history: [],
      recent: values.map((duration_ms) => ({
        completed: "2026-09-14T12:00:00Z",
        duration_ms,
      })),
    },
    60,
  );
  assert.deepEqual(sample.histogram, [2, 2, 1, 1, 1, 1, 1, 1]);
  assert.equal(sample.firstP50, null);
});

test("filters keep cancellations separate from failures and successes", () => {
  const records = [
    { id: 1, model: "gpt-6-astra", effort: "max", status: 200 },
    { id: 2, model: "gpt-5.6-sol", effort: "high", status: 429 },
    {
      id: 3,
      model: "gpt-5.6-sol",
      effort: "high",
      status: 499,
      error_kind: "connection_reset",
    },
  ];
  assert.deepEqual(
    metrics.filter(records, { status: "errors" }).map((r) => r.id),
    [2],
  );
  assert.deepEqual(
    metrics
      .filter(records, {
        model: "gpt-5.6-sol",
        effort: "high",
        status: "429",
        search: "SOL",
      })
      .map((r) => r.id),
    [2],
  );
  assert.deepEqual(
    metrics.filter(records, { status: "ok" }).map((r) => r.id),
    [1],
  );
  assert.deepEqual(metrics.filter(records, { search: "<script>" }), []);
  assert.deepEqual(
    metrics
      .filter(records, { status: "canceled", search: "connection_reset" })
      .map((r) => r.id),
    [3],
  );
});

test("search includes server messages and structured diagnostics", () => {
  const records = [
    {
      id: 1,
      model: "gpt-6-astra",
      effort: "high",
      status: 502,
      error_kind: "unexpected_eof",
      error: {
        source: "upstream",
        type: "server_error",
        code: "response_failed",
        status: 200,
        message: "The server could not complete this request.\nPlease retry.",
      },
    },
    {
      id: 2,
      model: "gpt-5.6-sol",
      effort: "high",
      status: 400,
      error: {
        source: "proxy",
        type: "invalid_request_error",
        message: "Missing input <script>alert('example')</script>",
      },
    },
    { id: 3, model: "gpt-5.6-sol", effort: "high", status: 200 },
  ];
  for (const search of [
    "upstream",
    "server_error",
    "response failed",
    "could not complete",
    "200",
  ]) {
    assert.deepEqual(
      metrics
        .filter(records, { status: "errors", search })
        .map((record) => record.id),
      [1],
    );
  }
  assert.deepEqual(
    metrics.filter(records, { search: "<script>" }).map((record) => record.id),
    [2],
  );
  assert.deepEqual(
    metrics.filter(records, { status: "ok", search: "Please retry" }),
    [],
  );
});

test("error details use literal text and preserve expansion on rerender", () => {
  const { context } = loadApp();
  const message = '<script>alert("literal")</script>\nSecond line';
  const record = {
    id: 17,
    status: 502,
    error: {
      source: "upstream",
      type: "server_error",
      code: "response_failed",
      status: 200,
      message,
      truncated: true,
    },
  };
  const first = context.errorDetails(record);
  assert.equal(first.detailsRow.hidden, true);
  assert.equal(first.toggle.attributes["aria-expanded"], "false");
  assert.equal(first.toggle.attributes["aria-controls"], first.detailsRow.id);
  first.toggle.handlers.click();
  assert.equal(first.detailsRow.hidden, false);
  assert.equal(first.toggle.attributes["aria-expanded"], "true");
  const rerendered = context.errorDetails(record);
  assert.equal(rerendered.detailsRow.hidden, false);
  const details = rerendered.detailsRow.children[0].children[0];
  const renderedMessage = details.children.find(
    (child) => child.tagName === "pre",
  );
  assert.equal(renderedMessage.textContent, message);
  assert.equal(renderedMessage.tabIndex, 0);
  assert.ok(
    details.children.some(
      (child) => child.textContent === "Message truncated by the proxy.",
    ),
  );
  const status = details.children[0].children.find(
    (item) => item.children[0].textContent === "Upstream HTTP",
  );
  assert.equal(status.children[1].textContent, 200);
  rerendered.toggle.handlers.click();
  assert.equal(context.errorDetails(record).detailsRow.hidden, true);
  const canceled = context.errorDetails({ ...record, id: 18, status: 499 });
  assert.equal(canceled.toggle.textContent, "View cancellation ▾");
  assert.equal(
    canceled.toggle.attributes["aria-label"],
    "View cancellation details for request 18",
  );
});

test("traffic outcomes partition all completions and rates exclude cancellations", () => {
  const mixed = metrics.outcomes({ requests: 10, errors: 3, canceled: 2 });
  assert.equal(mixed.successful, 5);
  assert.equal(mixed.failed, 3);
  assert.equal(mixed.canceled, 2);
  assert.equal(mixed.successful + mixed.failed + mixed.canceled, 10);
  assert.equal(mixed.successRate, 5 / 8);
  assert.equal(mixed.failureRate, 3 / 8);
  const canceled = metrics.outcomes({ requests: 4, errors: 0, canceled: 4 });
  assert.equal(canceled.successful, 0);
  assert.equal(canceled.failed, 0);
  assert.equal(canceled.canceled, 4);
  assert.equal(canceled.successRate, null);
  assert.equal(canceled.failureRate, null);
  const empty = metrics.outcomes({ requests: 0, errors: 0, canceled: 0 });
  assert.equal(empty.successRate, null);
  assert.equal(empty.failureRate, null);
});

function pendingResponse(signal) {
  let resolve, reject;
  const promise = new Promise((done, fail) => {
    resolve = done;
    reject = fail;
  });
  signal.addEventListener("abort", () => reject(new Error("aborted")), {
    once: true,
  });
  return { promise, resolve, reject, signal };
}

function pollingApp() {
  const timers = new Map();
  const calls = [];
  let nextTimer = 0;
  const app = loadApp({
    AbortController,
    setTimeout(callback, delay) {
      const id = ++nextTimer;
      timers.set(id, { callback, delay });
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    fetch(url, options) {
      assert.equal(url, "/dashboard/api");
      assert.equal(options.credentials, "omit");
      assert.equal(options.redirect, "error");
      assert.equal(options.cache, "no-store");
      const call = pendingResponse(options.signal);
      calls.push(call);
      return call.promise;
    },
  });
  // Polling tests stop at the rendering boundary; chart calculations and safe
  // error rendering are exercised independently above.
  vm.runInContext(
    "key = 'local-test-key'; render = (snapshot) => { latest = snapshot; };",
    app.context,
  );
  return {
    ...app,
    calls,
    timers,
    fire(delay) {
      const matches = [...timers].filter(([, timer]) => timer.delay === delay);
      assert.equal(matches.length, 1, `expected one ${delay}ms timer`);
      const [id, timer] = matches[0];
      timers.delete(id);
      return timer.callback();
    },
  };
}

test("poll deadlines cover stalled headers and bodies, then recover", async () => {
  for (const phase of ["headers", "body"]) {
    const app = pollingApp();
    vm.runInContext(
      "latest = { generated: 'old' }; connection('Live · local', 'live');",
      app.context,
    );
    const pending = app.context.refresh();
    await app.context.refresh();
    assert.equal(app.calls.length, 1, "polls must not overlap");
    if (phase === "body") {
      const body = pendingResponse(app.calls[0].signal);
      let started;
      const bodyStarted = new Promise((resolve) => {
        started = resolve;
      });
      app.calls[0].resolve({
        ok: true,
        status: 200,
        json: () => {
          started();
          return body.promise;
        },
      });
      await bodyStarted;
    }
    app.fire(10000);
    await pending;
    assert.equal(app.calls[0].signal.aborted, true);
    assert.equal(app.nodes.get("connection").textContent, "Connection timed out");
    assert.equal(app.nodes.get("stale").hidden, false);
    assert.match(app.nodes.get("stale").textContent, /last snapshot; retrying/);
    assert.equal(vm.runInContext("latest.generated", app.context), "old");
    const retry = app.fire(3000);
    app.calls[1].resolve({
      ok: true,
      status: 200,
      json: async () => ({ generated: "new" }),
    });
    await retry;
    assert.equal(app.nodes.get("connection").textContent, "Live · local");
    assert.equal(app.nodes.get("stale").hidden, true);
    assert.equal(vm.runInContext("latest.generated", app.context), "new");
    assert.deepEqual([...app.timers.values()].map((timer) => timer.delay), [3000]);
  }
});

test("an initial poll timeout reports automatic retries without a snapshot", async () => {
  const app = pollingApp();
  const pending = app.context.refresh();
  app.fire(10000);
  await pending;
  assert.equal(app.nodes.get("connection").textContent, "Connection timed out");
  assert.match(app.nodes.get("login-error").textContent, /Retrying every 3 seconds/);
  assert.deepEqual([...app.timers.values()].map((timer) => timer.delay), [3000]);
});

test("pause and disconnect cancel polling without stale errors or retries", async () => {
  for (const action of ["pause", "disconnect"]) {
    const app = pollingApp();
    const pending = app.context.refresh();
    app.nodes.get(action).handlers.click();
    await pending;
    assert.equal(app.calls[0].signal.aborted, true);
    assert.equal(app.timers.size, 0);
    assert.equal(
      app.nodes.get("connection").textContent,
      action === "pause" ? "Paused" : "Disconnected",
    );
    if (action === "disconnect") {
      assert.equal(vm.runInContext("key", app.context), "");
    } else {
      const refresh = app.context.refresh;
      let resumed;
      app.context.refresh = () => {
        resumed = refresh();
        return resumed;
      };
      app.nodes.get("pause").handlers.click();
      assert.equal(app.nodes.get("connection").textContent, "Reconnecting…");
      assert.equal(app.nodes.get("stale").hidden, false);
      app.calls[1].resolve({ ok: true, status: 200, json: async () => ({}) });
      await resumed;
      assert.equal(app.nodes.get("connection").textContent, "Live · local");
      assert.equal(app.nodes.get("stale").hidden, true);
      app.context.disconnect();
    }
  }
});

test("superseded polls cannot change or schedule a new connection", async () => {
  const app = pollingApp();
  const pending = app.context.refresh();
  const refresh = app.context.refresh;
  let replacement;
  app.context.refresh = () => {
    replacement = refresh();
    return replacement;
  };
  app.context.document.getElementById("key").value = "replacement-local-key";
  app.nodes.get("login-form").handlers.submit({ preventDefault() {} });
  await pending;
  assert.equal(app.calls.length, 2);
  assert.equal(app.calls[0].signal.aborted, true);
  assert.equal(app.nodes.get("connection").textContent, "Connecting…");
  assert.deepEqual([...app.timers.values()].map((timer) => timer.delay), [10000]);
  app.context.disconnect();
  await replacement;
  assert.equal(app.timers.size, 0);
});

test("rejected keys disconnect and stop polling", async () => {
  const app = pollingApp();
  const pending = app.context.refresh();
  app.calls[0].resolve({ ok: false, status: 401 });
  await pending;
  assert.equal(vm.runInContext("key", app.context), "");
  assert.equal(app.timers.size, 0);
  assert.equal(app.nodes.get("connection").textContent, "Disconnected");
  assert.match(app.nodes.get("login-error").textContent, /key was not accepted/);
});
