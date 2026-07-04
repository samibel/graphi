import { useState } from "react";
import type { ResultNode, ResultEdge } from "./types";

interface Props {
  node?: ResultNode | null;
  edges?: ResultEdge[];
}

export function ExportAgentContext({ node, edges }: Props) {
  const [format, setFormat] = useState<"markdown" | "json" | "mcp">("markdown");

  const markdown = () => {
    const lines = ["# Agent Context"];
    if (node) {
      lines.push(`## Focus`, `- **${node.qualified_name}** (${node.kind}) at ${node.source_path}:${node.line}`);
    }
    if (edges && edges.length > 0) {
      lines.push("## Relationships");
      for (const e of edges) {
        lines.push(`- ${e.kind} (${e.confidence_tier}, ${e.confidence.toFixed(2)}): ${e.reason}`);
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

  return (
    <div className="export-agent-context" data-testid="export-agent-context">
      <h3>Export agent context</h3>
      <div className="export-tabs">
        {(["markdown", "json", "mcp"] as const).map((f) => (
          <button key={f} type="button" onClick={() => setFormat(f)} disabled={format === f}>
            {f}
          </button>
        ))}
      </div>
      <pre>{output}</pre>
    </div>
  );
}
