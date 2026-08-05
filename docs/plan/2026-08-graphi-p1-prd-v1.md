<!-- REGISTRATION HEADER (2026-08-05) — added by this repository around the supplied
     document. Nothing in this header is part of the supplied text. The supplied text
     starts at the marker line that closes this header and runs unaltered to the end
     of the file, except for the character-encoding repair declared below. -->

# Registration record — P1 Trust & Coverage Intelligence PRD v1.0

**This section was added by the repository (2026-08-05). It is not part of the supplied
document.** The supplied text begins at the marker line below and runs to the end of the
file.

## Provenance — SUPPLIED IN THE TASK MESSAGE, transcribed; weaker than a file copy

The document below was supplied by the product owner (`samibel`) **inline in a task
message** on 2026-08-05 and transcribed into this file. This is the same **weaker
provenance class** as the July P1 PRD
([`2026-07-graphi-p1-trust-surface-prd.md`](2026-07-graphi-p1-trust-surface-prd.md)):
no independent original file exists to compare against, so the hash below is the hash
**of the transcription as registered**. It proves this file has not changed since
registration — not that the transcription is byte-identical to the owner's source.

### Declared alteration: character-encoding repair

The July registration could state "nothing in the body was corrected". **This one cannot,
and the difference is recorded rather than hidden.**

The supplied text arrived **mojibaked**: UTF-8 bytes decoded as Latin-1, so every
non-ASCII character was delivered as its multi-byte mis-rendering (`Ã¤` for `ä`, `ÃŸ` for
`ß`, `â€”` for `—`, `âœ…` for `✅`, `â‰¤` for `≤`, and so on). Registering that byte stream
verbatim would have preserved a transport defect, not the owner's document, and would
have made every German word in the PRD unsearchable and unquotable.

The transcription therefore applies **exactly one class of change: the inverse mojibake
mapping**, restoring each mis-rendered sequence to the character it encodes. Concretely:

```
Ã¤→ä  Ã¶→ö  Ã¼→ü  Ã„→Ä  Ã–→Ö  Ãœ→Ü  ÃŸ→ß
â€”→—  â€“→–  â€ž→„  â€œ→"  â€¢→•  â†’→→
â‰¤→≤  â‰¥→≥  âœ…→✅  âŒ→❌
```

**No other change was made.** Wording, typography, section numbering, table structure,
checkbox state and every claim are preserved as supplied — including the unresolved
`Status: Draft`, the open questions in §12, and the statements that conflict with this
repository's shipped code. Where the repository disagrees with the document, the
disagreement is recorded in the companion delta, never by editing the body.

**Registration hash.** The registered body is 60 570 bytes, 750 lines,
`sha256:cc538e6c1498c6dd9df9ed057a4029061fb9d73cae49310a09221f105a96e468`.
The hash covers the body **after** the encoding repair — it pins what was registered,
not the mojibaked transport form, which no longer exists anywhere. Re-verify that the
body is unchanged since registration:

```bash
sed -n '/^<!-- BEGIN SUPPLIED DOCUMENT/,$p' docs/plan/2026-08-graphi-p1-prd-v1.md \
  | tail -n +2 | shasum -a 256
# → cc538e6c1498c6dd9df9ed057a4029061fb9d73cae49310a09221f105a96e468
```

## Registration facts

| Field | Value |
|---|---|
| Registered path | `docs/plan/2026-08-graphi-p1-prd-v1.md` |
| Registered on | 2026-08-05 |
| Provenance | Supplied inline in the product owner's task message, transcribed with a declared encoding repair (see above) |
| Document version / date | v1.0, 2026-08-05 (the document's own header) |
| Document status | **Draft** (the document's own header) |
| Author / Owner | `Sami Bel` per the document's own header. §12 still asks who the binding P1 owner and technical approver are — the header names an author, not a resolved owner. |
| Relation to the July P1 PRD | A **condensed rewrite** of [`2026-07-graphi-p1-trust-surface-prd.md`](2026-07-graphi-p1-trust-surface-prd.md) (77 KB, 2 901 lines). Same product bet, different vocabulary and phase cut. It does not supersede that document silently — see the authority note below. |
| Companion delta | [`2026-08-graphi-p1-prd-v1-delta.md`](2026-08-graphi-p1-prd-v1-delta.md) — the binding, point-by-point reconciliation against the code shipped in v0.8.0 |

## Where this document sits in the authority chain

- **Against the frozen trust contract, this document wins.** The owner decided on
  2026-08-05 that where PRD v1.0 and
  [`2026-08-graphi-p1-trust-contract-v1.md`](2026-08-graphi-p1-trust-contract-v1.md)
  contradict each other, **PRD v1.0 is binding** and the contract is amended to follow.
  That decision is what makes the wire-contract changes in the companion delta (§A)
  legitimate rather than a regression. The contract's own §6 change procedure records
  the amendment.
- **It does not outrank the P0 PRD or the 9/10 master plan.** Like its July predecessor
  it precisifies P1 only and yields to higher-ranked planning on conflict.
- **Its stated precondition is still not met.** §8 Phase 0 requires a documented P0 GO
  before P1 production code. Per
  [`2026-07-graphi-p0-completion-checklist.md`](2026-07-graphi-p0-completion-checklist.md)
  no P0 Go/No-Go has been held. P1 production code nevertheless exists in this repository;
  that deliberate deviation was recorded in advance in
  [`docs/decisions/2026-08-p1-start-before-p0-go.md`](../decisions/2026-08-p1-start-before-p0-go.md).
  Registering this text neither starts P1 nor retroactively supplies the missing GO.
- **Most of this document describes work that already shipped.** Phases 0–9 were built
  against the July PRD and released as v0.8.0 (`f12b759` … `06719ca`). Reading this PRD
  as a forward plan without the companion delta would misstate the project's position.

<!-- BEGIN SUPPLIED DOCUMENT — transcribed from the owner's task message of 2026-08-05 with the declared encoding repair above. Everything below this line is the supplied text. Do not edit below this line. -->
# PRD — Graphi P1: Trust & Coverage Intelligence
**Version:** 1.0 | **Datum:** 2026-08-05 | **Autor:** Sami Bel | **Status:** Draft

## 1. Executive Summary
- **Problem:** Graphi unterscheidet intern bereits zwischen `confirmed`, `derived` und `heuristic` Edges und sammelt weitere Qualitätssignale wie Linker-Statistiken, degradierte Go-Typecheck-Units, übersprungene Dateien, Parse-Skip-Diagnostics, externe Nodes und Graph-Freshness. Diese Signale sind jedoch über mehrere Komponenten verteilt, teilweise nur während eines Ingest-Laufs verfügbar und nicht als einheitliche, persistierte und versionierte Trust-Antwort für CLI-Nutzer oder MCP-Agenten zugänglich. Betroffen sind vor allem AI-Coding-Agents, Entwickler, Reviewer und Security Engineers. Eine technisch erfolgreiche Abfrage kann deshalb fälschlich als vollständig oder für automatische Änderungen ausreichend interpretiert werden. Die wichtigste Evidenz ist die wiederkehrende Kritik, dass Graphi bei Preview-Sprachen heuristische Beziehungen verwendet, externe Dependencies nur bis zur lokalen Boundary modelliert und der Nutzer die tatsächliche Abdeckung eines Ergebnisses nicht sofort erkennt.
- **Lösung:** P1 führt eine lokale, deterministische Trust- und Coverage-Surface ein. Pro erfolgreicher Graph-Generation wird ein versionierter `TrustSnapshot` erzeugt, der Freshness, Confidence-Verteilung, Linker-Auflösung, Typecheck-Degradation, Parse-Skips, externe Boundaries und bekannte Limits zusammenführt. Die Informationen werden über `graphi trust-report`, ein kanonisches JSON-Format und das Labs-MCP-Tool `graph_health` bereitgestellt. Drei feste Policies — `exploratory-v1`, `review-v1` und `automated-change-v1` — bewerten, ob die vorhandene Evidenz für den jeweiligen Anwendungsfall ausreicht. Ein späterer Labs-Schritt ergänzt target-bezogene Trust-Assessments und eine strikt gefilterte Query-Surface, ohne die bestehenden Stable-12-Verträge zu verändern.
- **Erfolg bedeutet:** P1 gilt als erfolgreich, wenn 100 % der Trust-Ausgaben generation-bound und deterministisch sind, kein fehlender oder veralteter Snapshot als gesund bewertet wird, CLI und MCP byte-semantisch dieselben Fakten liefern, `graph_health` unter Warm-Start-Bedingungen einen p95 von höchstens 75 ms erreicht, die Snapshot-Erzeugung den Full-Index-p95 um höchstens 3 % erhöht, keine Source Bytes oder Netzwerkdaten verarbeitet werden und alle definierten Golden-, Parity-, Crash-, Privacy- und Policy-Evals bestanden sind.

## 2. Zielgruppe
- **Primäre Persona:** AI-Coding-Agent beziehungsweise Agent-Orchestrator, der Graphi lokal über MCP verwendet. Kontext: Der Agent soll Code erklären, Reviews unterstützen, Impact analysieren oder Änderungen vorbereiten. Bedarf: Vor einer riskanten Aktion muss er feststellen können, ob der Graph frisch, ausreichend abgedeckt und für die gewählte Handlung geeignet ist. Aktuelle Lösung ohne P1: Der Agent kombiniert `status`, einzelne Query-Ergebnisse und eigene Annahmen; dadurch besteht Overtrust- oder Undertrust-Risiko.
- **Sekundäre Persona:** Softwareentwickler, Maintainer, Reviewer und Security Engineer. Kontext: Sie prüfen Caller-, Reference-, Impact-, Taint- oder Refactoring-Ergebnisse. Bedarf: Sie benötigen eine verständliche Erklärung, welche Teile bestätigt, heuristisch, degradiert, übersprungen oder durch externe Boundaries begrenzt sind. Aktuelle Lösung ohne P1: Manuelle Prüfung mehrerer Commands, Logs und Dokumentationsseiten.
- **Nicht-Zielgruppe:** Nicht-technische Endanwender; globale Cross-Repository-Suchplattformen; Cloud-Code-Indexer; CVE-Datenbanken; vollständige Dependency-Quellcodeanalyse; Teams, die eine garantierte semantische Vollständigkeit über beliebige Sprachen, Build-Systeme und externe Services erwarten; Nutzer, die Graphi als Ersatz für CodeQL, Semgrep oder Sourcegraph einsetzen wollen.

## 3. Scope

### ✅ In Scope (MVP)
- [ ] **Gemeinsames Freshness-Modul** — Bestehende read-only Status- und Drift-Logik aus der CLI in eine wiederverwendbare Komponente extrahieren, ohne das existierende `graphi status`-Verhalten zu ändern.
- [ ] **Versioniertes Trust-Domainmodell** — `TrustSnapshot`, `GraphGenerationRef`, `TrustFacts`, `TrustFinding`, `TrustAssessment`, `TrustPolicy` und `TrustVerdict` mit Schema-Version 1 definieren.
- [ ] **Generation-bound TrustSnapshot** — Trust-Fakten nach einem erfolgreichen Full-Ingest sammeln und an die erfolgreiche Graph-Generation binden.
- [ ] **Persistenz ohne DB-Schemamigration** — Aggregierten Snapshot als kanonisches JSON unter einem versionierten `kv_meta`-Key speichern; unbekannte oder zukünftige Schema-Versionen fail-closed behandeln.
- [ ] **Confidence-Verteilung** — Anzahl und Anteil von `confirmed`, `derived` und `heuristic` Edges ausgeben.
- [ ] **Linker-Auflösung** — `ResolvedDerived`, `ResolvedHeuristic`, `ResolvedExternal`, `Skipped` und `Ambiguous` in den Snapshot übernehmen.
- [ ] **Typecheck-Degradation** — degradierte Units, Type Errors, übersprungene Dateien und Dropped Intents aggregiert ausgeben.
- [ ] **Parse-Skip-Diagnostics** — Source-freie Informationen zu Oversize-, Timeout-, Depth-, Unreadable- und Parse-Error-Skips persistieren.
- [ ] **External-Boundary-Fakten** — Anzahl externer Nodes, lokale eingehende Edges und betroffene lokale Bereiche aggregieren; keine Navigation in nicht indexierten Quellcode vortäuschen.
- [ ] **CLI `graphi trust-report`** — menschenlesbare und JSON-fähige Ausgabe mit Policy-Auswahl und stabilen Exit-Codes.
- [ ] **Labs-MCP-Tool `graph_health`** — Repository- und optional target-bezogene Trust-Abfrage über das bestehende MCP-stdio-Profil.
- [ ] **Feste Policies** — `exploratory-v1`, `review-v1` und `automated-change-v1` mit nachvollziehbaren, versionierten Regeln.
- [ ] **Target-bezogenes Trust-Assessment** — Für ein eindeutig aufgelöstes Symbol die im relevanten Query-Scope verwendeten Tiers, Boundaries und Limits bewerten.
- [ ] **Strict-Query-Prototyp in Labs** — Query-Ergebnisse nach minimalem Confidence-Tier filtern und die Anzahl zurückgehaltener Ergebnisse explizit melden.
- [ ] **Capability Matrix** — Pro Sprache maschinenlesbar ausgeben, ob sie `typed-confirmed`, `cross-file-heuristic`, `intra-file-only` oder `parse-only` unterstützt.
- [ ] **Dokumentation und Evals** — Trust-Modell, Policies, Grenzen, Beispiele, Golden-Set, Snapshot-Parity, Privacy und Performance dokumentieren und testen.

### ❌ Out of Scope (bewusst NICHT gebaut)
- [ ] **Neue Programmiersprachen oder GA-Promotion einer Preview-Sprache** — P1 soll Vertrauen in vorhandene Evidenz sichtbar machen, nicht die Sprachabdeckung horizontal erweitern.
- [ ] **Vollständiges Indexieren externer Dependencies** — Würde Local-first-Scope, Indexgröße und Performance stark verändern; P1 modelliert nur die Boundary.
- [ ] **Cross-Repository-Graph** — Eigenständige spätere Produktwette; nicht notwendig für eine lokale Trust-Surface.
- [ ] **Änderung der Stable-12-MCP-Verträge** — Der stabile Tool-Satz und seine Schemas bleiben unverändert; neue Agentenfunktionen werden Labs-gated.
- [ ] **Numerischer Gesamt-Trust-Score** — Eine einzelne Zahl würde unterschiedliche Risiken verdecken. P1 liefert Fakten, Findings und Policy-Verdicts.
- [ ] **Automatische Codeänderungen oder Refactoring-Promotion** — P1 liefert Safety-Evidenz, führt aber selbst keine produktiven Codeänderungen aus.
- [ ] **Taint-Engine-Verbesserungen** — Taint kann die Trust-Surface später konsumieren, ist aber kein Implementierungsteil von P1.
- [ ] **Semantic Search, Embeddings oder LLM-Integration** — Für P1 nicht erforderlich und unvereinbar mit der deterministischen Kernaufgabe.
- [ ] **Web UI, VS-Code-Extension oder Dashboard** — CLI und MCP sind die einzigen neuen Surfaces.
- [ ] **Cloud-API, Telemetrie oder Remote-Evaluation** — P1 bleibt vollständig lokal und Zero-Egress-konform.
- [ ] **DB-Tabellenmigration** — MVP nutzt vorhandene Metadaten-/Artifact-Muster; eine neue Tabelle ist nur nach separater Architekturentscheidung zulässig.
- [ ] **Persistieren von Source Bytes, Code-Snippets oder Prompt-Inhalten** — Trust-Artefakte enthalten ausschließlich aggregierte und normalisierte Metadaten.

## 4. Erfolgsmetriken

| Metrik | Zielwert | Messmethode | Zeitrahmen |
|---|---:|---|---|
| Generation-bound Snapshot-Korrektheit | 100 % | Golden Tests mit Generation-Mismatch, abgebrochenem Ingest und erfolgreichem Full Pass | Vor Labs-Rollout |
| False-safe Verdicts | 0 | Mutations-Eval: Snapshot/Freshness/Diagnostics gezielt entfernen oder verfälschen; Policy darf nie `PASS` liefern | Jede CI-Ausführung |
| CLI/MCP-Faktenparität | 100 % | Beide Surfaces gegen denselben kanonischen DTO-Byte-Stream testen | Vor Merge von Phase 6 |
| JSON-Determinismus | 100 % byte-identisch bei gleichem Graphzustand | 100 wiederholte Serialisierungen plus Snapshot-Golden-Files | Jede CI-Ausführung |
| `graph_health` Warm p95 | ≤ 75 ms | Benchmark mit vorhandenem SQLite-Graph und 1.000 Requests | Vor Public Beta |
| `trust-report --json` Warm p95 | ≤ 100 ms | CLI-Benchmark mit 1.000 Aufrufen | Vor Public Beta |
| Snapshot-Overhead Full Index p95 | ≤ 3 % | A/B-Benchmark auf Referenz-Repositories mit Trust Collector an/aus | Vor Default-Aktivierung |
| Zusätzliche persistierte Größe | ≤ 1 MB pro Graph-Generation im MVP | Artifact-/DB-Größenvergleich | Vor Public Beta |
| Source- und Privacy-Leakage | 0 Source Bytes, 0 Secrets | Fixture mit Secrets, langen Kommentaren und Prompt-Injection-Text; Output-Scanner | Jede CI-Ausführung |
| Stable-12-Vertragsänderungen | 0 | Coverage- und Stable-Contract-Guards | Jede CI-Ausführung |
| Policy-Golden-Set | 100 % erwartete Verdicts | Mindestens 60 versiegelte Fälle über alle drei Policies | Vor Public Beta |
| Target-Scope-Abstention | ≥ 99 % korrekt | Golden-Set mit unbekannten, mehrdeutigen und externen Targets | Vor Public Beta |
| Dokumentierte Finding-Codes | 100 % | Schema-/Dokumentations-Lint | Vor Release Candidate |
| Labs-Tool nicht ohne Opt-in sichtbar | 100 % | MCP-Katalogtest mit und ohne `WithLabs()` | Jede CI-Ausführung |
| Nutzerverständnis | ≥ 80 % lösen fünf Trust-Fragen korrekt innerhalb von 5 Minuten | Moderierter Test mit mindestens 10 Entwicklern | Innerhalb der Beta-Phase |

## 5. Kernkonzepte / Begriffe
- **TrustSnapshot** = Versionierte, persistierte und generation-bound Zusammenfassung aller Trust-relevanten Fakten eines erfolgreich abgeschlossenen Graph-Builds.
- **GraphGenerationRef** = Referenz auf die Graph-Generation, für die der Snapshot erstellt wurde. Enthält mindestens Schema-Version, Graphpfad-/Repository-Identität, Full-Pass-Generation, Git-Commit soweit verfügbar und Erstellungszeitpunkt.
- **TrustFacts** = Unbewertete Fakten, darunter Freshness, Edge-Tier-Verteilung, Linker-Statistiken, Typecheck-Degradation, Parse-Skips, External Boundaries und Capability-Level.
- **TrustFinding** = Maschinenlesbare Feststellung mit stabilem Code, Severity, Scope und aggregierter Evidenz, beispielsweise `GRAPH_STALE`, `SNAPSHOT_MISSING`, `HEURISTIC_EDGES_PRESENT`, `AMBIGUOUS_REFERENCES`, `DEGRADED_TYPECHECK` oder `EXTERNAL_BOUNDARY_REACHED`.
- **TrustPolicy** = Versionierter Regelsatz, der Fakten für einen konkreten Anwendungsfall bewertet.
- **TrustVerdict** = `PASS`, `WARN`, `FAIL` oder `UNVERIFIED`. `UNVERIFIED` wird verwendet, wenn die notwendige Evidenz fehlt oder nicht generation-bound ist.
- **exploratory-v1** = Policy für Navigation, Erklärung und Recherche. Heuristische Ergebnisse sind erlaubt, müssen aber sichtbar sein. Fehlender Snapshot oder Generation-Mismatch bleibt `UNVERIFIED`.
- **review-v1** = Policy für Code Review und Change-Risk-Unterstützung. Freshness ist erforderlich; relevante Degradation, Ambiguity und External Boundaries führen mindestens zu `WARN`, schwerwiegende Coverage-Lücken zu `FAIL`.
- **automated-change-v1** = Fail-closed Policy für autonome oder halbautonome Änderungen. Der relevante Target-Scope darf keine unbekannten oder verschwiegenen Lücken enthalten; heuristische, mehrdeutige, degradierte oder stale Evidenz blockiert die Freigabe.
- **Repository-Scope** = Bewertung des gesamten aktuell indexierten Repositories.
- **Target-Scope** = Bewertung eines eindeutig aufgelösten Symbols und des für die angefragte Operation relevanten Graph-Ausschnitts.
- **Confidence Tier** = Geschlossene Evidenzklasse eines Edges: `confirmed`, `derived` oder `heuristic`.
- **Coverage** = Sichtbare Beschreibung dessen, was analysiert wurde und wo bekannte Lücken liegen. Coverage ist keine Garantie semantischer Vollständigkeit.
- **External Boundary** = Übergang von lokal indexiertem Code zu einem externen, nicht navigierbaren Symbol oder Package.
- **Degraded Unit** = Package- oder Analyse-Unit, die nur teilweise verarbeitet werden konnte.
- **Abstention** = Bewusstes `UNVERIFIED`, `FAIL` oder `REFUSED`, wenn Graphi eine sichere positive Aussage nicht belegen kann.
- **Capability Level** = Technische Fähigkeit einer Sprachintegration: `typed-confirmed`, `cross-file-heuristic`, `intra-file-only` oder `parse-only`.

### Annahmen und Invarianten
- Derselbe Graphzustand und dieselbe Policy müssen immer dieselbe Trust-Antwort erzeugen.
- Fehlende Evidenz darf niemals als Nullproblem interpretiert werden.
- Ein fehlender Snapshot ist nicht gleichbedeutend mit einem gesunden Graphen.
- Ein leer gefiltertes Ergebnis muss zwischen „wirklich leer" und „Ergebnisse wegen Confidence zurückgehalten" unterscheiden.
- Human-readable CLI und MCP nutzen dasselbe kanonische Domainmodell; keine Surface implementiert eigene Trust-Regeln.
- Facts und Policy-Bewertung werden getrennt gespeichert bzw. berechnet. Der Snapshot enthält Fakten; das Verdict wird aus Fakten plus Policy deterministisch abgeleitet.
- Kein Finding enthält Source Bytes. Pfade werden normalisiert, längenbegrenzt und in Listen deterministisch sortiert.
- Der bestehende `graphi status`-Vertrag bleibt funktional und semantisch unverändert.
- Stable-12 bleibt unverändert. `graph_health` und Strict Query starten in Labs.
- P1 beginnt produktiv erst nach dokumentiertem P0-GO. Ohne P0-GO sind nur Dokumentation, Contract-Review und vorbereitende Test-Harness-Arbeiten erlaubt.

### Warum ein einfacher Workflow oder deterministischer Call nicht ausreicht
Ein Call wie `callers(MyService)` kann deterministisch ausgeführt werden und trotzdem ein unvollständiges Bild liefern. Die Ergebnisliste allein beantwortet nicht, ob relevante Packages degradiert waren, ob Files übersprungen wurden, ob der Pfad an einer externen Dependency endet, wie viele Kanten heuristisch sind oder ob der Graph stale ist. Für eine Agentenentscheidung muss der deterministische Query-Output mit einer kontextbezogenen Trust-Bewertung kombiniert werden. P1 verwendet dafür keine generative KI; die AI-spezifische Relevanz liegt darin, einem probabilistischen Agenten eine deterministische, fail-closed Entscheidungsgrundlage zu geben.

### Kanonisches JSON-Schema auf Feature-Ebene
```json
{
  "schema_version": 1,
  "snapshot": {
    "state": "complete",
    "generation": {
      "full_pass_generation": "opaque-generation-id",
      "repository": "local-repository-id",
      "git_commit": "optional-commit",
      "created_at": "RFC3339"
    }
  },
  "freshness": {
    "current": true,
    "drift": false,
    "full_pass_in_progress": false,
    "recommendation": "none"
  },
  "facts": {
    "edge_tiers": {
      "confirmed": 100,
      "derived": 20,
      "heuristic": 5
    },
    "link_resolution": {
      "resolved_derived": 20,
      "resolved_heuristic": 5,
      "resolved_external": 4,
      "skipped": 1,
      "ambiguous": 0
    },
    "degradation": {
      "degraded_units": 0,
      "type_errors": 0,
      "skipped_files": 0,
      "dropped_intents": 0
    },
    "parse_skips": {
      "total": 0,
      "by_reason": {}
    },
    "external_boundaries": {
      "nodes": 4,
      "incoming_local_edges": 7
    },
    "capabilities": []
  },
  "assessment": {
    "policy": "review-v1",
    "verdict": "warn",
    "findings": []
  }
}
```

## 6. Technische Constraints
- **Stack:** Go gemäß dem im Repository gepinnten Toolchain-Stand; SQLite als lokaler Graph Store; MCP über stdio; bestehende CLI- und Surface-Architektur; CGo-freies Default-Binary; kanonisches JSON; keine LLM- oder Embedding-Abhängigkeit.
- **Bestehende Codebasis:**
  - `cmd/graphi/status.go` enthält die bestehende, aktuell CLI-gebundene Freshness-/Drift-Auswertung.
  - `internal/state`, `internal/gitinfo`, `internal/ingestlock` und `cmd/internal/runtime` liefern vorhandene Status-Primitiven.
  - `engine/link.Stats` enthält Linker-Zähler für `ResolvedDerived`, `ResolvedHeuristic`, `ResolvedExternal`, `Skipped` und `Ambiguous`.
  - `engine/ingest/ingester.go` hält Linker- und Skip-Informationen aktuell teilweise nur in-memory.
  - `engine/typeresolve.Result` enthält bestätigte Edges, degradierte Units, Type Errors, Skipped Files und Dropped Intents, wird aber nach der Verarbeitung verworfen.
  - `core/model/edge.go` definiert und validiert die geschlossene Confidence-Tier-Menge.
  - `core/graphstore` persistiert Edge-Tier und Evidence; Store-Level-Tier-Counting existiert bereits.
  - `engine/ingest/warmstart.go` besitzt eine Full-Pass-Generation und Marker für laufende beziehungsweise unterbrochene Ingests.
  - `engine/diagnostic/filter.go` enthält ein wiederverwendbares Muster für `WithheldByConfidence`.
  - `surfaces/client`, `surfaces/cli` und `surfaces/mcp` bilden den bestehenden Surface-Pfad.
  - `surfaces/mcp/tools.go` und die Coverage-Guards schützen den eingefrorenen Stable-12-Vertrag.
  - `docs/coverage-matrix.yaml` muss für neue CLI- und MCP-Surfaces aktualisiert werden.
- **Vorgesehene neue Module/Dateien:**
  - `internal/repostatus/` für wiederverwendbare read-only Freshness-Logik.
  - `engine/trust/model.go`
  - `engine/trust/schema.go`
  - `engine/trust/validate.go`
  - `engine/trust/collector.go`
  - `engine/trust/persist.go`
  - `engine/trust/policy.go`
  - `engine/trust/assessment.go`
  - `engine/trust/target.go`
  - `cmd/graphi/trust_report.go`
  - Erweiterungen in den bestehenden `surfaces/client/`, `surfaces/cli/` und `surfaces/mcp/`-Patterns.
  - Tests und Fixtures unter den jeweils bestehenden Package-Testpfaden; kein separates Testframework.
- **API-Verträge:**
  - Neues JSON-Schema beginnt mit `schema_version: 1`.
  - Persistenz-Key: `trust.snapshot.v1`.
  - MCP-Tool-Name: `graph_health`.
  - Policy-Namen: `exploratory-v1`, `review-v1`, `automated-change-v1`.
  - Verdicts: `PASS`, `WARN`, `FAIL`, `UNVERIFIED`.
  - Finding-Codes sind stabile Maschinenverträge und werden nicht nachträglich umbenannt.
  - Unbekannte Schema- oder Policy-Versionen liefern `UNVERIFIED`, niemals eine optimistische Rückfallbewertung.
  - CLI-Exit-Codes:
    - `0` = `PASS`
    - `1` = `WARN`
    - `2` = `FAIL` oder `UNVERIFIED`
    - CLI-Usage-/Parsingfehler folgen dem bereits bestehenden CLI-Konventionsmuster.
- **Persistenz und Atomicity:**
  - MVP verwendet vorhandene Metadaten-Persistenz und keine neue Tabelle.
  - Der Collector darf flüchtige Ingest-Signale während des Laufs erfassen.
  - Ein Snapshot wird erst nach erfolgreichem Abschluss der relevanten Graph-Generation als `complete` veröffentlicht.
  - Bei Crash, Generation-Mismatch oder Persistenzfehler existiert kein gültiger positiver Snapshot.
  - Snapshot-Persistenzfehler dürfen den Graph Store nicht beschädigen. Der Graph kann nutzbar bleiben, `graph_health` muss jedoch `UNVERIFIED` melden.
- **Performance:**
  - Keine vollständige Edge-Materialisierung in Memory nur für den Trust Report.
  - Aggregationen erfolgen über vorhandene Store-Queries oder während des Ingests.
  - Listen werden gekappt und aggregiert; Standardoutput enthält Counts, nicht alle Pfade oder Edges.
- **Privacy und Zero Egress:**
  - Keine Netzwerkzugriffe.
  - Keine Telemetrie.
  - Keine Source Bytes, Code-Snippets, Kommentare oder Prompt-Inhalte im Snapshot.
  - Pfade dürfen lokal angezeigt werden, müssen in persistierten detaillierten Listen aber normalisiert und begrenzt sein.
- **Verbote:**
  - Der Agent darf Stable-12-Toolnamen, Beschreibungen, Input-Schemas oder Output-Schemas nicht ändern.
  - Keine neuen externen Dependencies ohne explizite Freigabe.
  - Keine neue DB-Tabelle oder Migration ohne Architecture Decision Record und Maintainer-Freigabe.
  - Keine parallele zweite Freshness-Implementierung.
  - Keine numerische Trust-Gesamtnote.
  - Keine automatische Internetauflösung externer Packages.
  - Keine Änderung an Parser-, Linker- oder Type-Resolver-Semantik, sofern die aktuelle Phase ausschließlich Trust-Surface-Arbeit betrifft.
  - Keine Speicherung eines positiven Snapshot-Verdicts; gespeichert werden Fakten, Verdicts werden aktuell aus Policy plus Fakten berechnet.
  - Keine globale Repo-Refaktorierung im Rahmen von P1.

## 7. AI/Agenten-spezifische Anforderungen
- **Modellwahl & Begründung:** P1 verwendet kein LLM und benötigt weder Ollama noch eine externe Modell-API. Die Trust-Berechnung ist deterministisch und muss unabhängig vom verwendeten Agentenmodell sein. Jeder MCP-kompatible Agent kann `graph_health` konsumieren. Diese Entscheidung vermeidet API-Kosten, Halluzinationen, nicht reproduzierbare Bewertungen und Privacy-Risiken.
- **Verhalten bei Unsicherheit / Fallback-Strategie / Human-Handoff:**
  - Fehlender Snapshot, unbekannte Schema-Version, Generation-Mismatch, stale Graph oder nicht eindeutig auflösbares Target führen mindestens zu `UNVERIFIED`.
  - `automated-change-v1` ist fail-closed: Bei relevanter Heuristik, Ambiguity, Degradation, Boundary oder fehlender Evidenz wird nicht freigegeben.
  - Der Output muss eine konkrete nächste Aktion nennen, zum Beispiel `reindex`, `inspect_heuristic_edges`, `resolve_target`, `review_external_boundary` oder `human_review`.
  - Der Agent darf `UNVERIFIED` nicht in `WARN` oder `PASS` umdeuten.
  - Bei `FAIL` oder `UNVERIFIED` muss ein Coding-Agent die autonome Änderung abbrechen oder einen Menschen einbeziehen.
- **Akzeptable Fehlerrate / Halluzinationstoleranz:**
  - False-safe Verdicts: 0 akzeptabel.
  - Falsches `PASS` für einen stale oder generation-mismatched Snapshot: 0 akzeptabel.
  - Nicht-deterministische Finding-Reihenfolge: 0 akzeptabel.
  - Falsch-positive Warnings sind in Beta bis 5 % akzeptabel, sofern sie keine falsche Sicherheit erzeugen; sie müssen vor Stable-Promotion reduziert werden.
  - Target-Auflösung darf bei Ambiguity nicht raten.
- **Eval-Methodik:**
  - Golden-Set mit mindestens 60 Fällen: 20 pro Policy, inklusive gesunder, warnender, fehlschlagender und unverifizierbarer Zustände.
  - Mutation Tests für entfernte Snapshot-Felder, geänderte Generation, stale Commit, abgebrochenen Ingest, Parse-Skips, degradierte Units, heuristische Edges und External Boundaries.
  - Snapshot-Golden-Files für Human- und JSON-Ausgabe.
  - Property Tests: gleiche Facts + gleiche Policy = gleiches Verdict; zusätzliche negative Evidenz darf ein Verdict nicht verbessern.
  - Parity Tests: Direct Client, CLI und MCP müssen identische Fakten und Verdicts liefern.
  - Privacy Fixture mit Secrets, Prompt-Injection-Kommentaren, langen Dateinamen und binären Dateien.
  - Performance-Eval auf kleinem Fixture, realem Go-Repository und Stress-Repository.
  - Regressionsstrategie: Jeder gefundene Trust-Fehler erhält zuerst einen fehlschlagenden Test, danach den Fix.
- **Kosten- und Latenzbudget pro Request:**
  - Modell-/API-Kosten: 0.
  - Netzwerkrequests: 0.
  - Warm `graph_health` Repository-Scope p95 ≤ 75 ms.
  - Warm target-bezogenes Assessment p95 ≤ 150 ms bei Standard-Depth.
  - CLI `trust-report --json` p95 ≤ 100 ms.
  - Snapshot-Erzeugung ≤ 3 % Full-Index-p95-Overhead.
  - Persistierte Daten ≤ 1 MB pro Snapshot im MVP.
- **Datenanforderungen:**
  - Quelle ausschließlich lokaler Graph Store, lokaler Git-/Freshness-Zustand und Ingest-Diagnostics.
  - Aktualisierung nach erfolgreichem Full-Ingest; bei späterem Graph-Drift wird der Snapshot nicht automatisch als aktuell angenommen.
  - Keine Trainingsdaten und kein Feintuning.
  - Keine Remote-Datensätze.
  - Keine Source Bytes im Snapshot.
  - Snapshot-Fakten werden schema-versioniert und bei unbekannter Version fail-closed gelesen.
- **Prompt-Injection-Schutz:**
  - Repository-Inhalte werden nicht als Anweisung interpretiert.
  - Trust-Ausgaben enthalten keine Code-Kommentare oder beliebige Source-Texte.
  - Finding-Texte stammen ausschließlich aus intern kontrollierten Templates.
  - Nutzerkontrollierte Pfade oder Namen werden escaped, normalisiert und längenbegrenzt.

## 8. Phasenplan (für Agenten-Ausführung)

Jede Phase entspricht genau einer eigenständig reviewbaren PR-/Commit-Einheit mit maximal 1–3 Tagen Arbeit. Die Phasen sind sequenziell. Eine Phase darf erst beginnen, wenn der testbare Output der vorherigen Phase bestätigt ist. P1-Produktcode darf erst nach dokumentiertem P0-GO begonnen werden.

### Phase 0: Contract Freeze, Baseline und Test-Harness
- **Ziel:** Der P1-Vertrag, die aktuelle Code-Baseline, die Verantwortlichkeiten und die Teststruktur sind eindeutig, bevor Produktionslogik entsteht.
- **Abhängigkeiten:** Dokumentiertes P0-GO; ohne P0-GO nur Dokumentationsänderungen erlaubt.
- **Scope:**
  - P1-Owner und technische Reviewer im PRD beziehungsweise Plan festlegen.
  - Aktuellen `main`-SHA als Implementierungsbaseline dokumentieren.
  - Bestehende Stable-12-, Status-JSON-, Parity- und Coverage-Guards als Baseline ausführen.
  - Test-Fixures für gesund, stale, interrupted, heuristic-heavy, degraded und external-boundary definieren.
  - Golden-Set-Manifest mit erwarteter Policy-Klassifikation anlegen.
- **Out of Scope:** Produktionscode, Persistenz, CLI-Befehl, MCP-Tool, Policy-Implementierung.
- **Aufgaben:**
  1. Unter `docs/plan/` einen P1-Execution-Record mit Baseline-SHA, Owner, Reviewern und P0-GO-Referenz anlegen.
  2. Unter dem bestehenden Testdata-Pattern kleine deterministische Repository-Fixtures registrieren; keine neue Top-Level-Teststruktur erfinden.
  3. Golden-Manifest für mindestens 12 Startfälle anlegen: je Policy ein `PASS`, `WARN`, `FAIL` und `UNVERIFIED`.
  4. Bestehende Baseline-Kommandos für Build, Lint, Unit Tests, Stable Contract und Coverage Matrix dokumentieren.
  5. Einen CI-Test ergänzen, der sicherstellt, dass vor Phase 1 noch kein `graph_health` im Standardprofil sichtbar ist.
- **Schnittstellen:** Nur Test- und Dokumentationsartefakte; noch kein öffentlicher API-Vertrag.
- **Testbarer Output:** Bestehender Build und alle Baseline-Tests sind grün; Fixtures sind deterministisch reproduzierbar; P0-GO, Owner und Baseline-SHA sind dokumentiert.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given der aktuelle `main`-Stand, When die Baseline-Suite ausgeführt wird, Then bestehen alle bestehenden Tests unverändert.
  - Given MCP ohne Labs, When der Tool-Katalog gelesen wird, Then enthält er weiterhin exakt den eingefrorenen Stable-Tool-Satz.
  - Given das Golden-Manifest, When es validiert wird, Then besitzt jeder Fall Policy, erwartetes Verdict und erwartete Finding-Codes.
- **Rollback-Plan:** PR vollständig zurückrollen; da nur Dokumentation und Test-Fixtures betroffen sind, entstehen keine Runtime- oder Datenkompatibilitätsfolgen.
- **Risiken/Fallback:** Falls P0-GO, Owner oder Baseline nicht eindeutig vorliegen, Phase stoppen und Product Owner beziehungsweise Maintainer einbeziehen.

### Phase 1: Gemeinsame Freshness- und Repository-Status-Komponente
- **Ziel:** CLI, Trust Engine und MCP können dieselbe read-only Freshness-/Drift-Auswertung verwenden, ohne `package main` zu importieren oder Logik zu duplizieren.
- **Abhängigkeiten:** Phase 0.
- **Scope:**
  - Bestehende Statusberechnung aus `cmd/graphi/status.go` in ein gemeinsames internes Package extrahieren.
  - Bestehenden `graphi status`-Output und Exit-Code byte-semantisch unverändert halten.
  - Read-only API für Repository, Git, Graphpfad, Full-Pass-State, Drift, Current und Recommendation bereitstellen.
- **Out of Scope:** TrustSnapshot, Policies, neue Statusfelder, CLI-Designänderungen.
- **Aufgaben:**
  1. `internal/repostatus/model.go` mit dem bestehenden Status-Domainmodell anlegen.
  2. `internal/repostatus/read.go` implementieren und vorhandene Primitiven aus `internal/state`, `internal/gitinfo`, `internal/ingestlock` und Runtime-Metadaten wiederverwenden.
  3. `cmd/graphi/status.go` auf den neuen Reader umstellen; JSON-Feldnamen, Reihenfolge soweit vertraglich relevant, Semantik und Exit-Codes beibehalten.
  4. Parity-Test hinzufügen: alter Golden-Output gegen neue Implementierung auf allen Phase-0-Fixtures.
  5. Race- und read-only Tests ergänzen; der Reader darf keine Graph- oder Git-Metadaten verändern.
- **Schnittstellen:**
  ```go
  package repostatus

  type Report struct {
      Repository         string
      GitBranch          string
      GitCommit          string
      GraphPath          string
      NodeCount          int64
      Profile            string
      LastSync           time.Time
      FullPassInProgress bool
      LockHeld           bool
      Drift              bool
      Current            bool
      Recommendation     string
  }

  func Read(ctx context.Context, input Input) (Report, error)
  ```
- **Testbarer Output:** `graphi status` erzeugt für alle bestehenden Golden-Cases denselben semantischen Output und dieselben Exit-Codes wie vor der Extraktion.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given ein aktueller Graph, When `graphi status --json` ausgeführt wird, Then bleibt der bestehende JSON-Vertrag unverändert.
  - Given ein interrupted Full Pass, When der gemeinsame Reader aufgerufen wird, Then meldet er den Zustand read-only und ohne positive Current-Annahme.
  - Given MCP- oder Engine-Code, When der Reader importiert wird, Then ist kein Import aus `cmd/graphi` notwendig.
- **Rollback-Plan:** `cmd/graphi/status.go` auf die vorherige interne Implementierung zurückstellen und `internal/repostatus` entfernen; keine persistierten Daten betroffen.

### Phase 2: Trust-Domainmodell und kanonischer Schema-Vertrag
- **Ziel:** Alle Trust-Fakten, Findings, Policies und Verdicts besitzen einen validierten, deterministischen und versionierten internen Vertrag.
- **Abhängigkeiten:** Phase 1.
- **Scope:**
  - Neues `engine/trust`-Package.
  - Schema-Version 1.
  - Enums und Validierungsregeln.
  - Kanonische Sortierung und JSON-Serialisierung.
  - Noch keine echte Datensammlung oder Persistenz.
- **Out of Scope:** Ingest-Wiring, `trust-report`, MCP, Target-Traversal.
- **Aufgaben:**
  1. `engine/trust/model.go` mit `Snapshot`, `GenerationRef`, `Facts`, `Finding`, `Assessment` und Unterstrukturen implementieren.
  2. `engine/trust/schema.go` mit `SchemaVersion = 1`, stabilen Finding-Codes, Verdicts und Policy-Namen anlegen.
  3. `engine/trust/validate.go` implementieren: geschlossene Enums, keine negativen Counts, keine unbekannten Tiers, keine positive Assessment-Antwort bei fehlendem Snapshot.
  4. Deterministische Sortierung für Findings, Capabilities, Reasons und begrenzte Detail-Listen implementieren.
  5. Canonical-JSON-Encoder oder bestehendes kanonisches JSON-Pattern wiederverwenden.
  6. Unit-, Property- und Roundtrip-Tests hinzufügen.
- **Schnittstellen:**
  ```go
  const SchemaVersion = 1

  type Verdict string
  const (
      VerdictPass       Verdict = "PASS"
      VerdictWarn       Verdict = "WARN"
      VerdictFail       Verdict = "FAIL"
      VerdictUnverified Verdict = "UNVERIFIED"
  )

  type PolicyID string
  const (
      PolicyExploratoryV1    PolicyID = "exploratory-v1"
      PolicyReviewV1         PolicyID = "review-v1"
      PolicyAutomatedChangeV1 PolicyID = "automated-change-v1"
  )
  ```
- **Testbarer Output:** 100 wiederholte Serialisierungen desselben Models sind byte-identisch; unbekannte Enum- oder Schema-Werte werden abgelehnt beziehungsweise als `UNVERIFIED` behandelt.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given identische Facts in unterschiedlicher Input-Reihenfolge, When serialisiert wird, Then ist der JSON-Output byte-identisch.
  - Given ein Snapshot ohne GenerationRef, When validiert wird, Then schlägt die Validierung fehl.
  - Given ein unbekannter Policy-Identifier, When bewertet werden soll, Then entsteht kein `PASS`, sondern ein expliziter Fehler oder `UNVERIFIED`.
- **Rollback-Plan:** Neues Package entfernen; keine Consumer und keine persistierten Daten vorhanden.

### Phase 3: Ingest-Collector für flüchtige Trust-Signale
- **Ziel:** Alle Trust-relevanten Rohsignale werden innerhalb desselben Ingest-Laufs verlustfrei in ein validierbares `TrustFacts`-Objekt überführt.
- **Abhängigkeiten:** Phase 2.
- **Scope:**
  - Linker-Stats sammeln.
  - Type-Resolver-Degradation sammeln.
  - Parse-Skip-Diagnostics aggregieren.
  - Store-Level-Tier-Counts und External-Boundary-Counts erfassen.
  - Collector bleibt zunächst in-memory.
- **Out of Scope:** Persistenz, CLI, MCP, Policy-Auswertung.
- **Aufgaben:**
  1. `engine/trust/collector.go` mit einem expliziten Builder implementieren; keine globalen Variablen.
  2. `engine/ingest/linkfiles.go` beziehungsweise den tatsächlichen Linker-Call so erweitern, dass `link.Stats` an den Collector übergeben werden.
  3. `engine/ingest/typeresolve.go` so erweitern, dass Degraded Units, Type Errors, Skipped Files und Dropped Intents vor dem Verwerfen aggregiert werden.
  4. `engine/ingest/ingester.go`-Skip-Diagnostics source-frei nach Reason aggregieren.
  5. Vorhandene Store-Level-Tier-Counts aus `core/graphstore` wiederverwenden; keine komplette Edge-Liste laden.
  6. External-Boundary-Aggregation über Store-Join oder collect-time Count implementieren; konkrete Source-Inhalte nicht speichern.
  7. Collector-Ergebnis validieren und in Tests gegen kontrollierte Fixture-Graphen prüfen.
- **Schnittstellen:**
  ```go
  type Collector interface {
      ObserveLinkStats(link.Stats)
      ObserveTypeResolve(typeresolve.Result)
      ObserveSkips([]ingest.SkipDiagnostic)
      Finalize(ctx context.Context, store graphstore.Reader) (trust.Facts, error)
  }
  ```
  Die konkrete Package-Grenze darf angepasst werden, um Import-Zyklen zu vermeiden; das Domainmodell bleibt in `engine/trust`.
- **Testbarer Output:** Für alle Phase-0-Fixtures stimmen erwartete Edge-Tiers, Skip-Reasons, Degradation und Boundary-Counts exakt.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given ein Fixture mit confirmed, derived und heuristic Edges, When der Collector finalisiert, Then stimmen alle drei Counts.
  - Given ein Typecheck-degradiertes Package, When der Ingest endet, Then enthält `TrustFacts` die Degradation auch dann, wenn die bisherigen transienten Result-Objekte verworfen werden.
  - Given eine Oversize-Datei, When sie fail-closed übersprungen wird, Then enthält der Collector Reason und Count, aber keine Source Bytes.
  - Given externe Ziel-Nodes, When Boundaries aggregiert werden, Then werden lokale eingehende Edges gezählt, ohne externe Navigierbarkeit zu behaupten.
- **Rollback-Plan:** Collector-Wiring entfernen; Ingest-Verhalten und Graphpersistenz bleiben unverändert.

### Phase 4: Generation-bound Snapshot-Persistenz
- **Ziel:** Nach einem erfolgreichen Full Pass existiert genau ein validierter Snapshot, der eindeutig zur veröffentlichten Graph-Generation gehört.
- **Abhängigkeiten:** Phase 3.
- **Scope:**
  - `trust.snapshot.v1` Persistenz.
  - Generation-Bindung.
  - Crash-/Interrupted-Verhalten.
  - Read API.
- **Out of Scope:** Policy-Engine, CLI, MCP, Incremental-Optimierung.
- **Aufgaben:**
  1. `engine/trust/persist.go` mit `WriteSnapshot`, `ReadSnapshot` und `DeleteOrInvalidateSnapshot` implementieren.
  2. Vorhandene `kv_meta`- beziehungsweise schema-versionierte Persistenzmuster wiederverwenden; keine neue Tabelle.
  3. Snapshot erst nach erfolgreichem Full-Pass-Abschluss als `complete` veröffentlichen.
  4. Bei begonnenem, aber nicht abgeschlossenem Full Pass darf kein neuer Snapshot gültig erscheinen.
  5. GenerationRef mit `full_pass_generation`, Repository-Identität, Git-Commit soweit verfügbar und Erstellungszeitpunkt befüllen.
  6. Reader prüft Snapshot-Schema, Generation-Match und Graph-Freshness unabhängig.
  7. Persistenzfehler fail-closed behandeln: Index darf nicht korrupt werden; Snapshot-Status wird `unavailable`.
  8. Crash-, Corruption-, Unknown-Schema- und Generation-Mismatch-Tests ergänzen.
- **Schnittstellen:**
  ```go
  const SnapshotMetaKey = "trust.snapshot.v1"

  func WriteSnapshot(ctx context.Context, store MetaStore, snapshot trust.Snapshot) error
  func ReadSnapshot(ctx context.Context, store MetaStore) (trust.Snapshot, trust.SnapshotState, error)
  ```
- **Testbarer Output:** Nach erfolgreichem Full Pass kann ein neuer Prozess den byte-identischen Snapshot lesen; nach Crash oder Generation-Mismatch entsteht niemals ein gültiger `complete`-Status.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given ein erfolgreich abgeschlossener Full Pass, When der Prozess neu startet, Then ist der Snapshot lesbar und generation-bound.
  - Given ein Crash vor Snapshot-Publikation, When `ReadSnapshot` aufgerufen wird, Then lautet der Zustand `unavailable` oder `incomplete`, niemals `complete`.
  - Given ein Snapshot einer alten Generation, When der Graph eine andere Generation besitzt, Then wird `generation_mismatch` gemeldet.
  - Given eine unbekannte Schema-Version, When gelesen wird, Then lautet das Ergebnis `UNVERIFIED` und es erfolgt kein stilles Downgrade.
- **Rollback-Plan:** Snapshot-Schreibpfad deaktivieren beziehungsweise revertieren. Der versionierte `kv_meta`-Eintrag bleibt harmlos und wird von älteren Versionen ignoriert; keine Down-Migration erforderlich.

### Phase 5: Policy-Engine für Repository-Scope
- **Ziel:** Die drei festen Policies erzeugen aus denselben Fakten nachvollziehbare und fail-closed Verdicts.
- **Abhängigkeiten:** Phase 4.
- **Scope:**
  - Policy-Regeln.
  - Stabile Finding-Codes.
  - Repository-Scope.
  - Next-action Recommendations.
- **Out of Scope:** Target-Traversal, Strict Query, neue Surfaces.
- **Aufgaben:**
  1. `engine/trust/policy.go` mit versionierten Policy-Definitionen implementieren.
  2. `engine/trust/assessment.go` als pure Funktion ohne I/O implementieren.
  3. Finding-Codes und Severity-Mapping dokumentieren.
  4. Monotonie-Regel testen: Zusätzliche negative Evidenz darf ein Verdict nicht verbessern.
  5. Mindestens 60 Golden-Cases erstellen beziehungsweise das Phase-0-Manifest erweitern.
  6. Empfehlungen deterministisch aus Findings ableiten.
- **Policy-Mindestregeln:**
  - `exploratory-v1`: Fresh Snapshot erforderlich. Heuristik und Boundaries sind erlaubt, aber sichtbar. Stale oder fehlende Generation führt zu `UNVERIFIED`.
  - `review-v1`: Fresh Snapshot erforderlich. Ambiguous, Skips, Degradation oder hoher relevanter Heuristikanteil führen zu `WARN` oder `FAIL` gemäß dokumentierter Schwellen.
  - `automated-change-v1`: Fresh Snapshot und vollständige relevante Evidenz erforderlich. Jede relevante Ambiguity, Skip, Degradation, unbekannte Coverage oder heuristic-only Beziehung führt zu `FAIL`; fehlender Snapshot führt zu `UNVERIFIED`.
- **Schnittstellen:**
  ```go
  func AssessRepository(facts trust.Facts, freshness repostatus.Report, policy trust.PolicyID) trust.Assessment
  ```
- **Testbarer Output:** Alle 60 Golden-Cases liefern exakt die erwarteten Verdicts, Finding-Codes und Recommendations.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given ein stale Graph, When jede Policy bewertet wird, Then entsteht kein `PASS`.
  - Given ein frischer Go-Graph ohne Skips, Ambiguity oder Degradation, When `automated-change-v1` bewertet wird, Then ist `PASS` möglich.
  - Given heuristic Edges, When `exploratory-v1` bewertet wird, Then kann `WARN` entstehen; When `automated-change-v1` bewertet wird, Then entsteht `FAIL`.
  - Given fehlender Snapshot, When irgendeine Policy bewertet wird, Then lautet das Verdict `UNVERIFIED`.
- **Rollback-Plan:** Policy-Package entfernen; Snapshot-Persistenz bleibt als unbewertete Faktenbasis bestehen.

### Phase 6: CLI `graphi trust-report`
- **Ziel:** Entwickler erhalten eine verständliche und JSON-fähige Trust-Ausgabe aus demselben Domainmodell.
- **Abhängigkeiten:** Phase 5.
- **Scope:**
  - Neuer read-only CLI-Befehl.
  - Human- und JSON-Ausgabe.
  - Policy-Auswahl.
  - Stabile Exit-Codes.
- **Out of Scope:** Target-Scope, Strict Query, MCP.
- **Aufgaben:**
  1. `cmd/graphi/trust_report.go` nach bestehendem Command-Pattern implementieren.
  2. Flags `--json` und `--policy exploratory-v1|review-v1|automated-change-v1` hinzufügen.
  3. Human-Renderer implementieren, der Facts, Verdict, Findings, Limits und Recommendations klar trennt.
  4. JSON direkt aus dem kanonischen DTO erzeugen; keine zweite Schema-Struktur.
  5. Exit-Codes 0/1/2 gemäß Abschnitt 6 implementieren.
  6. `docs/coverage-matrix.yaml` und CLI-Coverage-Guards aktualisieren.
  7. Snapshot-, Usage-, Missing-Graph- und Corruption-Tests ergänzen.
- **Schnittstellen:**
  ```bash
  graphi trust-report
  graphi trust-report --json
  graphi trust-report --policy review-v1
  ```
- **Testbarer Output:** Human- und JSON-Golden-Tests bestehen; CLI p95 bleibt innerhalb des Budgets; bestehende Commands bleiben unverändert.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given ein gesunder Snapshot, When `graphi trust-report --policy review-v1` ausgeführt wird, Then zeigt die Ausgabe Facts, `PASS` oder `WARN`, Finding-Codes und Recommendation.
  - Given ein fehlender Snapshot, When der Command ausgeführt wird, Then ist das Verdict `UNVERIFIED` und der Exit-Code 2.
  - Given `--json`, When derselbe Zustand zweimal abgefragt wird, Then ist der Output byte-identisch.
  - Given ein unbekannter Policy-Name, When der Command aufgerufen wird, Then entsteht ein Usage-Fehler und keine implizite Default-Policy.
- **Rollback-Plan:** CLI-Dispatch und Coverage-Matrix-Eintrag zurückrollen; Snapshot-Daten bleiben intern verfügbar.

### Phase 7: MCP-Labs-Tool `graph_health`
- **Ziel:** MCP-Agenten können denselben Repository-Trust wie die CLI maschinenlesbar und read-only abrufen.
- **Abhängigkeiten:** Phase 6.
- **Scope:**
  - Client-Port.
  - Direct Client.
  - MCP-Descriptor und Handler.
  - Labs-Gating.
  - Repository-Scope.
- **Out of Scope:** Stable-Promotion, Target-Scope, Strict Query.
- **Aufgaben:**
  1. Bestehende Client-Schnittstelle in `surfaces/client/` um eine Trust-Operation erweitern; keinen parallelen Client anlegen.
  2. Direct-Implementierung auf den gemeinsamen Trust-Service verdrahten.
  3. `graph_health` in `surfaces/mcp/tools.go` als Labs-Tool registrieren.
  4. Handler in bestehendem `toolcalls`-Pattern implementieren.
  5. Input-Schema mit verpflichtender oder eindeutig defaultbarer Policy definieren; Default für MCP ist `review-v1`.
  6. Output ist das kanonische JSON aus Phase 2/6.
  7. `WithLabs()`-, Capability- und Dispatch-Guards testen.
  8. `docs/coverage-matrix.yaml` aktualisieren; Stable-12-Guard muss unverändert grün bleiben.
- **Schnittstellen:**
  ```json
  {
    "name": "graph_health",
    "arguments": {
      "policy": "review-v1"
    }
  }
  ```
- **Testbarer Output:** Ohne Labs ist `graph_health` unsichtbar und nicht aufrufbar; mit Labs liefert es denselben semantischen Output wie `graphi trust-report --json`.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given MCP ohne Labs, When `tools/list` aufgerufen wird, Then erscheint `graph_health` nicht.
  - Given MCP mit Labs, When `graph_health` aufgerufen wird, Then entspricht der Output dem Direct-Client-Ergebnis.
  - Given ein stale oder fehlender Snapshot, When der Agent fragt, Then liefert das Tool `UNVERIFIED` und konkrete Recommendations.
  - Given ein unbekanntes Argument, When der Handler aufgerufen wird, Then greift die bestehende MCP-Schema-Validierung.
- **Rollback-Plan:** Tool-Registrierung und Handler entfernen. Durch das bestehende Labs-Gating kann das Tool sofort deaktiviert werden, ohne Stable-Verträge zu berühren.

### Phase 8: Target-bezogenes Trust-Assessment
- **Ziel:** Ein Agent kann die Trust-Eignung für ein konkretes Symbol und eine konkrete Query-Art prüfen, statt nur globale Repository-Werte zu erhalten.
- **Abhängigkeiten:** Phase 7.
- **Scope:**
  - Eindeutige Target-Auflösung.
  - Relevanter Graph-Ausschnitt.
  - Tier-Tally, Boundaries und withheld/unknown Counts im Scope.
  - Erweiterung von CLI und MCP um optionales Target.
- **Out of Scope:** Automatische Codeänderung, beliebige Query-Sprache, Cross-Repository-Targets.
- **Aufgaben:**
  1. `engine/trust/target.go` implementieren und vorhandene Symbolauflösung sowie result-scoped Tier-Tally wiederverwenden.
  2. Eingabe auf Symbol-ID oder eindeutig auflösbaren Symbolnamen begrenzen.
  3. `operation` auf eine geschlossene Startmenge begrenzen: `callers`, `callees`, `references`, `impact`, `neighborhood`.
  4. Scope-Fakten erheben: traversierte Edges pro Tier, External Boundaries, Ambiguity, withheld Results und relevante Degradation.
  5. Bei null, mehreren oder externen Targets nicht raten; Finding und `UNVERIFIED` erzeugen.
  6. CLI um `--target`, `--operation` und begrenzte `--depth` erweitern.
  7. MCP-Input-Schema um dieselben optionalen Felder erweitern, ohne bestehende Calls zu brechen.
  8. Golden- und Performance-Tests ergänzen.
- **Schnittstellen:**
  ```bash
  graphi trust-report \
    --policy automated-change-v1 \
    --target MyService \
    --operation impact \
    --depth 2
  ```
  ```json
  {
    "policy": "automated-change-v1",
    "target": "MyService",
    "operation": "impact",
    "depth": 2
  }
  ```
- **Testbarer Output:** Für eindeutige Targets liefert CLI/MCP einen Scope-Bericht; bei Ambiguity oder unbekanntem Target erfolgt korrekte Abstention.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given ein eindeutig bestätigtes Go-Symbol, When ein target-bezogenes Review-Assessment ausgeführt wird, Then enthält der Output Tier-Tally und Scope-Boundaries.
  - Given zwei Symbole mit gleichem Namen, When ohne Symbol-ID abgefragt wird, Then entsteht `TARGET_AMBIGUOUS` und `UNVERIFIED`.
  - Given ein Pfad, der eine externe Boundary erreicht, When `automated-change-v1` bewertet wird, Then wird die Boundary sichtbar und blockiert je Policy.
  - Given ein Scope ohne Resultate, aber mit withheld heuristic Results, When bewertet wird, Then wird nicht „keine Auswirkungen" behauptet.
- **Rollback-Plan:** Optionale Target-Felder und Target-Service entfernen; Repository-Scope aus Phase 7 bleibt kompatibel.

### Phase 9: Strict Query Labs-Prototyp
- **Ziel:** Agenten können Trust-aware Queries ausführen, die unterhalb eines Mindest-Tiers liegende Ergebnisse zurückhalten und die Zurückhaltung explizit melden.
- **Abhängigkeiten:** Phase 8.
- **Scope:**
  - Neues Labs-Tool beziehungsweise Labs-CLI-Surface.
  - Mindest-Tier `confirmed|derived|heuristic`.
  - Withheld Counts.
  - Geschlossene Operationen analog Phase 8.
- **Out of Scope:** Änderung der Stable Query-Schemas, automatische Refactorings, frei formulierbare Query Language.
- **Aufgaben:**
  1. Trust-aware Query-Service als Wrapper um bestehende Query-Operationen implementieren.
  2. Vorhandenes `WithheldByConfidence`-Pattern wiederverwenden.
  3. Neues Labs-MCP-Tool `strict_query` oder den im Repository üblichen äquivalenten Namen registrieren; endgültigen Namen vor Implementierung im Contract Freeze festlegen.
  4. CLI-Labs-Surface nach bestehendem Pattern ergänzen.
  5. Output muss `returned_count`, `withheld_count`, `minimum_tier`, `result_tier_counts` und Trust-Findings enthalten.
  6. Ein vollständig weggefiltertes Resultat darf nie als bewiesen leer erscheinen.
  7. Stable-12- und bestehende Query-Contracts unverändert lassen.
- **Schnittstellen:**
  ```json
  {
    "operation": "callers",
    "target": "MyService",
    "minimum_tier": "derived",
    "depth": 1
  }
  ```
- **Testbarer Output:** Queries filtern korrekt, melden withheld Results und verändern keine Stable-Operation.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given drei confirmed und zwei heuristic Caller, When `minimum_tier=derived`, Then werden drei Ergebnisse geliefert und zwei als withheld gemeldet.
  - Given ausschließlich heuristic Results, When `minimum_tier=confirmed`, Then ist die Ergebnisliste leer, aber `withheld_count > 0` und das Assessment nicht irreführend positiv.
  - Given Stable MCP ohne Labs, When der Katalog gelesen wird, Then bleibt das neue Tool unsichtbar.
- **Rollback-Plan:** Labs-Registrierung und Wrapper entfernen; keine Stable-Schema- oder Persistenzänderung.

### Phase 10: Härtung, Dokumentation und Beta-Rollout
- **Ziel:** P1 erfüllt Performance-, Privacy-, Determinismus-, Security- und Nutzerverständnis-Gates und kann kontrolliert als Beta veröffentlicht werden.
- **Abhängigkeiten:** Phase 9.
- **Scope:**
  - Finale Eval-Suite.
  - Performance und Storage.
  - Privacy-/Security-Review.
  - Dokumentation.
  - Rollout-Artefakte.
- **Out of Scope:** Neue Features, Sprachverbesserungen, Stable-Promotion von Labs-Tools.
- **Aufgaben:**
  1. Golden-Set und Mutation-Suite vollständig gegen die finale Version ausführen.
  2. A/B-Performance-Benchmark mit und ohne Trust Collector durchführen.
  3. Privacy-Fixture und Zero-Egress-Audit ausführen.
  4. Crash-, Corruption- und Generation-Mismatch-Tests auf unterstützten Plattformen ausführen.
  5. `docs/trust-surface.md`, CLI-Referenz, MCP-Referenz, Finding-Code-Katalog und Policy-Dokumentation erstellen.
  6. Capability Matrix dokumentieren und mit automatisiertem Drift-Test an die tatsächlichen Language Capabilities binden.
  7. Beta-Release Notes mit bekannten Limits veröffentlichen.
  8. Moderierten Nutzerverständnistest durchführen und Findings dokumentieren.
- **Schnittstellen:** Keine neuen Verträge; nur Härtung der in den vorherigen Phasen definierten Surfaces.
- **Testbarer Output:** Alle Zielwerte aus Abschnitt 4 sind erreicht; keine kritischen Security-/Privacy-Findings; Stable-12 unverändert; Beta-Artefakte vollständig.
- **Akzeptanzkriterien (Given/When/Then):**
  - Given die finale P1-Version, When die vollständige Eval-Suite läuft, Then gibt es kein False-safe Verdict.
  - Given ein Repository mit eingebetteten Secrets und Prompt-Injection-Kommentaren, When Trust-Surfaces ausgeführt werden, Then erscheint kein Source- oder Secret-Inhalt im Output.
  - Given die Referenz-Repositories, When A/B-Benchmarks laufen, Then bleibt der Full-Index-p95-Overhead ≤ 3 %.
  - Given ein MCP-Client ohne Labs, When aktualisiert wird, Then bleibt sein Stable-Vertrag unverändert.
- **Rollback-Plan:** Siehe Abschnitt 10. Labs-Tools können aus dem Katalog entfernt werden; Snapshot-Schreiben kann deaktiviert/revertiert werden; versionierte Metadaten werden von älteren Versionen ignoriert.

**Hinweis für den Agenten:** Phasen sind sequenziell abzuarbeiten. Keine Phase beginnen, deren Abhängigkeiten nicht als testbarer Output bestätigt sind. Bei Unklarheiten innerhalb einer Phase: Rückfrage stellen statt Scope zu erweitern. Jede Phase muss einen eigenen PR mit klarer Beschreibung, Tests, Messwerten und explizitem Hinweis auf nicht geänderte Stable-Verträge erzeugen.

## 9. Risiken & Sicherheit

| Risiko | Impact | Mitigation |
|---|---|---|
| Prompt Injection über Source-Code, Kommentare oder Dateinamen | Hoch — Agent könnte Repository-Inhalte als Anweisung interpretieren | Trust-Surface verarbeitet keine Source-Texte; Finding-Texte stammen aus kontrollierten Templates; Pfade escapen und begrenzen; Privacy-Fixture in CI |
| Halluzination beziehungsweise False-safe Verdict | Kritisch — Agent führt riskante Änderung auf unzureichender Evidenz aus | Kein LLM in Trust-Berechnung; fail-closed Policies; 0 tolerierte False-safe Fälle; Mutation- und Property-Tests |
| Biased Output zugunsten einer Sprache | Mittel — Preview-Sprachen wirken fälschlich gleichwertig oder schlechter als messbar belegt | Capability-Level explizit ausgeben; keine sprachübergreifende Einzelpunktzahl; Fakten statt Marketingklassifikation |
| Stale Snapshot | Kritisch — alte Fakten werden auf neuen Code angewandt | GenerationRef plus gemeinsame Freshness-Prüfung; stale führt nie zu `PASS` |
| Snapshot-/Graph-Generation-Mismatch | Kritisch | Generation-bound Validation; unbekannter oder anderer Generation-Identifier führt zu `UNVERIFIED` |
| Partial oder abgebrochener Ingest | Hoch | Full-Pass-Marker prüfen; Snapshot erst nach erfolgreichem Abschluss publizieren |
| Verlust flüchtiger Ingest-Signale | Hoch | Collector in denselben Ingest-Prozess integrieren, bevor Result-Objekte verworfen werden |
| Snapshot-Persistenzfehler beschädigt Index | Hoch | Metadaten separat und fail-safe schreiben; Index bleibt nutzbar, Trust wird `UNVERIFIED` |
| Performance-Regressions durch Store-Aggregationen | Hoch | Vorhandene Aggregationen wiederverwenden; keine vollständige Edge-Materialisierung; A/B-Benchmarks und 3-%-Gate |
| Snapshot wächst durch Detail-Listen unbeschränkt | Mittel | Counts standardmäßig; Listen deterministisch kappen; 1-MB-Budget |
| Externe Boundary wird als analysierte Dependency missverstanden | Hoch | Explizites Finding und Capability-Limit; keine Aussagen über interne externe Datenflüsse |
| Gefilterte leere Query wird als „keine Treffer" interpretiert | Hoch | `withheld_count` verpflichtend; Policy-Finding bei zurückgehaltenen Ergebnissen |
| Breaking Change am Stable-12-Vertrag | Kritisch | Neue MCP-Tools ausschließlich Labs; Coverage- und Stable-Contract-Guards |
| Doppelte Freshness-Logik driftet auseinander | Hoch | Phase 1 extrahiert eine gemeinsame read-only Implementierung; keine Kopie erlaubt |
| Unbekannte Schema-Version wird optimistisch gelesen | Kritisch | Unknown schema = `UNVERIFIED`; kein automatisches Downgrade |
| Nicht-deterministische Ausgabe erschwert Audits | Mittel | Kanonische Sortierung, Golden-Files, 100-fache Serialisierungstests |
| Finding-Texte leaken lokale absolute Pfade | Mittel | Pfade normalisieren; optional repository-relative Darstellung; Detail-Listen begrenzen |
| Policy-Schwellen werden ohne Evidenz festgelegt | Mittel | Versionierte Policies, Golden-Set, dokumentierte Änderung nur über neue Policy-Version |
| Labs-Feature wird als GA wahrgenommen | Mittel | `[labs]`-Kennzeichnung, Opt-in-Katalog, Dokumentation, keine Stable-Promotion in P1 |

## 10. Rollout & Rollback
- **Phasiertes Rollout:**
  1. **Internal-only:** Phasen 1–5 ohne öffentliche Surface. Snapshot und Policy werden nur durch Tests und interne Debug-Harnesses validiert.
  2. **CLI Beta:** `graphi trust-report` wird als Beta/read-only veröffentlicht. Keine Stable-Garantie für Finding-Katalog außerhalb Schema-Version 1.
  3. **MCP Labs:** `graph_health` wird nur mit bestehendem Labs-Opt-in registriert.
  4. **Design-Partner Beta:** Mindestens drei reale Repositories und zwei unterschiedliche Agenten-Clients nutzen P1; Metriken und Fehlklassifikationen werden gesammelt.
  5. **Default Snapshot Collection:** Erst nach Performance-, Privacy- und Crash-Gates wird Snapshot-Erzeugung standardmäßig aktiv.
  6. **Stable-Entscheidung:** Nicht Teil von P1. Eine spätere Entscheidung benötigt mindestens zwei Releases ohne False-safe Incident, belastbare Nutzungsevidenz und einen separaten Contract Review.
- **Kill-Switch:**
  - `graph_health` und Strict Query können durch Entfernen beziehungsweise Deaktivieren der Labs-Registrierung sofort aus dem MCP-Katalog genommen werden.
  - Snapshot-Schreiben kann über einen internen, explizit dokumentierten Ingest-Schalter deaktiviert werden, solange die Beta läuft; bei Deaktivierung muss die Surface `UNVERIFIED` melden.
  - CLI darf bei deaktivierter Snapshot-Erzeugung keinen synthetischen gesunden Zustand erzeugen.
- **Rollback-Trigger:**
  - Ein False-safe `PASS`.
  - Snapshot wird trotz Generation-Mismatch als gültig akzeptiert.
  - Source- oder Secret-Leakage.
  - Stable-12-Vertrag verändert.
  - Full-Index-p95-Regressionswert > 3 % nach bestätigter Reproduktion.
  - Graph Store wird durch Snapshot-Persistenz beschädigt.
  - Nicht-deterministische JSON-Ausgabe auf identischem Input.
  - Kritisches Security-Finding.
- **Rollback-Vorgehen:**
  1. Labs-Tool-Registrierung deaktivieren.
  2. CLI-Command bei Bedarf hinter Beta-Gate zurückziehen oder auf `UNVERIFIED` begrenzen.
  3. Snapshot-Writer deaktivieren/revertieren.
  4. `trust.snapshot.v1` nicht migrieren oder löschen; ältere Versionen ignorieren den Key.
  5. Stable-Queries und bestehende Graph-Daten bleiben unverändert.
  6. Incident als Golden-/Mutation-Test reproduzieren, bevor ein erneuter Rollout erfolgt.

## 11. AI-Anweisungen (für Coding-Agents wie Claude Code / Devin)
- Phasen strikt sequenziell umsetzen; keine Phase beginnen, bevor der testbare Output der vorherigen Phase grün bestätigt wurde.
- Pro Phase genau einen fokussierten PR erstellen; keine Sammel-PRs über mehrere Phasen.
- Keine Dateien außerhalb des aktuellen Phasenscopes ändern.
- Keine neuen Dependencies ohne explizite Freigabe installieren.
- Keine Dateien außerhalb der angegebenen beziehungsweise bereits etablierten Verzeichnisse anlegen.
- Keine Stable-12-MCP-Toolnamen, Schemas, Beschreibungen oder Semantik ändern.
- Keine neue Freshness-Implementierung kopieren; die gemeinsame Komponente aus Phase 1 wiederverwenden.
- Keine DB-Tabellen oder Migrationen ohne explizite Architekturfreigabe.
- Keine Source Bytes, Kommentare, Code-Snippets, Prompts oder Secrets in Trust-Artefakte schreiben.
- Keine Netzwerkkommunikation, Telemetrie oder Remote-API einführen.
- Keine numerische Trust-Gesamtnote implementieren.
- Keine unbekannten Targets, Tiers, Schema-Versionen oder Policies erraten.
- Fehlende Evidenz immer fail-closed behandeln.
- Vor jeder Produktionsänderung zuerst einen fehlschlagenden Test beziehungsweise ein Golden-Case hinzufügen.
- Bestehende Patterns für Client, CLI, MCP, Persistenz, Coverage Matrix und Labs-Gating wiederverwenden.
- Keine Parser-, Linker- oder Type-Resolver-Semantik „nebenbei" verbessern.
- Keine Repository-weiten Renames oder Formatierungsänderungen.
- Alle neuen öffentlichen Finding-Codes und JSON-Felder dokumentieren.
- Deterministische Sortierung für jede Liste und Map sicherstellen.
- Performancebudgets mit gemessenen Benchmarks belegen; keine ungemessenen Claims.
- In jeder PR-Beschreibung angeben:
  - Phase und Ziel
  - geänderte Dateien
  - bewusst nicht geänderte Bereiche
  - Tests und Benchmark-Ergebnisse
  - Rollback-Möglichkeit
  - Auswirkung auf Stable-12: muss „keine" sein
- Bei Unklarheit: Rückfrage stellen statt Annahme treffen oder Scope zu erweitern.
- Wenn ein Import-Zyklus droht, Architekturproblem transparent melden; nicht durch Kopieren von Domain-Typen umgehen.
- Wenn ein Phase-Gate nicht erreicht wird, Arbeit stoppen und den exakten fehlgeschlagenen Test beziehungsweise Messwert dokumentieren.

## 12. Offene Fragen
- [ ] Wer ist der verbindliche P1-Owner und wer erteilt die technische Freigabe?
- [ ] Welches konkrete Dokument gilt als P0-GO und damit als Startfreigabe für P1-Produktcode?
- [ ] Soll `graphi trust-report` während der ersten Beta frei sichtbar oder ebenfalls Labs-gated sein? Default dieses PRD: CLI Beta sichtbar, MCP Labs-gated.
- [ ] Soll Snapshot-Erzeugung zunächst nur nach Full Pass erfolgen oder bereits in P1 auch nach jedem erfolgreichen Incremental Pass? Default dieses PRD: Full Pass; Incremental Drift führt fail-closed zu einem nicht positiven Assessment.
- [ ] Welche maximalen Detail-Listen werden im Snapshot gespeichert? Default: nur Counts plus höchstens 20 normalisierte Beispiele je Finding-Kategorie.
- [ ] Welche Heuristik-Schwelle löst bei `review-v1` `WARN` gegenüber `FAIL` aus? Vor Phase 5 anhand des Golden-Sets festlegen und als Policy-Version einfrieren.
- [ ] Soll target-bezogenes Assessment Symbol-IDs verpflichtend machen, sobald eine Namensauflösung mehrdeutig ist? Default: Ja.
- [ ] Wie heißt das Strict-Query-Labs-Tool final (`strict_query`, `trust_query` oder bestehendes Naming-Pattern)? Entscheidung im Contract Review vor Phase 9.
- [ ] Welche Bedingungen sind für eine spätere Stable-Promotion von `graph_health` notwendig? Nicht Teil der P1-Implementierung; separates PRD erforderlich.
