/* Pure calculations shared by the dashboard and its dependency-free tests. */
"use strict";
const ClaudexMetrics = (() => {
  const percentile = (values, fraction) => {
    if (!values.length) return null;
    const sorted = [...values].sort((a, b) => a - b);
    return sorted[Math.max(0, Math.ceil(sorted.length * fraction) - 1)];
  };
  const sum = (items, value) =>
    items.reduce((total, item) => total + value(item), 0);
  const ratio = (numerator, denominator) =>
    denominator > 0 ? numerator / denominator : null;
  // Cancellations consume request slots and may report usage, but do not
  // describe success or failure of a request the caller waited to complete.
  function outcomes(totals) {
    const canceled = totals.canceled || 0;
    const evaluated = totals.requests - canceled;
    const successful = evaluated - totals.errors;
    return {
      successful,
      failed: totals.errors,
      canceled,
      successRate: ratio(successful, evaluated),
      failureRate: ratio(totals.errors, evaluated),
    };
  }
  const ended = (record) => new Date(record.completed).getTime();
  function sample(snapshot, minutes) {
    // Select complete calendar buckets plus the partial current minute. Keeping
    // records on the same boundary avoids comparing two subtly different windows.
    const cutoff =
      Math.floor(new Date(snapshot.generated).getTime() / 60000) * 60000 -
      (minutes - 1) * 60000;
    const records = snapshot.recent.filter((record) => ended(record) >= cutoff);
    const history = snapshot.history.filter(
      (bucket) => new Date(bucket.time).getTime() >= cutoff,
    );
    const elapsed = records.map((record) => record.duration_ms);
    const first = records
      .filter((record) => record.first_output_ms != null)
      .map((record) => record.first_output_ms);
    const buckets = [1000, 5000, 15000, 30000, 60000, 120000, 300000, Infinity];
    const histogram = buckets.map(
      (bound, index) =>
        elapsed.filter(
          (value) =>
            value <= bound && (index === 0 || value > buckets[index - 1]),
        ).length,
    );
    return {
      records,
      history,
      histogram,
      p50: percentile(elapsed, 0.5),
      p95: percentile(elapsed, 0.95),
      p99: percentile(elapsed, 0.99),
      firstP50: percentile(first, 0.5),
      firstCount: first.length,
    };
  }
  function filter(
    records,
    { model = "all", effort = "all", status = "all", search = "" } = {},
  ) {
    const query = search.toLowerCase().replaceAll("_", " ").trim();
    return records.filter(
      (record) =>
        (model === "all" || record.model === model) &&
        (effort === "all" || record.effort === effort) &&
        (status === "all" ||
          (status === "errors" &&
            record.status >= 400 &&
            record.status !== 499) ||
          (status === "canceled" && record.status === 499) ||
          (status === "ok" && record.status < 400) ||
          (status === "429" && record.status === 429)) &&
        (!query ||
          `${record.id} ${record.model} ${record.effort} ${record.status} ${record.error_kind || ""} ${record.error?.source || ""} ${record.error?.type || ""} ${record.error?.code || ""} ${record.error?.status || ""} ${record.error?.message || ""}`
            .toLowerCase()
            .replaceAll("_", " ")
            .includes(query)),
    );
  }
  return { percentile, sum, ratio, outcomes, sample, filter };
})();
if (typeof module !== "undefined") module.exports = ClaudexMetrics;
