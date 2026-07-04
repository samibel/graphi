// Agent-tools panel (Web/IDE Polish): runs the EP-020 agent-first analyzers —
// related_files / change_risk / agent_brief — against the currently selected
// node (or resolved seed) and renders the canonical ranked-item envelope:
// items with rank + reason + evidence path:line, the confidence top label and
// derivation method (heuristic results are VISIBLY marked, consistent with
// WhyConnectedPanel's data-tier convention), and limits (truncation).
//
// Every outcome renders a distinct, non-blank state: loading, typed
// unavailable (503 / analyzer absent from this build), analyzer-reported
// empty/ambiguous/error outcomes, and transport failure. agent_brief is gated
// on the /contract advertisement, mirroring the VS Code extension.
import { useEffect, useRef, useState } from "react";
import {
  agentBrief,
  changeRisk,
  relatedFiles,
  type AgentToolResponse,
} from "./graphiClient";
import type { AgentToolResult } from "./types";

type ToolKey = "related_files" | "change_risk" | "agent_brief";

interface Run {
  tool: ToolKey;
  status: "loading" | "done" | "failed";
  response?: AgentToolResponse;
  error?: string;
}

interface Props {
  /** Node id the target tools run against (selected node, else resolved seed). */
  target: string | null;
  /** Human-readable name for the target (qualified name when known). */
  targetLabel?: string;
  /** True when /contract advertises analyze/agent_brief (per-feature gating). */
  briefAdvertised: boolean;
}

function AgentToolResultView({ tool, result }: { tool: ToolKey; result: AgentToolResult }) {
  const evidenceById = new Map(result.evidence.map((ev) => [ev.ref_id, ev]));
  const method = result.confidence.method || "unknown";
  return (
    <div
      className="tool-result"
      data-testid="tool-result"
      data-tier={method}
      data-outcome={result.outcome}
    >
      {result.outcome === "error" ? (
        <p className="error">
          ⚠ {tool} reported an error{result.summary ? `: ${result.summary}` : ""}
        </p>
      ) : (
        <p className="summary">
          <strong>{tool}</strong> — {result.outcome}
          {result.summary ? `: ${result.summary}` : ""}
        </p>
      )}
      <p className="confidence">
        confidence: {result.confidence.top || "n/a"}{" "}
        <span className="tier">({method})</span>
        {method === "heuristic" && (
          <span className="heuristic-badge"> heuristic — verify before acting</span>
        )}
      </p>
      {result.items.length === 0 ? (
        result.outcome !== "error" && (
          <p className="hint-inline">
            {result.outcome === "empty"
              ? "No results for this target."
              : "No ranked items returned."}
          </p>
        )
      ) : (
        <ol className="tool-items">
          {result.items.map((it) => (
            <li key={it.ref_id}>
              <span className="rank">#{it.rank}</span> <code>{it.ref_id}</code>
              <p className="reason">{it.reason}</p>
              {it.evidence_ref_ids.length > 0 && (
                <ul className="evidence">
                  {it.evidence_ref_ids.map((ref) => {
                    const ev = evidenceById.get(ref);
                    return (
                      <li key={ref}>
                        {ev ? (
                          <>
                            {ev.path}:{ev.line}{" "}
                            <span className="hint-inline">({ev.role})</span>
                          </>
                        ) : (
                          ref
                        )}
                      </li>
                    );
                  })}
                </ul>
              )}
            </li>
          ))}
        </ol>
      )}
      {result.limits.truncated && (
        <p className="hint-inline truncated">
          partial result: showing {result.items.length} of{" "}
          {result.limits.total_available} (truncated
          {result.limits.dropped > 0 ? `, ${result.limits.dropped} dropped` : ""})
        </p>
      )}
    </div>
  );
}

export function AgentToolsPanel({ target, targetLabel, briefAdvertised }: Props) {
  const [run, setRun] = useState<Run | null>(null);
  // Monotonic run id: a stale response never overwrites a newer run's state.
  const seq = useRef(0);

  // Results describe the target they ran against — drop them when it changes.
  useEffect(() => {
    seq.current++;
    setRun(null);
  }, [target]);

  const invoke = (tool: ToolKey, call: () => Promise<AgentToolResponse>) => {
    const id = ++seq.current;
    setRun({ tool, status: "loading" });
    void call().then(
      (response) => {
        if (seq.current === id) setRun({ tool, status: "done", response });
      },
      (e: unknown) => {
        if (seq.current === id) {
          setRun({
            tool,
            status: "failed",
            error: String((e as Error)?.message ?? e),
          });
        }
      },
    );
  };

  const busy = run?.status === "loading";

  return (
    <div className="agent-tools-panel" data-testid="agent-tools-panel">
      <h3>Agent tools</h3>
      {target && (
        <p className="hint-inline">
          target: <code>{targetLabel ?? target}</code>
        </p>
      )}
      <div className="tool-buttons">
        <button
          type="button"
          disabled={!target || busy}
          onClick={() => invoke("related_files", () => relatedFiles(target!))}
        >
          Related files
        </button>
        <button
          type="button"
          disabled={!target || busy}
          onClick={() => invoke("change_risk", () => changeRisk(target!))}
        >
          Change risk
        </button>
        <button
          type="button"
          disabled={!briefAdvertised || busy}
          title={
            briefAdvertised
              ? undefined
              : "analyze/agent_brief not advertised by /contract"
          }
          onClick={() => invoke("agent_brief", () => agentBrief(target ?? undefined))}
        >
          Agent brief
        </button>
      </div>
      {!briefAdvertised && (
        <p className="hint-inline">
          agent brief not advertised by the daemon — button disabled.
        </p>
      )}
      {run?.status === "loading" && (
        <p className="hint-inline">running {run.tool}…</p>
      )}
      {run?.status === "failed" && (
        <p className="error">
          ⚠ {run.tool} failed: {run.error}
        </p>
      )}
      {run?.status === "done" &&
        run.response &&
        (run.response.available ? (
          <AgentToolResultView tool={run.tool} result={run.response.result} />
        ) : (
          <div className="tool-unavailable" data-testid="tool-unavailable">
            {run.tool} unavailable — {run.response.reason}
          </div>
        ))}
    </div>
  );
}
