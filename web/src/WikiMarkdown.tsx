// WikiMarkdown renders the Engine's raw wiki Markdown SAFELY (SW-046, AC-2/AC-6).
//
// Security posture (matches SW-045 S3 — payload-derived qualified names, paths,
// and kinds are UNTRUSTED text):
//   - raw HTML is HARD-disabled: NO rehype-raw, NO dangerouslySetInnerHTML.
//   - `skipHtml` drops any literal HTML nodes; `disallowedElements` additionally
//     bans `html`/`script`/`iframe`/`style` so nothing can smuggle markup.
//   - identifiers stay as escaped `<code>` text (the Engine wraps them in
//     backticks); react-markdown emits React text nodes, never HTML strings.
//
// Preservation posture (AC-2): the body is rendered VERBATIM. The ONLY transform
// is link rewriting, and it happens in exactly one place — the `a` component
// override (WikiLink). Member order, cross-link order, and visible text are left
// exactly as the Engine produced them.
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { WikiLink } from "./WikiLink";

export interface WikiMarkdownProps {
  /** Raw Markdown bytes as returned by /wiki or /wiki/c/{id}, unmodified. */
  body: string;
}

// Elements that could carry raw HTML / active content. Belt-and-braces on top
// of `skipHtml` (which already drops literal HTML nodes by default).
const DISALLOWED = ["html", "script", "iframe", "style"];

export function WikiMarkdown({ body }: WikiMarkdownProps) {
  return (
    <div className="wiki-body">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        disallowedElements={DISALLOWED}
        components={{ a: WikiLink }}
      >
        {body}
      </ReactMarkdown>
    </div>
  );
}
