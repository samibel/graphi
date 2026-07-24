// Minimal render helper for the SW-046 component tests (no @testing-library
// dependency). Mounts a React element into a fresh jsdom container under a
// MemoryRouter, flushing effects via act(). Returns the container + an unmount.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Routes, Route } from "react-router";
import type { ReactElement, ReactNode } from "react";

export interface Rendered {
  container: HTMLElement;
  unmount: () => void;
}

/** Render `ui` under a MemoryRouter rooted at `initialPath`. */
export async function renderAt(
  ui: ReactNode,
  initialPath = "/",
): Promise<Rendered> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  let root!: Root;
  await act(async () => {
    root = createRoot(container);
    root.render(<MemoryRouter initialEntries={[initialPath]}>{ui}</MemoryRouter>);
  });
  return {
    container,
    unmount: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

/**
 * Render a routed page component under a MemoryRouter with a `path` pattern so
 * `useParams` resolves. `initialPath` is the concrete URL to enter.
 */
export async function renderRoute(
  path: string,
  element: ReactElement,
  initialPath: string,
): Promise<Rendered> {
  return renderAt(
    <Routes>
      <Route path={path} element={element} />
    </Routes>,
    initialPath,
  );
}

/** Flush a microtask/macrotask so pending fetch .then() chains settle. */
export async function flush(): Promise<void> {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}
