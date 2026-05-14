const WORKBENCH_STATE_KEY = "wikios.workbenchState";

export function loadWorkbenchState<T>(fallback: T): T {
  const raw = localStorage.getItem(WORKBENCH_STATE_KEY);
  if (!raw) {
    return fallback;
  }
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

export function saveWorkbenchState(value: unknown) {
  localStorage.setItem(WORKBENCH_STATE_KEY, JSON.stringify(value));
}
