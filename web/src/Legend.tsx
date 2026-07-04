// Persistent legend for the highlight encodings (U6). Text/SVG only — no
// dangerouslySetInnerHTML (S3). Encodings are color-independent-redundant (U5):
// blast = red filled + large, citation = amber dashed, dimmed = faded gray.
import {
  COLOR_BLAST,
  COLOR_CITATION,
  COLOR_DIMMED,
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
        citation / evidence (dashed)
      </span>
      <span className="legend-item">
        <i
          className="swatch swatch-heuristic"
          style={{ borderColor: "#ff6b6b", borderStyle: "dotted" }}
        />
        heuristic (dotted)
      </span>
      <span className="legend-item">
        <i className="swatch swatch-dimmed" style={{ background: COLOR_DIMMED }} />
        out of scope (dimmed)
      </span>
    </span>
  );
}
