export function createErrorReporter(limit = 20) {
  const pending: Error[] = [];
  const reported = new WeakSet<Error>();
  let sink: ((error: Error) => void) | undefined;
  return {
    capture(value: unknown) {
      const error = value instanceof Error ? value : new Error("Browser error");
      if (error.name === "AbortError" || reported.has(error)) return;
      reported.add(error);
      if (sink) sink(error);
      else if (pending.length < limit) pending.push(error);
    },
    connect(report: (error: Error) => void) {
      sink = report;
      for (const error of pending.splice(0)) report(error);
    },
  };
}
