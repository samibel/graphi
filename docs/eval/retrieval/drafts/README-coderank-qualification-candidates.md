# CodeRank-Qualifikations-Kandidaten — NICHT ungeprüft weiterverwenden

Datum: 2026-09-17
Status: **Entwurf, nicht Teil eines Datensatzes. Für die Development-Qualifikation nicht benötigt.**

## Wozu diese Dateien existieren

- `coderank-qualification-candidate-queries.json` — 35 Kandidaten-Queries gegen
  `spf13/cobra` am Commit `a0a6ae020bb3899ff0276067863e50523f897370`
- `coderank-qualification-candidate-queries.md` — Review-Leitfaden, unsicherste zuerst
- `review-exact_identifier.json` — Review R1 (7 Kandidaten: 3 accepted, 3 amended, 1 rejected)
- `review-exact_path.json` — Review R2 (8 Kandidaten: 8 accepted)
- `review-nl_behaviour.json` — Review R5 (6 Kandidaten: 4 accepted, 1 amended, 1 rejected)

**Ohne Review:** die Lose `ambiguous` (7 Kandidaten) und `architecture_flow`
(7 Kandidaten). Für die liegt nichts vor — 21 von 35 Kandidaten sind geprüft.

Sie entstanden unter der Annahme, es gebe keinen konformen 64-Query-Datensatz.
**Diese Annahme war falsch.** `docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json`
(id `cobra-v3-dev-reviewed`, sha256
`33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba`) erfüllt das
Gate vollständig — 64/64, alle Strata exakt, `relevant_min_grade: 3`, nicht auf
der spent-Liste. Nachzuprüfen mit:

```
go run ./cmd/embedded-model-qualification validate-dataset \
  --dataset docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json
```

## Warum sie trotzdem aufgehoben werden

Für den Release fehlt weiterhin ein **neues, versiegeltes 64-Query-Holdout**.
Diese 35 Kandidaten sind ein möglicher Ausgangspunkt dafür.

## Warum sie dafür NICHT unverändert taugen

Ein repo-weiter Scan über 405 Query-Dateien (alle Datensätze, Entwürfe und
Run-Artefakte) zeigt zwei Kollisionsklassen. Beide würden ein daraus gebautes
Holdout entwerten.

### 1. Identischer Query-Text mit `cobra-v3-dev-reviewed` (Dev-Split)

| Kandidat | kollidiert mit | Stratum |
|---|---|---|
| cq-02 | cd-70 | exact_identifier |
| cq-09 | cd-74 | exact_path |
| cq-10 | cd-17 | exact_path |
| cq-15 | cd-18 | exact_path |
| cq-18 | cd-91 | ambiguous |
| cq-21 | cd-57 | ambiguous |

Beide Seiten tragen verschiedene `family_id`. Genau das hebelt aus, wofür
`family_id` existiert: käme eine Seite ins Holdout und die andere in den
Dev-Split, wäre die Antwort vorab bekannt. **Diese sechs müssen fallen oder die
Gegenseite muss fallen — nicht beide behalten.**

### 2. Grade-3-Spans, die bereits in versiegelten Holdouts vorkommen

Betroffen: cq-01, cq-02, cq-03, cq-04, cq-05, cq-06, cq-07, cq-16, cq-17,
cq-20, cq-23, cq-24, cq-25, cq-26, cq-28, cq-30, cq-33, cq-34 — insgesamt 18
von 35.

Überschneidungen mit `cobra-fresh-sealed-holdout-v5-2026-09-13`,
`cobra-second-fresh-sealed-holdout-v5-2026-09-13`,
`cobra-compact16-fresh-sealed-holdout-2026-09-15`,
`cobra-compact17-fresh-unseen-v2` / `-v3` / `-v4` sowie cobra-v2-Holdout-Zeilen.

Jene Holdouts sind verbraucht, für die laufende Qualifikation also unschädlich.
Für ein **neues** Holdout gilt das nicht: es darf keine Grade-3-Spans mit
`cobra-v3-dev-reviewed` teilen, das der Qualifikations-Dev-Split ist.

## Wer diese Dateien weiterverwendet, muss vorher

1. den Kollisionsscan erneut fahren — das Skript liegt nicht im Repo, die
   Prüfung ist aber in zwei Schritten reproduzierbar: identischer `query`-Text
   gegen alle Query-Dateien, und Grade-3-Span-Tupel `(path, start_line, end_line)`
   gegen alle Query-Dateien;
2. die sechs Textdubletten auflösen;
3. die 18 Span-Überschneidungen gegen den **dann geltenden** Dev-Split prüfen,
   nicht gegen die verbrauchten Holdouts;
4. die Lose `ambiguous` und `architecture_flow` überhaupt erst reviewen — für
   die 14 Kandidaten liegt kein Review vor;
5. `reviewer` in jedem judgement setzen. `Judgement.validate()`
   (`internal/eval/retrieval/dataset.go:294`) weist jedes judgement mit leerem
   `reviewer` ab; im Ursprungsentwurf war es in allen 167 leer.

## Bekannte inhaltliche Befunde aus den drei vorliegenden Reviews

- **cq-02 wurde abgelehnt** (R1) — Textdublette zu cd-70, siehe oben.
- **cq-34 wurde abgelehnt** (R5) — sein einziger Grade-3-Span
  `command.go:879-881` ist zugleich der einzige Grade-3-Span der Holdout-Zeile
  `ci-1343`, die nach demselben Verhalten fragt. Eine neue `family_id` verdeckt
  die Dev/Holdout-Überschneidung, statt sie zu beseitigen. Die Spans sind
  gültig, die Zeile ist kein unabhängiges Dev-Item. Deckt sich mit Klasse 2 des
  Scans oben.
- **cq-05** hatte einen kaputten Span (`doc/yaml_docs.go:30-51`, mitten im
  Doc-Kommentar abgeschnitten); auf `end_line: 46` korrigiert.
- **cq-04** judgement 2 von grade 2 auf 1 herabgestuft — der Span nennt das
  gefragte Symbol nirgends.
- **cq-07** judgement 3: `reason` behauptete einen „only in-tree caller", der
  nicht stimmt (`bash_completions.go:565` ruft ebenfalls auf); korrigiert.
- Zwei Spans sind handgesetzt statt abgeleitet und im JSON markiert:
  `bash_completionsV2.go:31-379` und `command.go:1285-1290`.
