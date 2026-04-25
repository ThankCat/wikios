"use client";

import { useCallback, useEffect, useRef, useState, type DependencyList } from "react";

type ScrollBehaviorOption = ScrollBehavior | "instant";

const bottomThreshold = 28;

export function useScrollFollow<T extends HTMLElement>(deps: DependencyList = []) {
  const followBottomRef = useRef(true);
  const [element, setElement] = useState<T | null>(null);
  const [isAtBottom, setIsAtBottom] = useState(true);
  const [canScroll, setCanScroll] = useState(false);

  const viewportRef = useCallback((node: T | null) => {
    setElement(node);
  }, []);

  const updateState = useCallback(() => {
    if (!element) {
      setIsAtBottom(true);
      setCanScroll(false);
      followBottomRef.current = true;
      return;
    }
    const maxScrollTop = Math.max(0, element.scrollHeight - element.clientHeight);
    const nextCanScroll = maxScrollTop > bottomThreshold;
    const nextAtBottom = maxScrollTop - element.scrollTop <= bottomThreshold;
    setCanScroll(nextCanScroll);
    setIsAtBottom(nextAtBottom);
    followBottomRef.current = nextAtBottom;
  }, [element]);

  const scrollToTop = useCallback(
    (behavior: ScrollBehaviorOption = "smooth") => {
      element?.scrollTo({ top: 0, behavior: behavior as ScrollBehavior });
      followBottomRef.current = false;
      setIsAtBottom(false);
    },
    [element],
  );

  const scrollToBottom = useCallback(
    (behavior: ScrollBehaviorOption = "smooth") => {
      if (!element) {
        return;
      }
      element.scrollTo({ top: element.scrollHeight, behavior: behavior as ScrollBehavior });
      followBottomRef.current = true;
      setIsAtBottom(true);
    },
    [element],
  );

  useEffect(() => {
    if (!element) {
      return;
    }
    updateState();
    element.addEventListener("scroll", updateState, { passive: true });
    window.addEventListener("resize", updateState);
    return () => {
      element.removeEventListener("scroll", updateState);
      window.removeEventListener("resize", updateState);
    };
  }, [element, updateState]);

  useEffect(() => {
    if (!element) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      if (followBottomRef.current) {
        element.scrollTo({ top: element.scrollHeight, behavior: "smooth" });
      }
      updateState();
    });
    return () => window.cancelAnimationFrame(frame);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [element, updateState, ...deps]);

  return {
    viewportRef,
    isAtBottom,
    canScroll,
    showControls: canScroll && !isAtBottom,
    scrollToTop,
    scrollToBottom,
  };
}
