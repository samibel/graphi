// Persistent legend for the graph encodings (U6). Text/SVG only — no
// dangerouslySetInnerHTML (S3). Highlight encodings are redundant beyond color
// (U5): blast = red + enlarged, citation = amber + thicker edge, dimmed =
// faded gray. Neutral node colors identify the node kind.
import {
  COLOR_BLAST,
  COLOR_CITATION,
  COLOR_DIMMED,
  KIND_COLORS,
} from "./highlights";

export function Legend() {
  return (
    <span className="legend" aria-label="highlight legend">
      <span className="legend-item">
        <i className="swatch swatch-blast" style={{ background: COLOR_BLAST }} />
        blast-radius (in scope)
      </span>
      <span className="legend-item">
        <i
          className="swatch swatch-citation"
          style={{ borderColor: COLOR_CITATION }}
        />
        citation / evidence
      </span>
      <span className="legend-item">
        <i className="swatch swatch-dimmed" style={{ background: COLOR_DIMMED }} />
        out of scope (dimmed)
      </span>
      <span className="legend-item">
        <i className="swatch" style={{ background: KIND_COLORS.function }} />
        function
      </span>
      <span className="legend-item">
        <i className="swatch" style={{ background: KIND_COLORS.type }} />
        type
      </span>
      <span className="legend-item">
        <i className="swatch" style={{ background: KIND_COLORS.file }} />
        file
      </span>
    </span>
  );
}
