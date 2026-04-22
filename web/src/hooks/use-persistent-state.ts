import { useEffect, useState } from "react";

export function usePersistentState<T>(key: string, fallback: T) {
  const [value, setValue] = useState<T>(() => {
    const raw = localStorage.getItem(key);
    if (!raw) {
      return fallback;
    }
    try {
      return JSON.parse(raw) as T;
    } catch {
      return fallback;
    }
  });

  useEffect(() => {
    localStorage.setItem(key, JSON.stringify(value));
  }, [key, value]);

  return [value, setValue] as const;
}

