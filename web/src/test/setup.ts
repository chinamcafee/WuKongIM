import { configure } from "@testing-library/react"
import "@testing-library/jest-dom/vitest"

class TestResizeObserver implements ResizeObserver {
  disconnect() {}
  observe() {}
  unobserve() {}
}

globalThis.ResizeObserver ??= TestResizeObserver

// Lazy route imports may compete with Go gates on shared CI hosts.
configure({ asyncUtilTimeout: 5000 })
