// Vitest setup: tell React this is an act() environment so component tests that
// flush effects via act(...) don't emit "not configured to support act" noise.
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT =
  true;
