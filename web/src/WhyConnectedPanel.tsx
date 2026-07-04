import type { ResultEdge } from "./types";

/**
 * Shared edge-explanation list: kind, confidence tier + score, reason and
 * evidence refs, with the data-tier attribute carrying the provenance tier
 * (heuristic edges are visibly marked). Reused by the two-node compare panel.
 */
export function EdgeExplanationList({ edges }: { edges: ResultEdge[] }) {
  return (
    <ul className="edge-explanations">
      {edges.map((e) => (
        <li key={e.id} data-tier={e.confidence_tier}>
          <strong>{e.kind}</strong>{" "}
          <span className="tier">({e.confidence_tier})</span>
          <span className="confidence">{e.confidence.toFixed(2)}</span>
          <p className="reason">{e.reason}</p>
          {e.evidence.length > 0 && (
            <ul className="evidence">
              {e.evidence.map((ref, idx) => (
                <li key={idx}>{ref}</li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  );
}

interface Props {
  edges: ResultEdge[];
}

export function WhyConnectedPanel({ edges }: Props) {
  if (edges.length === 0) return null;
  return (
    <div className="why-connected-panel" data-testid="why-connected-panel">
      <h3>Why connected</h3>
      <EdgeExplanationList edges={edges} />
    </div>
  );
}
