"use strict";
const $ = (id) => document.getElementById(id);
const metrics = ClaudexMetrics;
const palette = {
  teal: "#6bddbc",
  blue: "#689fed",
  purple: "#b39bec",
  coral: "#ef927e",
  canceled: "#8c9aad",
  gold: "#e7bd78",
  dim: "#626d81",
  muted: "#939cae",
  border: "#2a2f38",
};
const effortColors = {
  none: "#536173",
  minimal: "#718093",
  low: "#689fed",
  medium: "#6bddbc",
  high: "#b39bec",
  xhigh: "#e7bd78",
  max: "#ef927e",
  unknown: "#626d81",
};
const effortOrder = Object.keys(effortColors);
const modelOrder = [
  "gpt-6-astra",
  "gpt-5.6-sol",
  "gpt-5.6-terra",
  "gpt-5.6-luna",
];
const modelNames = {
  "gpt-6-astra": "Astra",
  "gpt-5.6-sol": "Sol",
  "gpt-5.6-terra": "Terra",
  "gpt-5.6-luna": "Luna",
};
const fullNumber = new Intl.NumberFormat();
const shortNumber = new Intl.NumberFormat(undefined, {
  notation: "compact",
  maximumFractionDigits: 1,
});
const clock = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});
const timeOfDay = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
});
const number = (value) => fullNumber.format(value);
const compact = (value) => shortNumber.format(value);
const percent = (value) =>
  value == null ? "—" : `${(value * 100).toFixed(1)}%`;
const duration = (value) =>
  value == null
    ? "—"
    : value < 1000
      ? `${Math.round(value)} ms`
      : value < 60000
        ? `${(value / 1000).toFixed(1)} s`
        : `${(value / 60000).toFixed(1)} min`;
const modelName = (model) => modelNames[model] || model;
const charts = new Map();
const expandedErrors = new Set();
let key = "",
  timer = null,
  controller = null,
  latest = null,
  paused = false,
  windowMinutes = 60,
  page = 0;
const pageSize = 25;
function text(id, value) {
  $(id).textContent = value;
}
function metric(id, value) {
  text(id, compact(value));
  $(id).title = number(value);
}
function connection(label, state = "") {
  text("connection", label);
  $("connection-dot").className = state;
}
function uptime(seconds) {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400)
    return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
  return `${Math.floor(seconds / 86400)}d ${Math.floor((seconds % 86400) / 3600)}h`;
}
function disconnect() {
  key = "";
  clearTimeout(timer);
  controller?.abort();
  controller = null;
  latest = null;
  paused = false;
  $("usage").hidden = true;
  $("login").hidden = false;
  $("key").value = "";
  $("login-error").textContent = "";
  $("stale").hidden = true;
  text("pause", "Pause live");
  connection("Disconnected");
  for (const chart of charts.values()) chart.dispose();
  charts.clear();
  expandedErrors.clear();
  $("models").replaceChildren();
  $("recent").replaceChildren();
  page = 0;
}
function chart(id, option, description) {
  if (!charts.has(id))
    charts.set(id, echarts.init($(id), null, { renderer: "canvas" }));
  $(id).setAttribute("aria-label", description);
  charts
    .get(id)
    .setOption(
      { animation: false, textStyle: { fontFamily: "sans-serif" }, ...option },
      { notMerge: true },
    );
}
const tooltip = {
  trigger: "axis",
  renderMode: "richText",
  backgroundColor: "#252c36",
  borderColor: "#3d4858",
  textStyle: { color: "#d8e1ed", fontSize: 11 },
  axisPointer: { type: "shadow", shadowStyle: { color: "#ffffff05" } },
};
function axes(history, tokenChart = false) {
  return {
    grid: { left: 42, right: 10, top: 18, bottom: 27 },
    tooltip,
    xAxis: {
      type: "category",
      data: history.map((bucket) => clock.format(new Date(bucket.time))),
      boundaryGap: true,
      axisLine: { lineStyle: { color: palette.border } },
      axisTick: { show: false },
      axisLabel: {
        color: palette.dim,
        fontSize: 9,
        hideOverlap: true,
        interval: Math.max(0, Math.floor(history.length / 7) - 1),
      },
    },
    yAxis: {
      type: "value",
      minInterval: tokenChart ? 0 : 1,
      splitNumber: 3,
      axisLabel: { color: palette.dim, fontSize: 9, formatter: compact },
      splitLine: { lineStyle: { color: palette.border, type: "dashed" } },
      axisLine: { show: false },
    },
  };
}
function renderCharts(sample) {
  const history = sample.history;
  chart(
    "traffic-chart",
    {
      ...axes(history),
      series: [
        {
          name: "Successful",
          type: "bar",
          stack: "requests",
          barMaxWidth: 14,
          itemStyle: { color: palette.teal },
          data: history.map(
            (bucket) => metrics.outcomes(bucket.totals).successful,
          ),
        },
        {
          name: "Failures",
          type: "bar",
          stack: "requests",
          barMaxWidth: 14,
          itemStyle: { color: palette.coral, borderRadius: [2, 2, 0, 0] },
          data: history.map((bucket) => metrics.outcomes(bucket.totals).failed),
        },
        {
          name: "Canceled",
          type: "bar",
          stack: "requests",
          barMaxWidth: 14,
          itemStyle: { color: palette.canceled, borderRadius: [2, 2, 0, 0] },
          data: history.map(
            (bucket) => metrics.outcomes(bucket.totals).canceled,
          ),
        },
      ],
    },
    `Successful, failed, and canceled requests per minute over the last ${windowMinutes} minutes, including the partial current minute.`,
  );
  const tokenSeries = [
    [
      "Uncached input",
      palette.teal,
      (bucket) => bucket.totals.tokens.input - bucket.totals.tokens.cached,
    ],
    ["Cached input", palette.blue, (bucket) => bucket.totals.tokens.cached],
    ["Output", palette.purple, (bucket) => bucket.totals.tokens.output],
  ];
  chart(
    "tokens-chart",
    {
      ...axes(history, true),
      series: tokenSeries.map(([name, color, value]) => ({
        name,
        type: "bar",
        stack: "tokens",
        barMaxWidth: 14,
        itemStyle: { color },
        data: history.map(value),
      })),
    },
    `Reported input, cached input, and output token counts per completion minute over the last ${windowMinutes} minutes.`,
  );
  chart(
    "latency-chart",
    {
      grid: { left: 3, right: 3, top: 13, bottom: 24 },
      tooltip: {
        ...tooltip,
        formatter: (values) =>
          `${values[0].name}\n${number(values[0].value)} requests`,
      },
      xAxis: {
        type: "category",
        data: [
          "≤1s",
          "1–5s",
          "5–15s",
          "15–30s",
          "30–60s",
          "1–2m",
          "2–5m",
          ">5m",
        ],
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: { fontSize: 8, color: palette.dim, interval: 0 },
      },
      yAxis: { type: "value", show: false, minInterval: 1 },
      series: [
        {
          type: "bar",
          data: sample.histogram,
          barMaxWidth: 29,
          itemStyle: { color: "#83a8b7", borderRadius: [3, 3, 0, 0] },
        },
      ],
    },
    `Duration distribution of ${sample.records.length} sampled completions. Median ${duration(sample.p50)}, P95 ${duration(sample.p95)}, P99 ${duration(sample.p99)}.`,
  );
  const models = [
    ...new Set(sample.records.map((record) => record.model)),
  ].sort((a, b) => modelOrder.indexOf(a) - modelOrder.indexOf(b));
  const efforts = effortOrder.filter((effort) =>
    sample.records.some((record) => (record.effort || "unknown") === effort),
  );
  chart(
    "mix-chart",
    {
      grid: { left: 52, right: 16, top: 8, bottom: 55 },
      tooltip,
      legend: {
        bottom: 0,
        left: 0,
        itemWidth: 7,
        itemHeight: 7,
        icon: "roundRect",
        itemGap: 12,
        textStyle: { fontSize: 9, color: palette.muted },
      },
      xAxis: {
        type: "value",
        minInterval: 1,
        axisLabel: { color: palette.dim, fontSize: 9 },
        splitLine: { lineStyle: { color: palette.border, type: "dashed" } },
      },
      yAxis: {
        type: "category",
        inverse: true,
        data: models.map(modelName),
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: { color: "#b7c4d4", fontSize: 10 },
      },
      series: efforts.map((effort) => ({
        name: effort,
        type: "bar",
        stack: "effort",
        barMaxWidth: 20,
        itemStyle: { color: effortColors[effort] },
        data: models.map(
          (model) =>
            sample.records.filter(
              (record) =>
                record.model === model &&
                (record.effort || "unknown") === effort,
            ).length,
        ),
      })),
      graphic: models.length
        ? []
        : [
            {
              type: "text",
              left: "center",
              top: "40%",
              style: {
                text: "No requests in this sample",
                fill: palette.dim,
                font: "11px sans-serif",
              },
            },
          ],
    },
    `Sampled request counts by model and reasoning effort, from ${sample.records.length} recent completions.`,
  );
}
function cell(value, className) {
  const td = document.createElement("td");
  if (value instanceof Node) td.append(value);
  else td.textContent = value;
  if (className) td.className = className;
  return td;
}
function row(values) {
  const tr = document.createElement("tr");
  tr.append(...values.map((value) => cell(value)));
  return tr;
}
function badge(value, className) {
  const span = document.createElement("span");
  span.textContent = value;
  span.className = className;
  return span;
}
function modelLabel(model, detail = false) {
  const wrap = document.createElement("div");
  const label = document.createElement("div");
  label.className = "model-label";
  const dot = document.createElement("i");
  dot.className = `model-dot model-${modelNames[model]?.toLowerCase() || "other"}`;
  label.append(dot, document.createTextNode(modelName(model)));
  wrap.append(label);
  if (detail) wrap.append(badge(model, "table-subtext"));
  return wrap;
}
function renderModels(sample) {
  const models = Object.entries(latest.models).sort(
    ([a], [b]) => modelOrder.indexOf(a) - modelOrder.indexOf(b),
  );
  $("models").replaceChildren(
    ...models.map(([model, totals]) => {
      const records = sample.records.filter((record) => record.model === model);
      const first = records.filter((record) => record.first_output_ms != null);
      return row([
        modelLabel(model, true),
        number(totals.requests),
        number(metrics.outcomes(totals).canceled),
        percent(metrics.outcomes(totals).failureRate),
        compact(totals.tokens.input),
        percent(metrics.ratio(totals.tokens.cached, totals.tokens.input)),
        compact(totals.tokens.output),
        duration(
          metrics.percentile(
            records.map((record) => record.duration_ms),
            0.95,
          ),
        ),
        duration(
          metrics.percentile(
            first.map((record) => record.first_output_ms),
            0.5,
          ),
        ),
      ]);
    }),
  );
  $("models-empty").hidden = models.length > 0;
  text("model-count", `${models.length} MODELS`);
}
function options(id, values, label) {
  const select = $(id);
  if (
    Array.from(select.options)
      .slice(1)
      .map((option) => option.value)
      .join("\n") === values.join("\n")
  )
    return;
  const previous = select.value;
  const first = new Option(label, "all");
  select.replaceChildren(
    first,
    ...values.map(
      (value) =>
        new Option(
          id === "model-filter" ? modelName(value) : value || "unknown",
          value,
        ),
    ),
  );
  select.value = values.includes(previous) ? previous : "all";
}
function errorDetails(record) {
  const detailsRow = document.createElement("tr");
  detailsRow.className =
    record.status === 499
      ? "error-details-row cancellation-details-row"
      : "error-details-row";
  detailsRow.id = `request-error-${record.id}`;
  const detailsCell = document.createElement("td");
  detailsCell.colSpan = 8;
  const details = document.createElement("div");
  details.className =
    record.status === 499
      ? "error-details cancellation-details"
      : "error-details";
  const metadata = document.createElement("dl");
  metadata.className = "error-metadata";
  for (const [name, value] of [
    ["Source", record.error.source],
    ["Type", record.error.type],
    ["Code", record.error.code],
    ["Upstream HTTP", record.error.status],
  ]) {
    if (value == null || value === "") continue;
    const item = document.createElement("div");
    const term = document.createElement("dt");
    term.textContent = name;
    const definition = document.createElement("dd");
    definition.textContent = value;
    item.append(term, definition);
    metadata.append(item);
  }
  const message = document.createElement("pre");
  message.className = "error-message";
  message.id = `request-error-message-${record.id}`;
  message.dataset.errorControl = "message";
  message.dataset.requestId = String(record.id);
  message.tabIndex = 0;
  message.setAttribute("aria-label", `Error message for request ${record.id}`);
  message.textContent =
    record.error.message || "No error message was provided.";
  const caption = document.createElement("p");
  caption.className = "error-caption";
  caption.textContent =
    "Credential-redacted error excerpt. Redaction is best effort; server errors may include echoed text.";
  details.append(metadata, caption, message);
  if (record.error.truncated) {
    const note = document.createElement("p");
    note.className = "error-truncation";
    note.textContent = "Message truncated by the proxy.";
    details.append(note);
  }
  detailsCell.append(details);
  detailsRow.append(detailsCell);

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className =
    record.status === 499 ? "error-toggle cancellation-toggle" : "error-toggle";
  toggle.id = `request-error-toggle-${record.id}`;
  toggle.dataset.errorControl = "toggle";
  toggle.setAttribute("aria-controls", detailsRow.id);
  const update = () => {
    const expanded = expandedErrors.has(record.id);
    const kind = record.status === 499 ? "cancellation" : "error";
    detailsRow.hidden = !expanded;
    toggle.setAttribute("aria-expanded", String(expanded));
    toggle.setAttribute(
      "aria-label",
      `${expanded ? "Hide" : "View"} ${kind} details for request ${record.id}`,
    );
    toggle.textContent = `${expanded ? "Hide" : "View"} ${kind} ${expanded ? "▴" : "▾"}`;
  };
  toggle.addEventListener("click", () => {
    if (expandedErrors.has(record.id)) expandedErrors.delete(record.id);
    else expandedErrors.add(record.id);
    update();
  });
  update();
  return { detailsRow, toggle };
}
function renderLog() {
  if (!latest) return;
  // Keep expansion memory bounded by the retained ring, including filtered rows.
  const retained = new Set(latest.recent.map((record) => record.id));
  for (const id of expandedErrors)
    if (!retained.has(id)) expandedErrors.delete(id);
  const focused = document.activeElement?.dataset.errorControl
    ? document.activeElement.id
    : null;
  const scrollPositions = new Map(
    Array.from(document.querySelectorAll(".error-message"), (message) => [
      message.id,
      message.scrollTop,
    ]),
  );
  options(
    "model-filter",
    [...new Set(latest.recent.map((record) => record.model))].sort(),
    "All models",
  );
  options(
    "effort-filter",
    [...new Set(latest.recent.map((record) => record.effort))].sort(),
    "All efforts",
  );
  const records = metrics.filter(latest.recent, {
    model: $("model-filter").value,
    effort: $("effort-filter").value,
    status: $("status-filter").value,
    search: $("search").value,
  });
  const pages = Math.max(1, Math.ceil(records.length / pageSize));
  page = Math.min(page, pages - 1);
  $("recent").replaceChildren(
    ...records
      .slice(page * pageSize, (page + 1) * pageSize)
      .flatMap((record) => {
        const started = document.createElement("div");
        const startTime = document.createElement("div");
        startTime.append(
          badge(`#${record.id}`, "request-id"),
          document.createTextNode(timeOfDay.format(new Date(record.started))),
        );
        started.append(
          startTime,
          badge(new Date(record.started).toLocaleDateString(), "table-subtext"),
        );
        const statusName =
          record.status === 499
            ? "Client canceled"
            : record.status === 429
              ? "Rate limited"
              : record.status >= 400
                ? "Request failed"
                : "Completed";
        const status = badge(
          record.status === 499 ? "499 · canceled" : record.status,
          `status-badge${record.status === 499 ? " canceled" : record.status === 429 ? " rate-limit" : record.status >= 400 ? " error" : ""}`,
        );
        status.title = statusName;
        const outcome = document.createElement("div");
        outcome.className = "request-outcome";
        outcome.append(status);
        if (record.error_kind && record.status !== 499)
          outcome.append(
            badge(record.error_kind.replaceAll("_", " "), "table-subtext"),
          );
        const requestRow = row([
          started,
          modelLabel(record.model),
          badge(record.effort || "—", "effort-badge"),
          outcome,
          duration(record.duration_ms),
          duration(record.first_output_ms),
          record.usage_reported
            ? `${compact(record.tokens.input)} / ${compact(record.tokens.output)}`
            : "—",
          record.usage_reported
            ? percent(metrics.ratio(record.tokens.cached, record.tokens.input))
            : "—",
        ]);
        if (record.status < 400 || record.status > 599 || !record.error)
          return [requestRow];
        const { detailsRow, toggle } = errorDetails(record);
        outcome.append(toggle);
        return [requestRow, detailsRow];
      }),
  );
  for (const [id, position] of scrollPositions)
    if ($(id)) $(id).scrollTop = position;
  if (focused) $(focused)?.focus({ preventScroll: true });
  $("log-empty").hidden = records.length > 0;
  text(
    "log-count",
    records.length
      ? `${page * pageSize + 1}–${Math.min((page + 1) * pageSize, records.length)} of ${number(records.length)} matching records · all retained history`
      : "No matching records",
  );
  text("page-number", `${page + 1} / ${pages}`);
  $("previous").disabled = page === 0;
  $("next").disabled = page >= pages - 1;
  text(
    "retained-count",
    `${latest.recent.length} / ${latest.recent_limit} RETAINED`,
  );
}
function render(snapshot) {
  if (latest?.started !== snapshot.started) {
    expandedErrors.clear();
    page = 0;
  }
  latest = snapshot;
  const totals = snapshot.totals,
    tokens = totals.tokens,
    outcomes = metrics.outcomes(totals),
    sample = metrics.sample(snapshot, windowMinutes);
  text("active", number(snapshot.active));
  text("uptime", uptime(snapshot.uptime_seconds));
  text("recoveries", number(snapshot.quota_recoveries));
  text("total-canceled", number(outcomes.canceled));
  text(
    "updated",
    `Updated ${timeOfDay.format(new Date(snapshot.generated))} · every 3s`,
  );
  text(
    "process-started",
    `Process started ${new Date(snapshot.started).toLocaleString()}. Chart times use your browser’s local timezone.`,
  );
  metric("total-requests", totals.requests);
  text("success-rate", percent(outcomes.successRate));
  metric("total-tokens", tokens.input + tokens.output);
  text(
    "token-split",
    `${compact(tokens.input)} input · ${compact(tokens.output)} output`,
  );
  text("cache-ratio", percent(metrics.ratio(tokens.cached, tokens.input)));
  text(
    "cache-detail",
    `${compact(tokens.cached)} of ${compact(tokens.input)} input tokens`,
  );
  metric("total-errors", totals.errors);
  text("error-detail", `${number(totals.rate_limited)} rate limited`);
  text("error-rate", percent(outcomes.failureRate));
  const windowRequests = metrics.sum(
    sample.history,
    (bucket) => bucket.totals.requests,
  );
  const windowTokens = metrics.sum(
    sample.history,
    (bucket) => bucket.totals.tokens.input + bucket.totals.tokens.output,
  );
  const coverage = metrics.sum(
    sample.history,
    (bucket) => bucket.totals.usage_reported,
  );
  const reasoning = metrics.sum(
    sample.history,
    (bucket) => bucket.totals.tokens.reasoning,
  );
  text("window-requests", `${number(windowRequests)} in window`);
  text("window-tokens", `${compact(windowTokens)} in window`);
  text(
    "usage-coverage",
    `${number(coverage)} / ${number(windowRequests)} requests reported usage · ${compact(reasoning)} reasoning tokens included in output`,
  );
  text(
    "latency-sample",
    `${number(sample.records.length)} recent completions · up to ${snapshot.recent_limit} retained`,
  );
  text(
    "window-description",
    `Last ${windowMinutes} minutes · includes the partial current minute; duration and mix charts use the latest ${snapshot.recent_limit} records.`,
  );
  for (const value of ["p50", "p95", "p99"])
    text(value, duration(sample[value]));
  text("first-output", duration(sample.firstP50));
  text(
    "first-sample",
    `${number(sample.firstCount)} / ${number(sample.records.length)} sampled requests had an observed delta. Duration includes failures and cancellations.`,
  );
  $("traffic-empty").hidden = windowRequests > 0;
  renderCharts(sample);
  renderModels(sample);
  renderLog();
}
async function refresh() {
  if (!key || paused || controller) return;
  const request = new AbortController();
  controller = request;
  let timedOut = false;
  // Bound both fetching headers and reading the snapshot body. This deadline
  // applies only to dashboard polling, never to inference requests.
  const deadline = setTimeout(() => {
    timedOut = true;
    request.abort();
  }, 10000);
  try {
    const response = await fetch("/dashboard/api", {
      headers: { Authorization: `Bearer ${key}` },
      cache: "no-store",
      credentials: "omit",
      redirect: "error",
      signal: request.signal,
    });
    if (request.signal.aborted) throw new Error("Metrics request aborted");
    if (response.status === 401) {
      disconnect();
      text("login-error", "That local client key was not accepted.");
      return;
    }
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const data = await response.json();
    if (request.signal.aborted) throw new Error("Metrics request aborted");
    $("login").hidden = true;
    $("usage").hidden = false;
    $("stale").hidden = true;
    render(data);
    connection("Live · local", "live");
  } catch (error) {
    // A previous request must not change a newer connection, or undo a pause.
    if (controller !== request) return;
    connection(timedOut ? "Connection timed out" : "Connection lost", "stale");
    if (latest) {
      text(
        "stale",
        "Live updates interrupted. Showing the last snapshot; retrying every 3 seconds.",
      );
      $("stale").hidden = false;
    } else
      text(
        "login-error",
        "Could not load metrics. Retrying every 3 seconds. Check that Claudex is running with -dashboard.",
      );
  } finally {
    clearTimeout(deadline);
    if (controller === request) {
      controller = null;
      if (key && !paused) timer = setTimeout(refresh, 3000);
    }
  }
}
$("login-form").addEventListener("submit", (event) => {
  event.preventDefault();
  clearTimeout(timer);
  controller?.abort();
  controller = null;
  key = $("key").value.trim();
  $("key").value = "";
  text("login-error", "");
  connection("Connecting…");
  refresh();
});
$("disconnect").addEventListener("click", disconnect);
$("pause").addEventListener("click", () => {
  paused = !paused;
  clearTimeout(timer);
  controller?.abort();
  controller = null;
  text("pause", paused ? "Resume live" : "Pause live");
  if (paused) {
    connection("Paused");
    text("stale", "Live updates paused. The proxy keeps collecting metrics.");
    $("stale").hidden = false;
  } else {
    connection("Reconnecting…");
    text("stale", "Resuming live updates. Showing the last snapshot until connected.");
    refresh();
  }
});
for (const button of document.querySelectorAll("[data-window]"))
  button.addEventListener("click", () => {
    windowMinutes = Number(button.dataset.window);
    for (const option of document.querySelectorAll("[data-window]")) {
      const selected = option === button;
      option.classList.toggle("selected", selected);
      option.setAttribute("aria-pressed", String(selected));
    }
    if (latest) render(latest);
  });
for (const id of ["search", "model-filter", "effort-filter", "status-filter"])
  $(id).addEventListener(id === "search" ? "input" : "change", () => {
    page = 0;
    renderLog();
  });
$("previous").addEventListener("click", () => {
  page = Math.max(0, page - 1);
  renderLog();
});
$("next").addEventListener("click", () => {
  page++;
  renderLog();
});
const resize = new ResizeObserver(() => {
  for (const item of charts.values()) item.resize();
});
resize.observe(document.querySelector("main"));
function updateNavigation() {
  const current = window.location.hash || "#overview";
  for (const link of document.querySelectorAll("nav a")) {
    const selected = link.hash === current;
    link.classList.toggle("nav-active", selected);
    if (selected) link.setAttribute("aria-current", "location");
    else link.removeAttribute("aria-current");
  }
}
window.addEventListener("hashchange", updateNavigation);
updateNavigation();
window.addEventListener("pagehide", disconnect);
