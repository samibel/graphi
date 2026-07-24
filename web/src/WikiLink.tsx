// WikiLink is the SINGLE, auditable place where in-body Markdown anchors are
// rewritten into client navigation (SW-046, AC-3/AC-6). The Engine emits
// path-style links inside the wiki Markdown:
//   /wiki              → back-link to the index
//   /wiki/c/{id}       → cross-link to a neighboring community
// react-router matches those paths 1:1, so the rewrite is a trivial
// <Link to={href}> with NO slug transform, NO reorder, NO text rewrite — the
// target id and the visible link text are preserved exactly as the renderer
// produced them (preservation contract, AC-2).
//
// Any other href (absolute http(s), mailto:, fragment, etc.) is rendered INERT:
// plain escaped text with no outbound navigation capability, consistent with
// the zero-outbound posture (S1) and read-only contract (AC-6).
import type { JSX, ReactNode } from "react";
import { Link } from "react-router";

/** Props mirror what react-markdown passes to an `a` component override. */
export interface WikiLinkProps {
  href?: string;
  children?: ReactNode;
}

const INDEX_PATH = "/wiki";
// /wiki/c/{id} — id captured verbatim (no validation beyond "is a path tail").
const COMMUNITY_RE = /^\/wiki\/c\/[^/]+$/;

/** True for the exact internal wiki routes the Engine emits. */
function isInternalWikiHref(href: string): boolean {
  return href === INDEX_PATH || COMMUNITY_RE.test(href);
}

export function WikiLink({ href, children }: WikiLinkProps): JSX.Element {
  // Internal wiki path → client-side navigation, target preserved verbatim.
  if (typeof href === "string" && isInternalWikiHref(href)) {
    return (
      <Link className="wiki-xref" to={href}>
        {children}
      </Link>
    );
  }
  // Everything else → inert text. We render a non-navigating <span> so the
  // visible text (and its order) is preserved, but there is no anchor / no
  // outbound capability (S1, AC-6).
  return <span className="wiki-inert">{children}</span>;
}
