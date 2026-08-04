<!-- REGISTRATION HEADER (2026-08-03) — added by this repository around the supplied
     document. Nothing in this header is part of the supplied text. The supplied text
     starts at the marker line that closes this header and runs unaltered to the end
     of the file. -->

# Registration record — P1 Trust Surface PRD

**This section was added by the repository (2026-08-03). It is not part of the supplied
document.** The supplied text begins at the marker line below and runs to the end of the
file.

## Provenance — SUPPLIED IN THE TASK MESSAGE, transcribed; weaker than a file copy

The document below was supplied by the product owner (`samibel`) **inline in a task
message** on 2026-08-03 and transcribed into this file. This is a **weaker provenance
class** than the P0 Delta PRD's registration, where the owner supplied a file that was
copied in unchanged (`cp`) and could be hashed against the original. Here no independent
original file exists to compare against: the hash below is the hash **of the transcription
as registered**, computed at registration time — it proves this file has not changed since
registration, not that the transcription is byte-identical to the owner's source document.
Recording the substitution instead of claiming the stronger fact follows the parent PRD's
solo-substitute rule (§8.8): a substitute is never recorded as if the original requirement
had been met.

Two transcription facts a later reader needs:

- The message introduced the document with the prose line *„Es geht um PRD — Graphi P1:
  Trust Surface"*; the document title was part of that prose, not of the document body.
  The registered body therefore begins at its own first line, `Dokumentstatus: Draft`,
  and carries **no top-level title heading**. The title is supplied by this header only.
- **Nothing in the body was corrected.** Wording, typography, section numbering and every
  claim are preserved as supplied — including the header field "Owner: noch festzulegen"
  and the design baseline it names. Where the repository disagrees with the document, the
  disagreement is recorded in the companion audit, never by editing the body.

**Registration hash.** The registered body is 72 197 bytes, 2 901 lines,
`sha256:58cce857b2657def19876bc0090f443ef74e5cb6383da2b2853b0347922bec8f`.
Re-verify that the body is unchanged since registration:

```bash
sed -n '/^<!-- BEGIN SUPPLIED DOCUMENT/,$p' docs/plan/2026-07-graphi-p1-trust-surface-prd.md \
  | tail -n +2 | shasum -a 256
# → 58cce857b2657def19876bc0090f443ef74e5cb6383da2b2853b0347922bec8f
```

## Registration facts

| Field | Value |
|---|---|
| Registered path | `docs/plan/2026-07-graphi-p1-trust-surface-prd.md` — the path the document itself names ("Zieldokumentpfad im Repository") |
| Registered on | 2026-08-03 |
| Provenance | Supplied inline in the product owner's task message, transcribed (see above) |
| Snapshot date of the supplied text | 2026-07-28 (as stated in the document's own header, "Letzte Aktualisierung") |
| Document status | **Draft** (the document's own header). Registration records the text; it is **not** the WP1.0 contract freeze, not a PRD approval, and not a P1 start decision. |
| Owner | "noch festzulegen" per the document itself — **still unassigned at registration** |
| Design baseline named by the document | `afa1b686de381dd455ab08e4bf33aaf9420d6aab` — exists in this repository's history (merge of PR #81, SW-130 baseline run) |
| Companion source audit | [`docs/plan/2026-08-graphi-p1-code-baseline-audit.md`](2026-08-graphi-p1-code-baseline-audit.md) — verifies the document's §3 "Source-derived Baseline" claims against the code and records what of P1 exists in code (nothing) |

## Where this document sits in the authority chain

- The document's own header governs: it precisifies P1, replaces neither the P0 PRD nor
  the 9/10 master plan, and yields to higher-ranked planning on conflict.
- **Its stated precondition is not met.** The document requires a documented P0 GO
  ("Voraussetzung: P0 „Proof and Truth" hat ein dokumentiertes GO erhalten"). Per
  [`docs/plan/2026-07-graphi-p0-completion-checklist.md`](2026-07-graphi-p0-completion-checklist.md),
  17 of 18 P0 stories are open and no P0 Go/No-Go has been held. Registering this text
  does not start P1; starting P1 before P0 GO would be a deliberate, owner-decided
  deviation and is not decided here.

<!-- BEGIN SUPPLIED DOCUMENT — transcribed from the owner's task message of 2026-08-03, registration hash sha256 58cce857b2657def19876bc0090f443ef74e5cb6383da2b2853b0347922bec8f, 72197 bytes, 2901 lines. Everything below this line is the supplied text, unaltered. Do not edit below this line. -->
Dokumentstatus: Draft
Produkt: Graphi
Programmphase: P1 — Trust Surface
Voraussetzung: P0 „Proof and Truth“ hat ein dokumentiertes GO erhalten
Primärer Scope: Go, CGo-freies Default-Binary, CLI, MCP stdio, lokale Graph Stores
Zieldokumentpfad im Repository: docs/plan/2026-07-graphi-p1-trust-surface-prd.md
Design-Baseline: Repository samibel/graphi, Stand afa1b686de381dd455ab08e4bf33aaf9420d6aab
Autorität: Diese PRD konkretisiert P1. Sie ersetzt weder die P0-PRD noch den bestehenden 9/10-Masterplan. Bei Konflikten gilt die ranghöhere, ausdrücklich als autoritativ markierte Planung.
Owner: noch festzulegen
Technische Freigabe: Graphi Maintainer
Review erforderlich durch: Engineering, Evaluation, Security/DevOps, Product
Letzte Aktualisierung: 2026-07-28

────────

1. Executive Summary

P0 beweist, wie gut der fokussierte Go-Kern von Graphi unter fest definierten Bedingungen funktioniert. P1 macht die Vertrauensgrenzen dieses Graphen im täglichen Einsatz sichtbar und maschinenlesbar.

Ein AI-Agent oder Entwickler darf nicht aus einer technisch erfolgreichen Graph-Abfrage automatisch schließen, dass das Ergebnis vollständig oder für jede Handlung sicher genug ist. Ein Ergebnis kann:

• aus bestätigten go/types-Beziehungen bestehen,
• aus deterministisch abgeleiteten Beziehungen bestehen,
• heuristische Cross-File-Beziehungen enthalten,
• an externen Dependencies enden,
• aus einem teilweise degradierten Package stammen,
• Dateien enthalten, die wegen Resource Bounds übersprungen wurden,
• mehrdeutige oder nicht auflösbare Referenzen verschweigen,
• auf einem veralteten Graphen ausgeführt worden sein.

Graphi besitzt viele der dafür notwendigen Rohsignale bereits:

• graphi status --json beschreibt Freshness, Drift, Indexzustand und Rebuild-Bedarf.
• Der Linker zählt ResolvedDerived, ResolvedHeuristic, ResolvedExternal, Skipped und Ambiguous.
• Der Go-Type-Resolver kennt bestätigte Edges, degradierte Units, Type Errors, übersprungene Dateien und verworfene Intents.
• Der Ingester besitzt strukturierte Skip-Diagnostics für Resource-Bound-Verletzungen.
• Graph-Edges tragen Confidence Tier und Evidence.
• Diagnostics besitzen deduplizierte und kategorisierte Qualitätsmetriken.

Diese Informationen sind heute jedoch über mehrere Komponenten verteilt. Ein Teil ist nur während oder unmittelbar nach dem Ingest verfügbar. Es existiert keine einheitliche, persistierte und versionierte Trust-Antwort für Nutzer und Agents.

P1 liefert deshalb:

1. Einen persistierten TrustSnapshot pro erfolgreicher Graph-Generation.
2. graphi trust-report als menschenlesbare und JSON-fähige CLI-Surface.
3. Ein einziges MCP-Tool graph_health für Repository- und Target-bezogene Trust-Abfragen.
4. Explizite Confidence-, Coverage-, Degradation- und Boundary-Signale.
5. Eine policy-basierte Bewertung für exploratory, review und automated_change.
6. Einen fail-closed Trust-Check, der nie aus fehlender Evidenz ein positives Ergebnis erzeugt.
7. Einen Labs-Prototyp für strikte Queries, der Ergebnisse nach Confidence und Policy begrenzen kann.
8. Ein versioniertes, deterministisches Wire-Format, das CLI und MCP gemeinsam nutzen.

P1 soll keinen künstlichen „Trust Score“ erzeugen, der unterschiedliche Risiken in einer einzigen Zahl versteckt. Die primäre Ausgabe besteht aus Fakten, Statusdimensionen, Limits, Warnings und expliziten Policy-Verdicts.

────────

2. Problem Statement

2.1 Aktuelle Situation

Graphi trennt bereits mehrere Confidence-Stufen:

• confirmed: durch einen Typechecker oder vergleichbar starke Evidenz bestätigt,
• derived: deterministisch aus lokalen Symbolbeziehungen abgeleitet,
• heuristic: durch sprachspezifische Resolver mit begrenzter Sicherheit zugeordnet.

Zusätzlich existieren nicht aufgelöste, mehrdeutige, externe oder degradierte Bereiche.

Diese Differenzierung ist technisch wertvoll. Sie erreicht den Nutzer jedoch nicht als zusammenhängende Antwort auf die entscheidende Frage:

> „Wie weit darf ich dieser Graphi-Antwort für die geplante Handlung vertrauen?“

Das heutige graphi status beantwortet eine andere Frage:

> „Existiert ein Graph und entspricht er dem aktuell ausgecheckten Code?“

status meldet Repository, Branch, Commit, Graphpfad, Node-Anzahl, Profil, Last Sync, Warm-Start-Fähigkeit, Drift, laufende oder abgebrochene Indexierung und eine Handlungsempfehlung.

Das ist notwendig, aber nicht ausreichend. Ein aktueller Graph kann trotzdem:

• viele heuristische Edges enthalten,
• degradierte Packages besitzen,
• Dateien übersprungen haben,
• externe Grenzen besitzen,
• relevante unresolved oder ambiguous References enthalten,
• für eine automatische Änderung ungeeignet sein.

2.2 Nutzerproblem

AI-Coding-Agent

Ein Agent erhält beispielsweise:

```text
impact(MyService) → 47 betroffene Symbole
```

Ohne Trust Surface weiß der Agent nicht:

• wie viele der traversierten Edges bestätigt sind,
• ob heuristische Edges enthalten sind,
• ob relevante Packages degradiert wurden,
• ob externe Dependencies den Pfad abschneiden,
• ob Dateien beim Index übersprungen wurden,
• ob der Graph aktuell ist,
• ob das Ergebnis für Exploration, Review oder autonome Änderung geeignet ist.

Entwickler

Ein Entwickler sieht eine plausible Caller-Liste, kann aber nicht erkennen:

• ob weitere Caller wegen unresolved References fehlen,
• ob die Antwort nur lokale Repository-Grenzen abdeckt,
• ob eine bestimmte Datei nur intra-file analysiert wurde,
• ob ein Typecheck für das Package degradiert war.

Reviewer oder Security Engineer

Ein Reviewer benötigt eine fail-closed Entscheidung:

```text
Kann diese Graph-Evidenz eine riskante Änderung unterstützen?
```

Eine pauschale Antwort „Graph ist gesund“ wäre irreführend. Die Bewertung muss an eine explizite Policy und einen konkreten Scope gebunden sein.

2.3 Produktproblem

Ohne Trust Surface entstehen drei Produktgefahren:

1. Overtrust: Agents behandeln heuristische oder partielle Ergebnisse als vollständig.
2. Undertrust: Nutzer ignorieren auch bestätigte und starke Evidenz, weil alle Ergebnisse gleich aussehen.
3. Claim Drift: Graphi kann „Confidence Tiers“ dokumentieren, ohne sie in der täglichen Agent-Journey praktisch nutzbar zu machen.

2.4 Geschäftliches Problem

Für professionelle Nutzung reicht es nicht, dass Graphi intern sorgfältig zwischen Confidence-Stufen unterscheidet. Teams benötigen:

• eine maschinenlesbare Trust-Antwort,
• nachvollziehbare Grenzen,
• reproduzierbare Policies,
• sichere Integration in Agent-Workflows,
• Auditability,
• klare Abstention.

P1 ist deshalb eine Voraussetzung für:

• sichere Review-Automation,
• spätere Refactoring-Promotion,
• Taint- und Security-Workflows,
• Enterprise-Piloten,
• nachvollziehbare Design-Partner-Evaluation.

────────

3. Source-derived Baseline

Dieser Abschnitt beschreibt den aktuellen technischen Ausgangspunkt. Er ist keine neue Produktbehauptung, sondern die Basis für die Anforderungen dieser PRD.

3.1 Bestehende Freshness-Surface

cmd/graphi/status.go besitzt bereits ein versioniertes JSON-Schema mit:

• Repository,
• Git Branch und Commit,
• Graphpfad,
• Node Count,
• Index Profile,
• Last Sync,
• Indexzustand,
• Drift,
• current,
• Recommendation.

P1 darf diese Verantwortung nicht duplizieren.

3.2 Bestehende Linker-Evidenz

engine/link.Stats enthält:

• ResolvedDerived
• ResolvedHeuristic
• ResolvedExternal
• Skipped
• Ambiguous

Der Ingester hält diese Werte aktuell in lastLinkStats. Diese Daten sind für eine Trust Surface relevant, aber als nur in-memory „letzter Lauf“-Zustand nicht ausreichend dauerhaft oder generation-sicher.

3.3 Bestehende Type-Resolver-Evidenz

engine/typeresolve.Result enthält:

• bestätigte Edges,
• Ergebnisse pro Package Unit,
• degradierte Units,
• Type Errors,
• übersprungene Dateien,
• DroppedIntents.

Diese Informationen sind entscheidend, um den bestätigten Go-Anteil und Degradationen sichtbar zu machen.

3.4 Bestehende Parse-Skip-Evidenz

Der Ingester speichert strukturierte Skip-Diagnostics für:

• zu große Dateien,
• Parse Timeouts,
• überschrittene Recursion Depth,
• weitere fail-closed Resource-Bound-Fälle.

Diese Daten dürfen keine Source Bytes enthalten, müssen aber als Trust-Limit persistiert werden.

3.5 Bestehende Diagnostic-Evidenz

engine/diagnostic.Metrics enthält:

• Anzahl Diagnostics,
• gezeigte und analysierte Findings,
• Suppression nach Kategorie,
• deduplizierte Findings.

Diese Signale sind ergänzend. Sie dürfen nicht mit Graph-Coverage verwechselt werden.

3.6 Aktuelle Lücke

Es existiert keine einheitliche, persistierte Antwort für:

• Confidence-Verteilung,
• Degradation,
• skipped und ambiguous Resolution,
• Parse Skips,
• externe Grenzen,
• Target-spezifische Trust-Einschätzung,
• Policy-Eignung.

────────

4. Produktvision für P1

> Graphi soll nicht nur eine Antwort liefern, sondern auch die Evidenzqualität, Grenzen und Eignung dieser Antwort für eine konkrete Handlung sichtbar machen.

Die ideale Agent-Journey:

```text
Agent erhält Aufgabe
→ Agent prüft `graph_health`
→ Graphi meldet Freshness, Confidence, Degradation und Boundaries
→ Agent wählt passende Policy
→ Graphi bewertet den relevanten Scope
→ Agent nutzt Stable Query
→ Agent zitiert Ergebnis und Trust-Limits
→ bei unzureichender Evidenz: Abstention oder zusätzliche Prüfung
```

P1 ersetzt keine menschliche Entscheidung. P1 liefert die Fakten und Policy-Entscheidungen, die eine sichere Entscheidung ermöglichen.

────────

5. Ziele

5.1 Primäre Ziele

1. Einen versionierten TrustSnapshot nach jeder erfolgreichen Graph-Generation persistieren.
2. Repository-weite Trust-Fakten schnell und read-only verfügbar machen.
3. Freshness aus der bestehenden Statuslogik wiederverwenden, nicht duplizieren.
4. Confidence-Verteilung für Nodes und Edges sichtbar machen.
5. Linker-Skips, Ambiguities und External Boundaries sichtbar machen.
6. Type-Resolver-Degradation pro Package sichtbar machen.
7. Parse-Skips mit sicherer Provenienz sichtbar machen.
8. Target- oder Pfad-bezogene Trust-Assessments ermöglichen.
9. Explizite Policies für Exploration, Review und automatische Änderung definieren.
10. Fehlende oder stale Trust-Evidenz fail-closed als UNKNOWN oder UNAVAILABLE behandeln.
11. CLI und MCP über denselben Engine-Port und dieselbe Serialisierung bedienen.
12. Default-Ausgaben kompakt und agententauglich halten.
13. Detailed Evidence opt-in verfügbar machen.
14. Kein Netzwerk, keine Telemetrie und keine externe Bewertung einführen.
15. Einen Labs-Prototyp für Confidence-gefilterte Queries bereitstellen.
16. P1 mit realen Agent-Tasks und adversarialen Trust-Fixtures evaluieren.

5.2 Sekundäre Ziele

• Grundlage für spätere Safe-Refactor-Policies schaffen.
• Grundlage für Taint-Coverage-Warnings schaffen.
• Nutzer über Repository-Grenzen und externe Dependencies informieren.
• Supportfälle reduzieren, in denen „leer“ mit „vollständig geprüft“ verwechselt wird.
• Ein wiederverwendbares Trust-Modell für spätere Sprachen schaffen.

────────

6. Nicht-Ziele

P1 umfasst ausdrücklich nicht:

• eine universelle numerische Vertrauenspunktzahl,
• die Behauptung, Graphi kenne die vollständige Ground Truth eines unbekannten Repositorys,
• neue GA-Sprachen,
• neue Stable Operations,
• eine automatische Promotion von Labs zu GA,
• vollständige Third-Party-Dependency-Indexierung,
• Cross-Repository Search,
• Cloud-Verifikation,
• Telemetrie,
• LLM-basierte Bewertung der Graphqualität,
• automatische Downloads von Dependencies,
• vollständige Framework-Semantik,
• einen Ersatz für Compiler, CodeQL oder Runtime Tests,
• eine generelle Erlaubnis für autonome Änderungen,
• Änderung des bestehenden graphi status-Vertrags zu einer kombinierten Trust-Surface,
• ein Dashboard oder eine neue Web UI,
• eine zweite Diagnoseengine,
• eine Policy-Sprache mit frei ausführbarem Code,
• automatische Security-Zertifizierung.

────────

7. Produktentscheidungen

7.1 status und trust-report bleiben getrennt

graphi status beantwortet:

• Ist ein Graph vorhanden?
• Ist er aktuell?
• Läuft oder scheiterte ein Index?
• Welche Drift existiert?
• Was soll der Nutzer als Nächstes tun?

graphi trust-report beantwortet:

• Welche Evidenzqualität besitzt der Graph?
• Welche Confidence-Tiers sind vorhanden?
• Welche Bereiche sind degradiert?
• Welche Dateien oder References wurden übersprungen?
• Wo endet die Coverage?
• Ist der Graph für eine bestimmte Policy geeignet?

Die Trust Surface darf status intern konsumieren, aber nicht dessen Verantwortung kopieren.

7.2 Kein einzelner globaler Trust Score

Ein Wert wie 87/100 wäre einfach zu kommunizieren, würde aber unterschiedliche Risiken unzulässig zusammenfassen.

Beispiel:

• 99 % bestätigte Edges,
• aber eine übersprungene Auth-Datei,
• oder ein degradiertes zentrales Package.

Ein globaler Durchschnitt könnte ein kritisches lokales Problem verdecken.

P1 verwendet deshalb:

• Dimensionen,
• Counts,
• Ratios,
• Statuswerte,
• Warnings,
• Limit Codes,
• Policy Verdicts.

7.3 Fakten und Policy werden getrennt

TrustSnapshot enthält beobachtete Fakten.

TrustAssessment bewertet diese Fakten gegen eine explizite Policy.

```text
Facts + Scope + Policy → Verdict
```

Eine Policy darf Fakten nicht verändern oder verstecken.

7.4 Trust ist scope-bezogen

Repository-weite Health ist nützlich, aber für eine konkrete Änderung oft zu grob.

P1 unterstützt:

• Repository Scope
• Package Scope
• File Scope
• Symbol Scope
• Query Result Scope, soweit technisch verfügbar

7.5 Fehlende Evidenz ist kein positives Signal

• Snapshot fehlt → UNAVAILABLE
• Snapshot gehört zu anderer Graph-Generation → STALE
• Scope kann nicht aufgelöst werden → UNKNOWN
• Datenquelle nicht unterstützt → UNKNOWN
• Partial Evidence → PARTIAL

────────

8. Zielnutzer und Jobs to Be Done

8.1 AI-Coding-Agent

Job: Vor einer Aufgabe bestimmen, ob Graphi-Evidenz für die geplante Handlung ausreicht.

Beispiele:

• Codebasis explorieren
• PR reviewen
• Change Impact analysieren
• Änderung planen
• autonome Änderung vorbereiten

8.2 Entwickler

Job: Verstehen, warum eine Graph-Abfrage vollständig, leer oder eingeschränkt erscheint.

8.3 Reviewer

Job: Erkennen, ob ein Review auf bestätigten, abgeleiteten oder heuristischen Beziehungen basiert.

8.4 Security Engineer

Job: Erkennen, ob Source-to-Sink- oder Impact-Aussagen durch Coverage-Gaps eingeschränkt sind.

8.5 Platform Team

Job: Eine maschinenlesbare Policy in Agent-Workflows oder CI integrieren.

8.6 Graphi Maintainer

Job: Trust-Regressions erkennen, ohne Rohdaten aus mehreren Subsystemen manuell zusammenzuführen.

────────

9. User Stories

US-1 — Repository Trust Overview

Als Entwickler möchte ich einen kompakten Trust Report sehen, damit ich die Qualität und Grenzen des aktuellen Graphen verstehe.

US-2 — Agent Preflight

Als AI-Agent möchte ich graph_health vor einer riskanten Aufgabe ausführen, damit ich weiß, ob ich Graphi-Evidenz verwenden, ergänzen oder ablehnen muss.

US-3 — Target Assessment

Als Reviewer möchte ich die Trust-Evidenz für ein bestimmtes Symbol oder Package sehen, damit globale Probleme nicht jeden lokalen Befund pauschal entwerten und lokale Probleme nicht in einem globalen Durchschnitt verschwinden.

US-4 — Confidence Breakdown

Als Nutzer möchte ich sehen, wie viele relevante Edges confirmed, derived oder heuristic sind.

US-5 — Degraded Package

Als Nutzer möchte ich wissen, wenn ein Package nicht vollständig type-geprüft werden konnte.

US-6 — Parse Skip

Als Nutzer möchte ich wissen, wenn Dateien wegen Resource Bounds oder Parse-Problemen nicht indexiert wurden.

US-7 — External Boundary

Als Nutzer möchte ich erkennen, wenn ein Pfad an einer externen Dependency endet.

US-8 — Ambiguous Resolution

Als Nutzer möchte ich sehen, wenn Referenzen wegen Mehrdeutigkeit verworfen wurden.

US-9 — Policy Check

Als Platform-Team möchte ich eine reproduzierbare Policy anwenden, damit ein Agent bei unzureichender Evidenz fail-closed stoppt.

US-10 — Strict Query Prototype

Als Agent möchte ich eine Query auf bestimmte Confidence-Tiers begrenzen, damit ich für riskante Aufgaben keine heuristischen Edges stillschweigend verwende.

────────

10. Scope

10.1 In Scope

• Go
• lokaler Graph Store
• bestehende Stable Operations als Datenkonsumenten
• graphi status als Freshness-Quelle
• Linker Stats
• Type Resolver Stats
• Parse Skip Diagnostics
• Edge Confidence
• External Nodes und Boundaries
• Diagnostic Summary als ergänzende Dimension
• CLI
• MCP stdio
• versionierte JSON-Ausgabe
• lokale Policies
• Target-spezifische Analyse
• Labs Strict Query Prototype

10.2 Out of Scope

• neue Parser
• Compiler-Resolver für neue Sprachen
• Dependency Source Attachment
• Cross-Repo
• Hosted Service
• UI
• Taint-Promotion
• Refactoring-Promotion
• automatische Codeänderung
• semantische Vollständigkeitsgarantie

────────

11. Terminologie

11.1 Confidence Tier

confirmed

Eine Beziehung wurde durch eine stärkere semantische Quelle wie go/types bestätigt.

derived

Eine Beziehung wurde deterministisch aus lokalen, eindeutigen Symbolinformationen abgeleitet.

heuristic

Eine Beziehung wurde durch sprachspezifische oder selector-basierte Heuristik zugeordnet.

11.2 Coverage Limit

Ein beobachteter Bereich, in dem Graphi keine vollständige strukturelle Navigation verspricht.

Beispiele:

• externe Dependency,
• übersprungene Datei,
• degradiertes Package,
• unresolved Reference,
• ambiguous Reference,
• unsupported language capability.

11.3 Trust Fact

Ein aus dem Graph, Ingest, Resolver oder Status deterministisch abgeleiteter Fakt.

11.4 Trust Policy

Eine versionierte Regelmenge, die Trust Facts für einen benannten Use Case bewertet.

11.5 Trust Verdict

• PASS
• WARN
• FAIL
• UNKNOWN

11.6 Snapshot State

• CURRENT
• STALE
• UNAVAILABLE
• INCOMPLETE

11.7 Scope

• repository
• package
• file
• symbol
• result-set

────────

12. High-Level Architecture

```text
                         ┌─────────────────────┐
                         │ graphi status logic │
                         │ freshness / drift   │
                         └──────────┬──────────┘
                                    │
┌─────────────────┐       ┌────────▼─────────┐       ┌────────────────────┐
│ ingest/link     │──────▶│ Trust Collectors │◀──────│ graphstore metadata │
│ stats/evidence  │       └────────┬─────────┘       └────────────────────┘
└─────────────────┘                │
                                   │
┌─────────────────┐       ┌────────▼─────────┐
│ typeresolve     │──────▶│ TrustSnapshot v1 │
│ units/errors    │       └────────┬─────────┘
└─────────────────┘                │
                                   │
┌─────────────────┐       ┌────────▼─────────┐
│ parse skips     │──────▶│ engine/trust     │
│ diagnostics     │       │ assess + policy  │
└─────────────────┘       └──────┬─────┬─────┘
                                 │     │
                         ┌───────▼─┐ ┌─▼──────────────┐
                         │ CLI     │ │ client port     │
                         │ report  │ │ shared marshal  │
                         └─────────┘ └──────┬─────────┘
                                           │
                                      ┌────▼────┐
                                      │ MCP     │
                                      │ health  │
                                      └─────────┘
```

12.1 Vorgeschlagene Packages

```text
engine/trust/
  types.go
  snapshot.go
  collect.go
  assess.go
  policy.go
  limitations.go
  scope.go
  serialize.go

engine/ingest/
  trust_persist.go
  trust_evidence.go

surfaces/client/
  trust_port.go
  trust_marshal.go

surfaces/mcp/
  trust_descriptor.go
  trust_handler.go

surfaces/cli/
  trust.go

cmd/graphi/
  trust_report.go
```

Die endgültige Struktur kann abweichen. Die Layering-Regel bleibt:

```text
cmd → surfaces → engine → core
```

────────

13. Datenmodell

13.1 TrustSnapshot

Der Snapshot beschreibt eine abgeschlossene Graph-Generation.

```go
type TrustSnapshot struct {
    SchemaVersion   int
    SnapshotVersion string

    GraphGeneration GraphGenerationRef
    Repository      RepositoryTrustFacts
    Index           IndexTrustFacts
    Languages       []LanguageTrustFacts
    Parse           ParseTrustFacts
    Link            LinkTrustFacts
    TypeResolution  TypeResolutionTrustFacts
    Graph           GraphTrustFacts
    External        ExternalBoundaryFacts
    Diagnostics     DiagnosticTrustFacts
    Limitations     []TrustLimitation
}
```

13.2 Graph Generation Reference

```go
type GraphGenerationRef struct {
    RepositoryRootFingerprint string
    SourceCommit              string
    Branch                    string
    GraphGenerationID         string
    IndexProfile              string
    CompletedAt               string
    BinaryVersion             string
    BinaryCommit              string
    SchemaVersion             string
}
```

CompletedAt ist informative Provenienz. Die kanonischen Trust Facts müssen ohne aktuelle Wall Clock deterministisch reproduzierbar sein.

13.3 Repository Trust Facts

```go
type RepositoryTrustFacts struct {
    FilesDiscovered int
    FilesIndexed    int
    Packages        int
    Modules         int
}
```

13.4 Language Trust Facts

```go
type LanguageTrustFacts struct {
    Language             string
    StabilityTier        string
    CapabilityLevel      string
    FilesDiscovered      int
    FilesIndexed         int
    FilesSkipped         int
    CrossFileResolver    bool
    ConfirmedResolver    bool
}
```

Empfohlene CapabilityLevel-Werte:

• typed_confirmed
• cross_file_heuristic
• intra_file_only
• parse_only
• not_shipped

P1 nutzt diese Werte primär für Go. Das Modell soll jedoch spätere Sprachen ohne Schema-Neuentwurf aufnehmen können.

13.5 Parse Trust Facts

```go
type ParseTrustFacts struct {
    Attempted int
    Parsed    int
    Skipped   int
    ByReason  map[string]int
    Evidence  []ParseSkipEvidence
}
```

Evidence ist in der Default-Antwort begrenzt und nur bei details=true vollständig.

13.6 Link Trust Facts

```go
type LinkTrustFacts struct {
    ResolvedDerived   int
    ResolvedHeuristic int
    ResolvedExternal  int
    Skipped           int
    Ambiguous         int
    ByFile            []FileLinkTrustFacts
}
```

13.7 Type Resolution Trust Facts

```go
type TypeResolutionTrustFacts struct {
    UnitsTotal        int
    UnitsChecked      int
    UnitsDegraded     int
    TypeErrors        int
    SkippedFiles      int
    DroppedIntents    int
    ConfirmedEdges    int
    DegradedUnits     []DegradedUnitEvidence
}
```

13.8 Graph Trust Facts

```go
type GraphTrustFacts struct {
    NodesTotal int
    EdgesTotal int

    EdgesByKind map[string]int
    EdgesByTier map[string]int

    ConfirmedRatio float64
    DerivedRatio   float64
    HeuristicRatio float64
}
```

Ratios sind informative Ableitungen aus Counts. Counts bleiben die primäre Evidenz.

13.9 External Boundary Facts

```go
type ExternalBoundaryFacts struct {
    ExternalNodes int
    ExternalEdges int
    Packages      int
    TopBoundaries []ExternalBoundaryEvidence
}
```

Default-Ausgaben dürfen Top-N-Grenzen zeigen, müssen aber klar angeben, dass dies kein vollständiger Dependency-Graph ist.

13.10 Trust Limitation

```go
type TrustLimitation struct {
    Code       string
    Severity   string
    Scope      ScopeRef
    Message    string
    Evidence   []EvidenceRef
    Action     string
}
```

13.11 Scope Reference

```go
type ScopeRef struct {
    Kind      string
    ID        string
    Path      string
    Package   string
    Symbol    string
}
```

13.12 Trust Assessment

```go
type TrustAssessment struct {
    SchemaVersion int
    Policy        PolicyRef
    Scope         ScopeRef
    SnapshotState string
    Verdict       string
    Facts         TrustFactSummary
    Findings      []TrustFinding
    Limitations   []TrustLimitation
    Recommendations []string
}
```

────────

14. Persistenzmodell

14.1 Anforderungen

Trust-Evidenz muss:

• an eine konkrete Graph-Generation gebunden sein,
• erst nach erfolgreichem Commit sichtbar werden,
• bei abgebrochenem Ingest nicht als aktuell erscheinen,
• read-only abrufbar sein,
• keinen Sourcecode duplizieren,
• keine externen Services benötigen,
• Full-/Incremental-Parität unterstützen.

14.2 Aggregate Snapshot

Der kanonische Aggregate Snapshot wird als versioniertes JSON oder normalisierte Metadata gespeichert.

Empfohlener Key:

```text
trust.snapshot.v1
```

Zusätzliche Metadaten:

```text
trust.snapshot.schema
trust.snapshot.generation
trust.snapshot.digest
```

14.3 Detail Evidence

Für Target-spezifische Assessments reichen Aggregate nicht aus.

Benötigt werden mindestens:

• Parse Skip nach Datei,
• Linker Skipped und Ambiguous nach Quelldatei,
• External Boundary nach Quelle und Ziel-QN,
• Type Resolver Degradation nach Package,
• Dropped Intents nach Package oder Datei,
• Confidence-Verteilung nach File, Package oder Symbol-Nachbarschaft.

Empfohlene Sidecar-Tabellen:

```sql
trust_file_evidence
trust_package_evidence
trust_external_boundary
trust_generation
```

Beispiel:

```sql
CREATE TABLE trust_generation (
    generation_id TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL,
    snapshot_json BLOB NOT NULL,
    snapshot_digest TEXT NOT NULL,
    source_commit TEXT NOT NULL,
    completed_at TEXT NOT NULL
);
```

```sql
CREATE TABLE trust_file_evidence (
    generation_id TEXT NOT NULL,
    path TEXT NOT NULL,
    language TEXT NOT NULL,
    parse_status TEXT NOT NULL,
    parse_reason TEXT NOT NULL,
    resolved_derived INTEGER NOT NULL,
    resolved_heuristic INTEGER NOT NULL,
    resolved_external INTEGER NOT NULL,
    skipped INTEGER NOT NULL,
    ambiguous INTEGER NOT NULL,
    PRIMARY KEY (generation_id, path)
);
```

```sql
CREATE TABLE trust_package_evidence (
    generation_id TEXT NOT NULL,
    package_key TEXT NOT NULL,
    degraded_reason TEXT NOT NULL,
    type_errors INTEGER NOT NULL,
    dropped_intents INTEGER NOT NULL,
    confirmed_edges INTEGER NOT NULL,
    PRIMARY KEY (generation_id, package_key)
);
```

Die genaue Speicherung ist eine Engineering-Entscheidung. Die semantischen Anforderungen dieser PRD sind verbindlich.

14.4 Atomicity

Trust Snapshot und Graph Generation müssen atomar gekoppelt sein.

Zulässige Implementierungen:

1. derselbe durable Commit,
2. staged Snapshot plus Generation Pointer Flip,
3. post-commit write mit fail-closed „snapshot unavailable“, bis vollständig.

Nicht zulässig:

• alter Snapshot auf neuem Graph,
• neuer Snapshot auf altem Graph,
• stilles Weiterverwenden nach Candidate- oder Schemawechsel.

────────

15. CLI Surface

FR-1 — graphi trust-report

Command

```bash
graphi trust-report [--json] [--details] \
  [--target <symbol|path|package>] \
  [--policy exploratory|review|automated_change]
```

Default-Verhalten

Ohne --target:

• Repository Scope,
• kompakte Human-Ausgabe,
• Freshness,
• Snapshot State,
• Confidence Counts,
• Degradation Counts,
• Resolution Gaps,
• External Boundaries,
• Limitations,
• keine unbeschränkten Detail-Listen.

Beispiel Human Output

```text
Graphi trust report

graph:
  state:          current
  generation:     7d2f...
  source:         main@afa1b686
  profile:        balanced

coverage:
  files:          1,428 / 1,431 indexed
  parse skipped:  3
  packages:       84
  degraded:       2

edge evidence:
  confirmed:      82,410
  derived:        11,203
  heuristic:      7,891

resolution:
  external:       1,204
  unresolved:     318
  ambiguous:      24
  dropped intents: 51

boundaries:
  external dependency navigation: unavailable
  cross-repository coverage: unavailable

policy:
  review: WARN

warnings:
  - 2 packages were not fully type-checked
  - 3 files were skipped during parsing
  - 24 ambiguous references were omitted

recommendation:
  review the affected packages or use --target for a scoped assessment
```

Exit Codes

Empfohlen:

• 0: Policy PASS, oder ohne Policy Snapshot verfügbar und current
• 1: WARN
• 2: Operational Error
• 3: FAIL
• 4: UNKNOWN oder UNAVAILABLE

Alternativ kann das Projekt bestehende Exit-Code-Konventionen wiederverwenden. Die Werte müssen eindeutig dokumentiert und getestet sein.

Acceptance Criteria

• --json ist versioniert.
• Default-Ausgabe bleibt kompakt.
• --details ist bounded oder paginiert.
• keine Source Bytes.
• keine Netzwerkzugriffe.
• stale Snapshot wird sichtbar.
• fehlender Snapshot wird nicht als gesund dargestellt.

────────

16. JSON Contract

FR-2 — trust-report --json

Beispiel

```json
{
  "schema_version": 1,
  "snapshot_version": "trust-v1",
  "snapshot_state": "CURRENT",
  "graph_generation": {
    "id": "7d2f...",
    "source_commit": "afa1b686de381dd455ab08e4bf33aaf9420d6aab",
    "profile": "balanced",
    "binary_commit": "afa1b686de381dd455ab08e4bf33aaf9420d6aab"
  },
  "freshness": {
    "current": true,
    "drift": {
      "added": 0,
      "changed": 0,
      "removed": 0
    }
  },
  "scope": {
    "kind": "repository",
    "id": ""
  },
  "coverage": {
    "files_discovered": 1431,
    "files_indexed": 1428,
    "files_skipped": 3,
    "packages_total": 84,
    "packages_degraded": 2
  },
  "edge_evidence": {
    "confirmed": 82410,
    "derived": 11203,
    "heuristic": 7891
  },
  "resolution": {
    "resolved_external": 1204,
    "skipped": 318,
    "ambiguous": 24,
    "dropped_intents": 51
  },
  "boundaries": [
    {
      "code": "EXTERNAL_NOT_NAVIGABLE",
      "severity": "info",
      "count": 1204
    }
  ],
  "policy": {
    "name": "review",
    "version": 1,
    "verdict": "WARN"
  },
  "limitations": [
    {
      "code": "TYPECHECK_DEGRADED",
      "severity": "warning",
      "count": 2,
      "action": "inspect degraded packages"
    }
  ]
}
```

Contract-Regeln

• Jede definierte Top-Level-Eigenschaft ist immer vorhanden.
• Leere Arrays statt null.
• Counts sind nicht negativ.
• Canonical Sort für Listen.
• Map-Ausgaben werden vor Serialization in sortierte Strukturen überführt, falls Byte-Parität erforderlich ist.
• Breaking Changes erhöhen schema_version.
• Neue additive Felder dürfen innerhalb derselben Major-Version ergänzt werden, wenn die bestehende Kompatibilitätsregel dies erlaubt.
• JSON enthält keine absoluten privaten Pfade, außer der Nutzer fordert ausdrücklich Details und die bestehende CLI-Konvention erlaubt es.
• Repository-Pfade bleiben normalisiert und relativ.

────────

17. MCP Surface

FR-3 — graph_health

P1 fügt genau ein neues Labs-MCP-Tool hinzu.

Warum nur ein Tool?

Die MCP-Tool-Fläche ist bereits groß. P1 soll Trust zentralisieren und keine neuen Einzelfunktionen für jede Dimension erzeugen.

Tool Name

```text
graph_health
```

Input Schema

```json
{
  "type": "object",
  "properties": {
    "target": {
      "type": "string",
      "description": "optional symbol id, qualified name, repository-relative path or package"
    },
    "policy": {
      "type": "string",
      "enum": ["exploratory", "review", "automated_change"],
      "description": "optional trust policy"
    },
    "details": {
      "type": "boolean",
      "description": "include bounded supporting evidence"
    },
    "limit": {
      "type": "integer",
      "description": "maximum detail items"
    }
  }
}
```

Output

Das MCP-Tool verwendet dasselbe kanonische TrustAssessment-Format wie die CLI.

Tool Metadata

• Labs
• read-only
• idempotent
• non-destructive
• local-only
• no network
• deterministic für dieselbe Graph-Generation und denselben Input

Acceptance Criteria

• CLI und MCP liefern semantisch identische Fakten.
• Default MCP Output bleibt unter dem definierten Tokenbudget.
• Tool führt keine Indexierung aus.
• Tool startet keinen Daemon.
• Tool lädt keine Dependencies.
• Tool meldet stale oder unavailable explizit.
• Tool ist nur im Labs-Profil sichtbar, bis ein separater Promotion-Entscheid erfolgt.

────────

18. Freshness Integration

FR-4 — Reuse von status

P1 muss die bestehende Statuslogik als Freshness-Quelle verwenden oder eine gemeinsame, tieferliegende read-only Komponente extrahieren.

Nicht zulässig:

• zweite Drift-Implementierung,
• andere Branch-Semantik,
• andere Warm-Start-Definition,
• widersprüchliche Recommendation.

Trust Snapshot State

CURRENT

• Graph existiert,
• warm-startable,
• kein Drift,
• Snapshot Generation stimmt mit Graph Generation überein.

STALE

• Source Drift,
• Snapshot Generation passt nicht,
• Graph wurde nach Snapshot geändert.

INCOMPLETE

• Full Pass Marker,
• abgebrochener Ingest,
• Snapshot unvollständig.

UNAVAILABLE

• kein Graph,
• kein Snapshot,
• alte inkompatible Graph-Version,
• Trust-Daten nicht migrierbar.

Acceptance Criteria

• status und trust-report widersprechen sich nicht.
• status.current=false kann niemals zu Trust Snapshot State CURRENT führen.
• ein laufender Ingest wird als INCOMPLETE oder „in progress“ sichtbar.
• ein Snapshot wird erst nach erfolgreichem Ingest freigegeben.

────────

19. Confidence Distribution

FR-5 — Edge Confidence Facts

P1 muss Counts für mindestens folgende Tiers ausgeben:

• confirmed
• derived
• heuristic

Optional:

• unknown
• legacy/unclassified

Scope-Verhalten

Repository

Counts über den gesamten Graphen.

Package/File

Counts für Edges mit Source oder relevantem Endpoint im Scope. Die Semantik muss dokumentiert werden.

Symbol

Counts über die unmittelbar relevanten oder traversierten Edges des Assessments.

Regeln

• Externe Edges bleiben heuristic.
• Linker-Edges dürfen nicht als confirmed umetikettiert werden.
• Typechecker-bestätigte Edges müssen als confirmed erkennbar bleiben.
• Ratios werden aus Counts berechnet.
• Ein hoher confirmed Ratio erzeugt allein kein PASS.

Acceptance Criteria

• Fixture mit rein confirmed Edges.
• Fixture mit gemischten Tiers.
• Fixture mit nur heuristic Edges.
• Counts stimmen mit Graph Store überein.
• Full-/Incremental-Parität gilt auch für Trust Counts.

────────

20. Parse Coverage

FR-6 — Parse Skip Visibility

P1 muss übersprungene Dateien sichtbar machen.

Gründe

Mindestens:

• file too large
• parse timeout
• recursion depth exceeded
• unsupported parser
• parse failure
• root confinement rejection
• unreadable file, soweit relevant

Default Output

• Gesamtzahl,
• Counts nach Reason,
• höchstens Top-N Paths bei Details.

Security

• keine File Contents,
• normalisierte relative Paths,
• keine Secrets,
• keine ungefilterten Parser Errors mit Source-Snippets.

Policy-Wirkung

• exploratory: WARN bei Skips
• review: WARN oder FAIL, wenn Scope betroffen
• automated_change: FAIL, wenn Scope betroffen; globaler Skip außerhalb des Scopes mindestens WARN

Acceptance Criteria

• Skip verschwindet nach erfolgreicher Reindexierung.
• stale Skip-Evidence wird nicht weiterverwendet.
• Skip im Target File führt bei automated_change zu FAIL.
• Skip außerhalb des Scopes wird separat ausgewiesen.

────────

21. Link Resolution Coverage

FR-7 — Unresolved und Ambiguous Visibility

P1 muss Linker-Gaps sichtbar machen:

• skipped/unresolved
• ambiguous
• external materialized
• derived
• heuristic

Detail Evidence

Pro Datei oder Package:

```json
{
  "path": "internal/service/foo.go",
  "resolved_derived": 12,
  "resolved_heuristic": 4,
  "resolved_external": 3,
  "skipped": 2,
  "ambiguous": 1
}
```

Policy-Wirkung

Exploratory

• heuristic erlaubt,
• unresolved/ambiguous als Warning,
• kein automatischer Fail außer Snapshot unavailable.

Review

• ambiguous im Scope → WARN,
• unresolved im Scope → WARN,
• hoher heuristic Anteil → WARN nach Policy Threshold,
• parse skip im Scope → FAIL oder WARN nach definierter Severity.

Automated Change

• ambiguous im Scope → FAIL,
• parse skip im Scope → FAIL,
• unresolved im Scope → FAIL, sofern die Änderung davon betroffen sein kann,
• nur heuristic tragender kritischer Pfad → FAIL,
• externe Boundary im relevanten Pfad → WARN oder FAIL abhängig von Aktion.

Acceptance Criteria

• keine positive Policy-Antwort bei fehlenden Scope-Daten.
• Ambiguity wird nicht als resolved gezählt.
• External wird getrennt von unresolved gezählt.
• Detail-Listen sind deterministisch sortiert.

────────

22. Type Resolution Health

FR-8 — Package Degradation

P1 muss pro Go-Package sichtbar machen:

• checked oder degraded,
• Degradation Reason,
• Type Error Count,
• Dropped Intents,
• Confirmed Edge Count,
• Skipped Files.

Wichtige Semantik

Type Errors bei Stub-Imports können erwartet sein und bedeuten nicht automatisch, dass das gesamte Package degradiert ist.

P1 darf deshalb nicht einfach:

```text
type_errors > 0 → package failed
```

verwenden.

Stattdessen muss es unterscheiden:

• checked with type errors,
• degraded,
• skipped,
• partial confirmed coverage.

Beispiel

```json
{
  "package": "engine/query",
  "state": "checked_with_errors",
  "type_errors": 17,
  "dropped_intents": 3,
  "confirmed_edges": 284
}
```

Acceptance Criteria

• Degraded Reason wird nicht aus Type Error Count erfunden.
• Units mit Type Errors, aber erfolgreichem Check bleiben checked.
• Packages mit Panic oder Full-Parse-Failure werden degraded.
• Package Assessment kann Target-Scope beeinflussen.

────────

23. External Boundaries

FR-9 — Boundary Visibility

P1 macht explizit:

• externe Nodes sind terminale Grenzknoten,
• strukturelle Queries navigieren nicht in externe Implementierungen,
• externe Calls können für lokale Evidenz und bestimmte Analyzer sichtbar sein,
• Cross-Repository Coverage ist nicht vorhanden.

Boundary Codes

Mindestens:

• EXTERNAL_NOT_NAVIGABLE
• CROSS_REPOSITORY_UNAVAILABLE
• DEPENDENCY_INTERNALS_UNKNOWN
• DYNAMIC_RUNTIME_UNKNOWN

Target Assessment

Wenn ein Target oder Query Result externe Edges berührt:

• Anzahl Grenzen,
• betroffene externe Qualified Names,
• lokale Call Sites,
• Policy-Auswirkung.

Acceptance Criteria

• „External“ wird nicht als „unresolved“ bezeichnet.
• Keine Behauptung über Library-Internals.
• Default-Ausgabe zeigt bounded Top-N.
• vollständige Details benötigen explizite Option.

────────

24. Diagnostic Dimension

FR-10 — Diagnostics als ergänzende Evidenz

Diagnostics dürfen im Trust Report erscheinen, aber nicht mit Coverage gleichgesetzt werden.

Mögliche Felder:

• total diagnostics,
• shown,
• suppressed by category,
• dedup collapsed,
• critical/high counts, falls Severity-Modell vorhanden.

Regeln

• Keine Diagnostic bedeutet nicht automatisch „Graph vollständig“.
• Viele Diagnostics bedeuten nicht automatisch „Graph unbrauchbar“.
• Diagnostic-Evidenz ist eine separate Dimension.

Acceptance Criteria

• Report benennt die Dimension eindeutig.
• Keine Vermischung mit Linker Skips.
• Default-Ausgabe bleibt kompakt.

────────

25. Trust Policies

FR-11 — Versionierte Built-in Policies

P1 liefert drei fest definierte Policies.

25.1 exploratory-v1

Zweck:

• Codebasis verstehen,
• Dateien finden,
• Hypothesen bilden,
• Follow-up Queries planen.

Regeln:

• Graph muss vorhanden sein.
• stale Graph → WARN oder FAIL nach Drift.
• heuristische Edges erlaubt.
• unresolved und ambiguous werden sichtbar.
• Parse Skips erzeugen WARN.
• externe Boundaries erzeugen INFO/WARN.
• fehlender Snapshot → UNKNOWN.

25.2 review-v1

Zweck:

• PR Review,
• Change Impact,
• Risikoanalyse,
• Reviewer-Fragen.

Regeln:

• Graph muss current sein.
• Target muss auflösbar sein.
• Parse Skip im Target Scope → FAIL.
• degraded Package im Target Scope → WARN oder FAIL nach Schwere.
• Ambiguous References im Scope → WARN.
• rein heuristic kritischer Pfad → WARN.
• externe Boundaries im Pfad → WARN.
• fehlende Scope-Evidenz → UNKNOWN.

25.3 automated-change-v1

Zweck:

• Vorbereitung einer autonomen Änderung,
• Safe Delete,
• Inline,
• automatisierte Refactorings.

Regeln:

• Graph current.
• Snapshot current.
• Target eindeutig.
• kein Parse Skip im Scope.
• kein degraded Package im Scope.
• keine Ambiguity im Scope.
• keine unresolved References im relevanten Scope.
• kritische Beziehungen mindestens derived; bevorzugt confirmed.
• externe Boundary mit möglicher Verhaltensabhängigkeit → FAIL oder explizite Human Approval.
• fehlende Evidenz → FAIL oder UNKNOWN, niemals PASS.

Policy Definition

Policies werden als Code oder statische versionierte Daten implementiert.

Nicht zulässig:

• Nutzer liefert ausführbaren Ausdruck,
• dynamisches Scripting,
• LLM entscheidet Verdict,
• stille Threshold-Änderung ohne Version Bump.

Acceptance Criteria

• gleiche Facts + gleiche Policy → gleiches Verdict.
• jede Policy besitzt Fixtures.
• jede Regel besitzt Finding Code.
• Policy-Version steht im Output.
• Threshold-Änderung erhöht Policy-Version.

────────

26. Policy Findings

FR-12 — Explainable Verdicts

Jedes Verdict muss begründet sein.

```go
type TrustFinding struct {
    Code       string
    Severity   string
    Dimension  string
    Scope      ScopeRef
    Observed   any
    Threshold  any
    Message    string
    Evidence   []EvidenceRef
}
```

Beispiele:

• GRAPH_STALE
• SNAPSHOT_MISSING
• PARSE_SKIPPED_IN_SCOPE
• PACKAGE_DEGRADED
• AMBIGUOUS_REFERENCE_IN_SCOPE
• UNRESOLVED_REFERENCE_IN_SCOPE
• HEURISTIC_ONLY_PATH
• EXTERNAL_BOUNDARY_REACHED
• TARGET_NOT_FOUND
• SCOPE_EVIDENCE_UNAVAILABLE

Acceptance Criteria

• kein Verdict ohne Findings oder explizite „all checks passed“-Liste.
• Findings sind sortiert.
• Findings enthalten Action oder Recommendation.
• Messages enthalten keine unbelegten Behauptungen.

────────

27. Target Resolution

FR-13 — Target Scope

--target und MCP target akzeptieren:

• Node ID,
• Qualified Name,
• repository-relative File Path,
• Package Identifier.

Ambiguity

Ein Bare Name mit mehreren Treffern:

• wird nicht automatisch gewählt,
• erzeugt TARGET_AMBIGUOUS,
• gibt bounded Candidates zurück,
• Verdict ist UNKNOWN oder FAIL abhängig von Policy.

NotFound

Ein nicht vorhandenes Target:

• erzeugt TARGET_NOT_FOUND,
• wird nicht als leerer Scope behandelt.

Scope Expansion

Symbol

• direkte Node,
• relevante incident Edges,
• optional bounded Neighborhood,
• owning File,
• owning Package.

File

• Nodes der Datei,
• von der Datei ausgehende und eingehende relevante Edges,
• File Evidence.

Package

• Package Files,
• Type Unit,
• Package Evidence,
• package-interne und grenzüberschreitende Edges.

Acceptance Criteria

• Scope Semantik dokumentiert.
• keine unbounded Whole-Graph-Traversal für Symbol Assessment.
• selektive Reads werden genutzt.
• Target Assessment p95 erfüllt Performance Gate.

────────

28. Strict Query Prototype

FR-14 — Labs Strict Query

P1 liefert nach dem Kern-Trust-Report einen Labs-Prototyp.

Produktentscheidung

Die bestehenden Stable Operations werden nicht still verändert.

Zulässige Formen:

Option A — Labs Wrapper

```bash
graphi query-strict callers \
  --symbol p.MyFunc \
  --min-tier derived \
  --policy review
```

Option B — Optional Labs Flags

```bash
graphi query callers \
  --symbol p.MyFunc \
  --trust-policy review \
  --min-tier derived
```

Option B ist nur zulässig, wenn die zusätzlichen Flags den bestehenden Default-Vertrag nicht verändern und klar als Labs markiert werden können.

Verhalten

• Policy Preflight.
• Query ausführen.
• Result Edges nach erlaubten Tiers filtern.
• entfernte Edges zählen.
• Result Trust Assessment ausgeben.
• nicht vorgaukeln, gefiltertes Result sei vollständig.

Beispiel

```json
{
  "operation": "callers",
  "result": {
    "nodes": [],
    "edges": []
  },
  "filter": {
    "minimum_tier": "derived",
    "excluded_heuristic_edges": 7
  },
  "trust": {
    "verdict": "WARN",
    "limitations": [
      "7 heuristic caller edges were excluded"
    ]
  }
}
```

Acceptance Criteria

• Default Stable Query unverändert.
• kein Edge-Tier wird hochgestuft.
• Filtering ist deterministisch.
• excluded Count sichtbar.
• leer nach Filter wird nicht als „keine Caller existieren“ dargestellt.
• MCP-Prototyp nur, wenn kein zweites unnötiges Tool erforderlich ist; bevorzugt über graph_health plus bestehende Query-Outputs.

────────

29. Recommendations

FR-15 — Actionable Next Step

Trust Report darf Empfehlungen ausgeben.

Beispiele:

• run 'graphi sync'
• run 'graphi rebuild'
• inspect degraded package engine/query
• use --target for scoped assessment
• review 3 skipped files
• do not use automated_change policy
• supplement with compiler or tests
• human review required at external boundary

Regeln

• deterministisch,
• keine LLM-Generierung,
• aus Finding Codes abgeleitet,
• bounded,
• keine automatische destructive Action.

────────

30. Performance Requirements

NFR-1 — Repository Report Latency

Auf dem P0-Referenz-Stressrepo:

• Aggregate trust-report --json p95 ≤ 100 ms bei vorhandenem Snapshot.
• Human Rendering p95 ≤ 120 ms.
• Kein Whole-Graph Scan im Hot Path.

NFR-2 — Target Assessment Latency

• Symbol Scope p95 ≤ 200 ms.
• File Scope p95 ≤ 250 ms.
• Package Scope p95 ≤ 300 ms.
• Bounded Details p95 ≤ 500 ms.

NFR-3 — Output Budget

Default:

• CLI Human ≤ 120 Zeilen.
• JSON ≤ 64 KiB.
• MCP Output ≤ 8.000 geschätzte Tokens; Ziel ≤ 2.000 Tokens.
• Details sind durch limit bounded.

NFR-4 — Storage Budget

• Aggregate Snapshot ≤ 256 KiB für das Referenz-Stressrepo.
• Detail Evidence ≤ 5 % der Graph-DB-Größe oder ein explizit gemessenes alternatives Budget.
• keine Source-Duplikation.

NFR-5 — Ingest Overhead

• Trust Collection erhöht Full Index p95 um höchstens 5 %.
• Trust Collection erhöht Incremental Update p95 um höchstens 10 %.
• Peak RSS Overhead höchstens 5 %.
• Überschreitungen benötigen Profil und Design Review.

────────

31. Determinism and Parity

NFR-6 — Deterministische Facts

Gleiche Graph-Generation und gleicher Scope erzeugen:

• dieselben Counts,
• dieselben Finding Codes,
• dasselbe Verdict,
• dieselbe Sortierung,
• dieselbe kanonische JSON-Struktur.

Zeitstempel oder absolute Laufzeitdaten dürfen die kanonische Gleichheit nicht verfälschen.

NFR-7 — Full/Incremental Parity

Wenn Full und Incremental denselben finalen Graphen erzeugen, müssen sie auch denselben TrustSnapshot erzeugen, abgesehen von ausdrücklich nicht-kanonischer Provenienz wie Completion Time.

Acceptance Criteria

• TrustSnapshot Digest gleich.
• Detail Evidence gleich.
• Policy Verdicts gleich.
• External Boundary Counts gleich.
• Skip-Evidence gleich.

────────

32. Security and Privacy

NFR-8 — Local-first

• keine Netzwerkaufrufe,
• keine Telemetrie,
• keine Remote-Verifikation,
• keine Accounts,
• keine API Keys.

NFR-9 — Data Minimization

Persistiert werden nur:

• Counts,
• relative Paths,
• Package IDs,
• Qualified Names,
• Reason Codes,
• Confidence,
• Evidence Anchors.

Nicht persistiert:

• vollständige Source Files,
• Source Snippets ohne Notwendigkeit,
• Secrets,
• Environment Variables,
• Benutzeridentität.

NFR-10 — Path Safety

• relative normalisierte Paths,
• Root Confinement,
• keine ..-Escapes,
• keine Symlink-Ausbrüche,
• Detail-Rendering respektiert bestehende Path-Security-Regeln.

NFR-11 — Fail Closed

• corrupt Snapshot → UNAVAILABLE,
• Digest mismatch → STALE/FAIL,
• unbekannte Policy → Operational Error,
• fehlende Detailquelle → UNKNOWN,
• keine Default-PASS-Fallbacks.

────────

33. Reliability

NFR-12 — Crash Safety

Ein Crash:

• vor Graph Commit → alter Snapshot bleibt aktiv,
• nach Graph Commit, vor Snapshot Publish → Snapshot unavailable/stale, nie fälschlich current,
• nach Snapshot Publish → neue Generation konsistent.

NFR-13 — Migration

Bei älteren Graph Stores:

• trust-report meldet UNAVAILABLE mit Handlungsempfehlung,
• optional graphi rebuild,
• keine automatische positive Migration ohne vollständige Evidenz.

NFR-14 — Concurrency

• read-only Report während Ingest erkennt laufende Generation,
• kein Lesen halbgeschriebener Snapshot-Daten,
• keine Data Race auf lastLinkStats,
• persistierte Generation ist die Read Boundary.

────────

34. Observability Without Telemetry

P1 benötigt interne lokale Messbarkeit:

• Collect Duration
• Snapshot Size
• Evidence Rows
• Assessment Duration
• Scope Resolution Duration
• Policy Evaluation Duration
• Output Size
• Cache Hit/Miss, falls Cache verwendet wird

Diese Werte:

• bleiben lokal,
• können in Benchmarks und Tests ausgewertet werden,
• werden nicht automatisch übertragen.

────────

35. Error Model

Fehlerklassen

• ErrTrustSnapshotUnavailable
• ErrTrustSnapshotStale
• ErrTrustSnapshotCorrupt
• ErrTrustSchemaUnsupported
• ErrTrustTargetNotFound
• ErrTrustTargetAmbiguous
• ErrTrustScopeUnsupported
• ErrTrustPolicyUnknown
• ErrTrustDetailLimit
• ErrTrustSelectiveLookupUnavailable

Surface Mapping

CLI:

• klare Fehlermeldung,
• dokumentierter Exit Code,
• keine Stack Traces standardmäßig.

MCP:

• typisierte Tool-Fehler,
• keine leeren Erfolgsantworten bei Operational Errors.

────────

36. Evaluation Plan

36.1 Trust Fixture Corpus

Mindestens folgende Fixtures:

1. vollständig aktueller, rein confirmed Graph,
2. gemischte confirmed/derived/heuristic Edges,
3. stale Graph,
4. fehlender Graph,
5. fehlender Snapshot,
6. corrupt Snapshot,
7. Parse Skip im Target File,
8. Parse Skip außerhalb des Targets,
9. degradiertes Target Package,
10. degradiertes anderes Package,
11. unresolved Reference im Scope,
12. ambiguous Reference im Scope,
13. externe Boundary im Pfad,
14. Bare Name mit mehreren Targets,
15. Target NotFound,
16. Full-/Incremental-Parität,
17. laufender Ingest,
18. abgebrochener Ingest,
19. altes Schema,
20. unsupported language capability.

36.2 Policy Matrix

Für jedes Fixture werden alle drei Policies ausgeführt.

Mindestens 60 versiegelte Policy-Fälle:

```text
20 Fixtures × 3 Policies
```

Zusätzlich mindestens 20 kombinierte oder adversariale Fälle.

36.3 Agent Tasks

Mindestens 30 Tasks:

• 10 Exploration,
• 10 Review,
• 10 Automated Change Preflight.

Bewertet werden:

• wählt der Agent die richtige Policy,
• erkennt der Agent Warnings,
• stoppt der Agent bei FAIL/UNKNOWN,
• zitiert der Agent Trust Limits,
• vermeidet der Agent falsche Vollständigkeitsclaims.

36.4 Human Usability

Mindestens 10 externe oder nicht implementierende Nutzer.

Aufgaben:

• Trust Report interpretieren,
• Unterschied status/trust erklären,
• geeignetes Vorgehen wählen,
• Target Assessment ausführen.

────────

37. Success Metrics

37.1 Correctness

|Metrik                                  |Gate             |
|----------------------------------------|----------------:|
|Snapshot-Fakten gegen Ground Truth      |100 % in Fixtures|
|Policy-Verdicts gegen versiegelte Matrix|≥ 98 %           |
|False PASS                              |0                |
|Stale als Current                       |0                |
|Missing Evidence als PASS               |0                |
|Scope Resolution Accuracy               |≥ 99 %           |
|Full-/Incremental Trust Parity          |100 %            |

37.2 Agent Safety

|Metrik                                           |Gate  |
|-------------------------------------------------|-----:|
|Agent stoppt bei `automated_change` FAIL         |100 % |
|Agent erkennt UNKNOWN                            |≥ 95 %|
|Agent zitiert mindestens ein relevantes Limit    |≥ 90 %|
|Agent behauptet keine Vollständigkeit bei Partial|≥ 95 %|

37.3 UX

|Metrik                                       |Gate   |
|---------------------------------------------|------:|
|Nutzer unterscheiden Status und Trust korrekt|≥ 90 % |
|Nutzer wählen passende Policy                |≥ 85 % |
|Median Time to Interpret                     |≤ 2 min|
|Unbeaufsichtigter CLI Task Success           |≥ 85 % |

37.4 Performance

|Metrik                |Gate                      |
|----------------------|-------------------------:|
|Aggregate Report p95  |≤ 100 ms                  |
|Symbol Assessment p95 |≤ 200 ms                  |
|Package Assessment p95|≤ 300 ms                  |
|Full Index Overhead   |≤ 5 %                     |
|Incremental Overhead  |≤ 10 %                    |
|Default MCP Tokens    |Ziel ≤ 2.000, hart ≤ 8.000|

────────

38. Work Packages

WP1.0 — Contract and Architecture

Deliverables

• freigegebene P1 PRD,
• ADR für Status-vs-Trust-Separation,
• Trust Terminology,
• JSON Schema v1,
• Policy Specification v1,
• Storage Decision,
• Threat Model Addendum.

Exit Gate

Alle Contract-Entscheidungen vor Implementierung eingefroren.

────────

WP1.1 — Durable Trust Snapshot

Aufgaben

1. TrustSnapshot Types implementieren.
2. Generation Binding definieren.
3. Aggregate Collector bauen.
4. Snapshot Digest erzeugen.
5. persistieren.
6. atomic publish.
7. old-store behavior.
8. corruption tests.
9. Full-/Incremental parity tests.

Exit Gate

Jede erfolgreiche Generation besitzt einen konsistenten Snapshot; keine falsche aktuelle Evidenz nach Crash.

────────

WP1.2 — Detail Evidence Index

Aufgaben

1. File Evidence Schema.
2. Package Evidence Schema.
3. External Boundary Schema.
4. Linker Stats pro Datei.
5. Type Resolver Summary pro Package.
6. Parse Skip Persistence.
7. Selective Read Ports.
8. Migration und Cleanup.
9. Storage Budgets.

Exit Gate

Target Assessment benötigt keinen Whole-Graph Scan.

────────

WP1.3 — Engine Trust Service

Aufgaben

1. Repository Assessment.
2. Target Resolution.
3. File Scope.
4. Package Scope.
5. Symbol Scope.
6. Limitation Builder.
7. Recommendation Builder.
8. Canonical Sorting.
9. Shared Serialization.

Exit Gate

Pure oder read-only Trust-Evaluation mit stabilen Fixtures.

────────

WP1.4 — Built-in Policies

Aufgaben

1. exploratory-v1.
2. review-v1.
3. automated-change-v1.
4. Finding Codes.
5. versionierte Thresholds.
6. Policy Matrix.
7. False-PASS Red Gates.

Exit Gate

0 False PASS in versiegelten Fixtures.

────────

WP1.5 — CLI Surface

Aufgaben

1. graphi trust-report.
2. --json.
3. --target.
4. --policy.
5. --details.
6. Exit Codes.
7. Help.
8. HOWTO.
9. golden output tests.

Exit Gate

Unbeaufsichtigte Nutzer können Report interpretieren und richtige Handlung wählen.

────────

WP1.6 — MCP Surface

Aufgaben

1. Client Port.
2. shared marshaller.
3. graph_health descriptor.
4. handler.
5. Labs gating.
6. capability filtering.
7. tool annotations.
8. parity tests.
9. token budget tests.

Exit Gate

CLI und MCP sind semantisch identisch.

────────

WP1.7 — Strict Query Prototype

Aufgaben

1. Designentscheidung Option A/B.
2. Preflight Policy.
3. Confidence Filter.
4. excluded Count.
5. Result Trust Envelope.
6. no-empty-misrepresentation gate.
7. agent evaluation.

Exit Gate

Keine Stable-Semantik wird verändert; gefilterte Leere wird korrekt erklärt.

────────

WP1.8 — Evaluation and Release Decision

Aufgaben

1. 20 Basis-Fixtures.
2. 80 Policy-Fälle.
3. 30 Agent Tasks.
4. 10 Human Usability Tests.
5. Performance Harness.
6. Security Review.
7. independent reproduction.
8. P1 Evidence Pack.
9. Go/No-Go.

Exit Gate

Alle Success Metrics grün.

────────

39. Priorisierter Backlog

P1-A — Blocker

1. ADR: status vs trust-report.
2. Trust Terminology einfrieren.
3. JSON Schema v1.
4. Policy Spec v1.
5. Generation Binding.
6. Snapshot Atomicity.
7. False-PASS Red Gates.
8. Storage Budget.
9. Threat Model Addendum.
10. Owner und Reviewer benennen.

P1-B — Snapshot

11. Aggregate Collector.
12. Edge Tier Counts.
13. Link Stats Collector.
14. Parse Skip Collector.
15. Type Resolver Collector.
16. External Boundary Collector.
17. Diagnostic Summary Collector.
18. Snapshot Digest.
19. Persist/Read.
20. Old Store Handling.

P1-C — Detail Evidence

21. File Evidence Model.
22. Package Evidence Model.
23. External Boundary Evidence.
24. Selective Lookup.
25. Scope Resolver.
26. Bounded Detail Output.
27. Cleanup alter Generations.
28. Storage Regression Tests.

P1-D — Policies

29. Exploratory Policy.
30. Review Policy.
31. Automated Change Policy.
32. Finding Codes.
33. Recommendations.
34. Policy Versioning.
35. Policy Matrix.
36. False-PASS Audit.

P1-E — Surfaces

37. CLI Human Renderer.
38. CLI JSON.
39. CLI Exit Codes.
40. Client Port.
41. MCP Descriptor.
42. MCP Handler.
43. Surface Parity.
44. Help und Docs.

P1-F — Strict Mode

45. Strict Query ADR.
46. Confidence Filter.
47. Query Trust Envelope.
48. Excluded Count.
49. Empty Result Semantics.
50. Agent Tests.

P1-G — Evidence

51. Trust Fixture Corpus.
52. Agent Tasks.
53. Human Tests.
54. Benchmark.
55. Security Review.
56. Independent Reproduction.
57. P1 Evidence Index.
58. Go/No-Go Decision.

────────

40. Suggested Story Slices

Story P1-001 — Freeze Trust Contract

Outcome: JSON, Terminology und Statuswerte sind eingefroren.

Story P1-002 — Persist Aggregate Snapshot

Outcome: erfolgreicher Ingest schreibt einen generation-gebundenen Snapshot.

Story P1-003 — Fail Closed on Missing or Stale Snapshot

Outcome: kein false green.

Story P1-004 — Persist Parse and Link Evidence

Outcome: Skips, Ambiguities und External Counts sind dauerhaft sichtbar.

Story P1-005 — Persist Type Resolver Health

Outcome: Package Degradation ist sichtbar.

Story P1-006 — Repository Trust Service

Outcome: read-only Assessment ohne Whole-Graph Scan.

Story P1-007 — Target Scope Resolver

Outcome: Symbol, File und Package sind eindeutig bewertbar.

Story P1-008 — Built-in Policies

Outcome: reproduzierbare Verdicts.

Story P1-009 — CLI Trust Report

Outcome: Human und JSON Surface.

Story P1-010 — MCP Graph Health

Outcome: Agent Preflight.

Story P1-011 — Surface Parity

Outcome: gleiche Facts und Findings über CLI/MCP.

Story P1-012 — Strict Query Prototype

Outcome: Confidence-gefilterte Labs Query.

Story P1-013 — Trust Evaluation Corpus

Outcome: versiegelte Policy- und Agent-Evaluation.

Story P1-014 — Performance and Storage Gates

Outcome: Trust Surface bleibt leichtgewichtig.

Story P1-015 — P1 Independent Audit

Outcome: reproduzierbares Evidence Pack und Go/No-Go.

────────

41. Delivery Sequence

Phase 1 — Contract

```text
Terminology
→ JSON Schema
→ Policy Spec
→ Storage ADR
→ False-PASS Red Gates
```

Phase 2 — Snapshot

```text
Collectors
→ Generation Binding
→ Persist
→ Read
→ Corruption/Crash Tests
```

Phase 3 — Scope

```text
File Evidence
→ Package Evidence
→ Symbol Resolution
→ Selective Assessment
```

Phase 4 — Policies

```text
Facts
→ Findings
→ Verdict
→ Recommendations
```

Phase 5 — Surfaces

```text
Engine Port
→ CLI
→ Client
→ MCP
→ Parity
```

Phase 6 — Strict Prototype

```text
Policy Preflight
→ Confidence Filter
→ Trust Envelope
→ Agent Tests
```

Phase 7 — Evidence

```text
Fixtures
→ Agent Tasks
→ Human Tests
→ Performance
→ Security
→ Independent Reproduction
```

────────

42. Acceptance Criteria by Epic

AC-TRUST-01 — Snapshot Integrity

• Snapshot gehört exakt zur aktiven Generation.
• Digest stimmt.
• kein partial publish.
• Crash erzeugt kein current false green.

AC-TRUST-02 — Confidence Accuracy

• Counts stimmen mit Store.
• confirmed/derived/heuristic bleiben getrennt.
• keine Upgrades.

AC-TRUST-03 — Degradation Accuracy

• Package State stimmt mit Type Resolver.
• Type Errors werden nicht pauschal als Degradation behandelt.

AC-TRUST-04 — Skip Accuracy

• Parse Skips vollständig und sicher.
• stale Skips verschwinden.

AC-TRUST-05 — Boundary Accuracy

• External getrennt von unresolved.
• keine Navigation in unbekannte Internals behauptet.

AC-TRUST-06 — Policy Safety

• 0 False PASS.
• fehlende Evidenz nie PASS.
• automated-change stoppt auf blockierenden Gaps.

AC-TRUST-07 — Scope Accuracy

• Target Resolution ≥ 99 %.
• Ambiguity wird nicht automatisch entschieden.

AC-TRUST-08 — Surface Parity

• CLI/MCP Facts gleich.
• Findings und Verdict gleich.
• Canonical Serialization.

AC-TRUST-09 — Performance

• alle P1 Performance Gates.

AC-TRUST-10 — Usability

• Nutzer verstehen Status vs Trust.
• Agents reagieren sicher.

────────

43. Go/No-Go-Kriterien

GO

P1 erhält GO, wenn:

• P0 GO dokumentiert ist,
• Contract und Policies eingefroren sind,
• Snapshot atomar und generation-sicher ist,
• Detail Evidence verfügbar ist,
• CLI und MCP funktionieren,
• 0 False PASS in Fixtures,
• Policy Accuracy mindestens 98 %,
• Trust Parity 100 %,
• Performance Gates grün,
• Security Review grün,
• Agent Safety Gates grün,
• unabhängige Reproduktion bestanden,
• keine offenen High/Critical Findings.

NO-GO

• Snapshot kann auf falscher Generation current erscheinen.
• missing Evidence führt zu PASS.
• automated-change Policy erlaubt bekannte Scope-Gaps.
• Status und Trust widersprechen sich.
• Target Ambiguity wird still aufgelöst.
• CLI und MCP unterscheiden sich semantisch.
• Trust Report benötigt Whole-Graph Scan im Hot Path.
• Index Overhead überschreitet Budget ohne akzeptierte Entscheidung.
• Sourcecode oder Secrets werden unnötig persistiert.
• Strict Query stellt gefilterte Leere als vollständige Leere dar.

CONDITIONAL GO

Für P1-Abschluss nicht zulässig.

Ein Strict-Query-Prototyp kann separat als Labs experimental bleiben, wenn der Kern von P1 vollständig grün ist und die restliche Trust Surface nicht davon abhängt.

────────

44. Stop-Regeln

• Kein globaler numerischer Trust Score ohne separate PRD und externe Validierung.
• Keine neue Policy ohne Version.
• Kein Threshold Change ohne Evidence und Policy Version Bump.
• Kein Stable-Promotion-Claim innerhalb P1.
• Kein neuer MCP-Tool-Split, solange graph_health ausreichend ist.
• Kein Whole-Graph Hot-Path-Fallback.
• Keine automatische Reparatur eines degradierten Graphen durch Trust Report.
• Kein LLM im Verdict.
• Kein Remote Service.
• Kein false-green Fallback bei Migration oder Corruption.
• Kein Strict Mode, der Default-Stable-Semantik verändert.

────────

45. Risiken und Mitigationen

Risiko 1 — Trust Report wird als Vollständigkeitsbeweis missverstanden

Mitigation:

• klare Terminologie,
• Limit Codes,
• keine globale Punktzahl,
• explizite UNKNOWN-Dimensionen.

Risiko 2 — Snapshot veraltet gegenüber Graph

Mitigation:

• Generation Binding,
• Digest,
• atomic publish,
• stale checks.

Risiko 3 — Zu großer Storage Overhead

Mitigation:

• Aggregate + selektive Details,
• keine Source-Duplikation,
• Budgets,
• Profiling.

Risiko 4 — Zu großer MCP Output

Mitigation:

• compact default,
• bounded details,
• limit,
• Top-N.

Risiko 5 — Policy wird zu komplex

Mitigation:

• drei Built-ins,
• keine freie DSL,
• versionierte statische Regeln.

Risiko 6 — Global Health entwertet lokalen guten Scope

Mitigation:

• Target Assessment,
• Findings außerhalb und innerhalb Scope getrennt.

Risiko 7 — Lokaler kritischer Gap verschwindet in globalen Counts

Mitigation:

• keine Durchschnitts-PASS-Logik,
• scope-blockierende Findings.

Risiko 8 — Type Errors werden falsch interpretiert

Mitigation:

• checked_with_errors vs degraded,
• Resolver-Semantik direkt übernehmen.

Risiko 9 — Strict Filtering erzeugt falsche Leere

Mitigation:

• excluded Count,
• explicit partial outcome,
• Red Gate.

Risiko 10 — Tool-Fläche wächst weiter

Mitigation:

• genau ein MCP-Tool,
• keine Dimensionstools.

Risiko 11 — Trust-Erfassung verlangsamt Ingest

Mitigation:

• vorhandene Stats wiederverwenden,
• aggregieren während vorhandener Passes,
• kein zusätzlicher Whole-Graph Scan,
• Performance Gate.

Risiko 12 — Private Pfade oder Daten leaken

Mitigation:

• relative Paths,
• Details opt-in,
• keine Source Bytes,
• Security Review.

────────

46. Documentation Requirements

Zu aktualisieren:

• readme.md
• docs/cli-reference.md
• docs/HOWTO.md
• docs/agent-workflows.md
• docs/stability-tiers.md
• docs/FEATURES.md
• docs/capability-manifest.json
• docs/coverage-matrix.yaml
• Website Tutorial
• MCP Setup Tutorial
• Changelog

Erforderliche Dokumentation

1. Unterschied Status vs Trust.
2. Confidence Tiers.
3. Policy Semantik.
4. External Boundaries.
5. UNKNOWN und FAIL.
6. Beispiele für Agent Preflight.
7. Strict Query Einschränkungen.
8. Datenschutz.
9. Performancegrenzen.
10. Schema-Versionierung.

────────

47. Example Agent Workflow

```text
1. Call graph_health(policy="review", target="engine/query.Service")
2. Verify snapshot_state == CURRENT
3. Read verdict and limitations
4. If FAIL or UNKNOWN:
   - do not make a definitive graph-only claim
   - inspect source or run additional tools
5. Call impact / callers / related_files
6. Include trust limitations in the answer
7. For automated change:
   - require automated_change PASS
   - otherwise request human review
```

Example Safe Agent Response

```text
Graphi found 14 local dependents. The graph is current.
Twelve relevant edges are confirmed or derived; two are heuristic.
The target package is not degraded, but the path reaches one external dependency,
whose internal behavior is outside Graphi's coverage. Treat this as a structural
local blast-radius estimate, not a complete runtime guarantee.
```

────────

48. Definition of Done

P1 ist abgeschlossen, wenn:

☐ P0 GO dokumentiert ist.
☐ P1 Owner und Reviewer benannt sind.
☐ Status-vs-Trust ADR akzeptiert ist.
☐ Trust Terminology eingefroren ist.
☐ JSON Schema v1 eingefroren ist.
☐ Built-in Policy Spec v1 eingefroren ist.
☐ TrustSnapshot implementiert ist.
☐ Snapshot generation-gebunden ist.
☐ Snapshot atomar publiziert wird.
☐ Snapshot Digest geprüft wird.
☐ Corruption fail-closed behandelt wird.
☐ alte Stores UNAVAILABLE melden.
☐ Parse Skips persistiert werden.
☐ Linker Stats persistiert werden.
☐ Type Resolver Health persistiert wird.
☐ External Boundaries persistiert werden.
☐ Confidence Counts korrekt sind.
☐ Detail Evidence selektiv gelesen wird.
☐ kein Whole-Graph Hot-Path Scan existiert.
☐ Repository Assessment implementiert ist.
☐ Symbol Assessment implementiert ist.
☐ File Assessment implementiert ist.
☐ Package Assessment implementiert ist.
☐ exploratory-v1 implementiert ist.
☐ review-v1 implementiert ist.
☐ automated-change-v1 implementiert ist.
☐ 0 False PASS in Fixtures existieren.
☐ graphi trust-report implementiert ist.
☐ --json implementiert ist.
☐ --target implementiert ist.
☐ --policy implementiert ist.
☐ --details bounded ist.
☐ Exit Codes dokumentiert sind.
☐ MCP graph_health implementiert ist.
☐ MCP Tool Labs-gegated ist.
☐ CLI/MCP Parität 100 % beträgt.
☐ Default MCP Output im Budget liegt.
☐ Strict Query Prototype implementiert oder explizit separat deferred ist.
☐ Strict Query keine Stable-Semantik verändert.
☐ Trust Fixture Corpus vollständig ist.
☐ mindestens 80 Policy-Fälle versiegelt sind.
☐ mindestens 30 Agent Tasks ausgewertet sind.
☐ mindestens 10 Human Usability Tests ausgewertet sind.
☐ Policy Accuracy mindestens 98 % beträgt.
☐ Target Resolution Accuracy mindestens 99 % beträgt.
☐ Full-/Incremental Trust Parity 100 % beträgt.
☐ Aggregate Report p95 ≤ 100 ms beträgt.
☐ Symbol Assessment p95 ≤ 200 ms beträgt.
☐ Package Assessment p95 ≤ 300 ms beträgt.
☐ Full Index Overhead ≤ 5 % beträgt.
☐ Incremental Overhead ≤ 10 % beträgt.
☐ Security Review grün ist.
☐ unabhängige Reproduktion bestanden ist.
☐ Evidence Index vollständig ist.
☐ keine offenen High/Critical Findings bestehen.
☐ P1 Go/No-Go dokumentiert ist.

────────

49. Post-P1 Entry Criteria für P2

P2 „Measured Go Gaps“ darf beginnen, wenn P1 GO erhält.

Erlaubte P2-Themen:

• gemessene High-Confidence-Falschaussagen reduzieren,
• Go Recall verbessern,
• Package Degradation reduzieren,
• Framework-Fixtures,
• bessere confirmed Coverage,
• gemessene Performance-Hotspots,
• Target-spezifische Trust-Gaps.

Nicht automatisch erlaubt:

• neue GA-Sprache,
• neue Stable Surface,
• vollständige Dependency-Indexierung,
• Taint-GA,
• Refactoring-GA,
• SaaS.

Diese benötigen eigene Evidenz und eigene PRDs.

────────

50. Kurzfassung der Produktentscheidung

P1 macht Graphi nicht „allwissend“. P1 macht sichtbar, was Graphi weiß, wie stark es das weiß und wo es bewusst nicht weiter weiß.

```text
Freshness wiederverwenden
→ Trust Facts persistieren
→ Confidence sichtbar machen
→ Degradation sichtbar machen
→ Boundaries sichtbar machen
→ Scope bestimmen
→ Policy anwenden
→ Verdict erklären
→ CLI und MCP ausliefern
→ Strict Query sicher prototypisieren
→ adversarial evaluieren
```

Das Ergebnis von P1 soll eine Trust Surface sein, die AI-Agents und Entwicklern eine sichere, reproduzierbare und ehrliche Grundlage für Exploration, Review und spätere automatisierte Änderungen liefert.
