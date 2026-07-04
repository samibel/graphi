import { useEffect, useRef, useState } from "react";
import type { ResultNode, ResultEdge } from "./types";

interface Props {
  node?: ResultNode | null;
  edges?: ResultEdge[];
}

/** Legacy textarea/execCommand copy path for contexts without the async
 * Clipboard API (or where it is permission-denied). Returns success. */
function fallbackCopy(text: string): boolean {
  const ta = document.createElement("textarea");
  ta.value = text;
  // Keep it out of view without display:none (which would prevent selection).
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.focus();
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  ta.remove();
  return ok;
}

export function ExportAgentContext({ node, edges }: Props) {
  const [format, setFormat] = useState<"markdown" | "json" | "mcp">("markdown");
  const [copied, setCopied] = useState(false);
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (copyTimer.current) clearTimeout(copyTimer.current);
    };
  }, []);

  const markdown = () => {
    const lines = ["# Agent Context"];
    if (node) {
      lines.push(`## Focus`, `- **${node.qualified_name}** (${node.kind}) at ${node.source_path}:${node.line}`);
    }
    if (edges && edges.length > 0) {
      lines.push("## Relationships");
      for (const e of edges) {
        lines.push(`- ${e.kind} (${e.confidence_tier}, ${e.confidence.toFixed(2)}): ${e.reason}`);
        for (const ref of e.evidence) {
          lines.push(`  - evidence: ${ref}`);
        }
      }
    }
    return lines.join("\n");
  };

  const json = () => {
    return JSON.stringify({ node, edges }, null, 2);
  };

  const mcp = () => {
    return node
      ? `mcp__graphi__explain_symbol --symbol ${node.id}`
      : "mcp__graphi__agent_brief";
  };

  const output = format === "markdown" ? markdown() : format === "json" ? json() : mcp();

  const confirmCopied = () => {
    setCopied(true);
    if (copyTimer.current) clearTimeout(copyTimer.current);
    copyTimer.current = setTimeout(() => setCopied(false), 1500);
  };

  const copy = async () => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(output);
        confirmCopied();
        return;
      }
    } catch {
      /* fall through to the legacy path */
    }
    if (fallbackCopy(output)) confirmCopied();
  };

  return (
    <div className="export-agent-context" data-testid="export-agent-context">
      <h3>Export agent context</h3>
      <div className="export-tabs">
        {(["markdown", "json", "mcp"] as const).map((f) => (
          <button
            key={f}
            type="button"
            onClick={() => {
              setFormat(f);
              setCopied(false);
            }}
            disabled={format === f}
          >
            {f}
          </button>
        ))}
        <button
          type="button"
          className="copy-button"
          data-testid="copy-button"
          onClick={() => void copy()}
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre>{output}</pre>
    </div>
  );
}
