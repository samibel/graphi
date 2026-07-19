# Business- & Monetization-Review

## Kurzurteil

`graphi` ist ein technisch substanzielles, klar positioniertes OSS-Produkt, aber noch **kein belastbares Business**. Das Repository belegt eine starke Produktthese (lokale Code-Intelligence für Entwickler und Coding Agents), sehr breite Funktionsabdeckung, reproduzierbare Releases und ernsthafte Trust-Arbeit. Es belegt jedoch **keinen einzigen zahlenden Kunden, keinen Preis, keinen kommerziellen Plan, keinen Kauf- oder Kontaktpfad, keinen aktiven SaaS-Betrieb und keine Adoption-Metrik**. Die Website deklariert das einzige Angebot ausdrücklich mit Preis `0`; der primäre CTA ist „Star on GitHub“. Brutal formuliert: Hier wurde sehr viel Produkt gebaut, bevor nachweisbar geklärt wurde, wer wofür bezahlt.

Das Marktpotenzial ist aus dem Repository heraus **UNKNOWN**. Technisch adressiert das Produkt nachvollziehbare Probleme und besitzt mehrere denkbare Commercialization-Seams (CI/PR-Gates, IDE, MCP, lokale/selbst gehostete Analyse). Ob daraus ein Markt wird, ist ohne repo-belegte Nachfrage, Nutzung und Zahlungsbereitschaft nicht feststellbar.

## Score 0–10

**3,5/10 Business- und Monetarisierungsreife**

- Produkt-/Value-Proposition-Reife: **7/10** – Problem, Zielgruppen und Differenzierung sind klar dokumentiert.
- OSS-Reife: **7/10** – permissive Lizenz, Contributions, Releases, Security Policy und mehrere Installationspfade sind vorhanden.
- Distribution-Reife: **5/10** – GitHub Releases und Installer sind solide; weitere Kanäle sind teilweise nur vorbereitet oder nicht nachweisbar veröffentlicht.
- Monetarisierungsreife: **1/10** – kein Angebot, kein Preis, kein Billing, keine Entitlements, keine Sales-/Lead-Strecke.
- Marktvalidierung: **2/10** – zwei externe Feldtests belegen Testnutzung und Probleme, aber keine Adoption oder Zahlungsbereitschaft.
- SaaS-Reife: **1/10** – das Kernversprechen schließt Accounts, Telemetrie und regulären Netzwerkbetrieb explizit aus; eine SaaS-Architektur oder Control Plane ist nicht belegt.

## Größte Stärken

1. **Scharfe, verständliche Positionierung.** Das Produkt verspricht eine lokale, provenance-gestützte Code-Graph-Schicht, die wiederholtes Greppen und Whole-File-Reads für Menschen und Agents ersetzt. Die beiden Zielgruppen – Entwickler und AI Coding Agents – sind explizit benannt (`readme.md:96-117`).

2. **Glaubwürdiger Trust-Moat im Produkt.** Local-first, keine Accounts, keine Telemetrie und kein Runtime-Egress sind nicht nur Marketingtext; Setup-/Privacy-Audit und CI-Gates werden dokumentiert (`docs/setup-privacy.md:42-63`, `CONTRIBUTING.md:9-20`). Das ist ein klarer Differenzierungsanker, auch wenn die kommerzielle Nachfrage danach UNKNOWN bleibt.

3. **Sehr niedrige OSS-Einstiegshürde.** Checksum-verifizierte One-Line-Installer für Unix und Windows sowie der direkte Start im Ziel-Repo reduzieren Aktivierungsfriktion (`readme.md:41-66`). `graphi setup` integriert mehrere lokale MCP-Clients (`readme.md:80-91`).

4. **Breite Produktoberfläche und natürliche Distributions-Seams.** CLI, MCP, HTTP/SSE, TUI, Web, VS Code und GitHub Action teilen dieselbe Engine (`docs/FEATURES.md:90-115`). Insbesondere PR-Review plus optionalem Merge-Gate ist näher an einem teamweiten, wiederkehrenden Workflow als eine reine lokale Visualisierung (`extensions/github-action/action.yml:54-68`, `extensions/github-action/action.yml:85-95`).

5. **Reproduzierbarer Release- und Qualitätsapparat.** Cross-Platform-Assets, Checksums, eingebettete Web-UI und GitHub Release Upload sind automatisiert (`.github/workflows/release.yml:135-186`). Die interne Release-Gate-Logik blockiert definierte Qualitätsregressionen (`readme.md:368-385`). Das senkt grundsätzlich die operative Hürde für Distribution und Support.

6. **Ehrliche Feldtest-Aufarbeitung.** Zwei externe Tester und konkrete Real-Repo-Fehler sind dokumentiert; die Gegenmaßnahmen sind reproduzierbar gegatet (`docs/real-world-report.md:1-40`, `docs/real-world-report.md:61-74`). Das ist stärker als reine Feature-Behauptung und gut für OSS-Vertrauen.

7. **Permissive Lizenz schafft Reichweiten- und Integrationschancen.** Apache-2.0 erlaubt kostenlose Reproduktion, Modifikation, Unterlizenzierung und Distribution sowie Patentnutzung (`LICENSE:66-80`). Das erleichtert Adoption und Drittintegration.

## Größte Schwächen

1. **Kein Geschäftsmodell im Repository.** Es gibt keine belegte Free/Pro/Team/Enterprise-Aufteilung, keine Preis- oder Packaging-Seite, kein Billing, keine Lizenzprüfung, keine Entitlements, keinen Trial, kein Sponsor-Modell und keine Support-SLA. Die einzige maschinenlesbare Offer-Angabe setzt den Preis auf `0` (`site/index.html:35-46`).

2. **Kein Conversion Funnel.** Die Landingpage führt zu „Star on GitHub“ oder kostenloser Installation (`site/index.html:583-590`); im Footer fehlen Contact, Demo, Sales, Newsletter oder Waitlist (`site/index.html:595-611`). Damit kann das Projekt weder Kaufabsicht erfassen noch Leads qualifizieren.

3. **Keine repo-belegte Nachfrage.** Es gibt keine Nutzungs-, Installations-, Download-, Retention-, Team-Rollout-, Conversion- oder Umsatzmetrik. Die zwei externen Feldtests zeigen reale Nutzung, aber beide dokumentieren primär schwere Defekte, nicht wiederholte Nutzung oder Kaufabsicht (`docs/real-world-report.md:9-21`).

4. **Feature-Überbreite vor Business-Fokus.** Das Inventory umfasst 38 MCP-Tools und sehr viele Epics (`docs/FEATURES.md:56-87`), während kommerzielle Basiselemente vollständig fehlen. Diese Asymmetrie erhöht Wartungs- und Messagingkosten, ohne einen repo-belegten Umsatzhebel zu schaffen.

5. **Das „Saved $X“-Versprechen ist kein Umsatzbeweis und noch nicht vollautomatisch.** Die Messung vergleicht gegen eine intern definierte Whole-File-Read-Baseline (`docs/meter/metering.md:31-55`). Noch kritischer: Die automatische Daemon-Einbindung der Meter→Price→Cap→Ledger-Kette für *jeden* Engine-Call wird als spätere Integration bezeichnet (`docs/savings/cap-readout.md:72-79`). Der prominent im Quick Start beworbene Dollarwert (`readme.md:62-66`) ist daher kein belastbarer Nachweis von realisiertem Kunden-ROI.

6. **OSS-Capture ist ungelöst.** Apache-2.0 erlaubt Forks, Modifikationen und kommerzielle Weiterverteilung (`LICENSE:66-71`, `LICENSE:89-128`). Das ist hervorragend für Reichweite, aber ohne proprietären Service, Marke, Daten-/Netzwerkeffekt, Hosted Control Plane oder Support-Angebot ist im Repository kein Mechanismus erkennbar, der Wert zuverlässig beim Maintainer abschöpft.

7. **SaaS steht im Konflikt zum aktuellen Markenkern.** „Alles lokal“, „keine Accounts“, „keine Telemetrie“, „keine Netzwerkaufrufe“ ist ausdrücklich Teil des Produkts (`readme.md:112-117`). Das ist kein technisches Problem, aber ein Hosted-SaaS-Modell würde eine neue Vertrauens- und Architekturgeschichte benötigen. Eine solche ist nicht vorhanden.

8. **Distributionskanäle sind teilweise nur vorbereitet.** Homebrew-/Scoop-Publishing wird ohne Secret sauber übersprungen, und die Workflow-Kommentare verlangen erst noch die Erstellung der Ziel-Repositories (`.github/workflows/release.yml:187-205`). Die committed Manifeste sind Platzhalter mit Version `0.0.0` und werden ausdrücklich nicht publiziert (`.github/workflows/release.yml:190-197`).

9. **VS-Code-Distribution ist nur package-fähig, nicht nachweisbar publiziert.** Das Manifest enthält Publisher/Version und `vsce package`, aber keinen Publish-Workflow oder belegten Marketplace-Link (`extensions/vscode/package.json:1-10`, `extensions/vscode/package.json:108-116`).

10. **Die GitHub Action wirkt als Distributionsartefakt nicht release-sicher.** Ihr Default verweist auf `v0.43.0` (`extensions/github-action/action.yml:36-43`), während der Repository-Changelog aktuell bis `0.4.0` reicht (`CHANGELOG.md:64-95`). Die Beispiel-Dokumentation verwendet außerdem Platzhalter-Koordinaten statt eines belegten publizierten Action-Slugs (`extensions/github-action/README.md:71-99`). Damit ist der geschäftlich interessanteste Team-Workflow nicht als tatsächlich konsumierbarer Kanal belegt.

## Kritische Blocker

1. **Kein validierter Ideal Customer Profile (ICP).** „Developers“ und „AI coding agents“ sind Zielgruppen, aber keine Käuferdefinition. Buyer, Budget Owner, Teamgröße, Repo-Profil, Compliance-Anforderung, Einkaufsprozess und dringendster Use Case sind UNKNOWN.

2. **Keine Zahlungsbereitschaft.** Ohne Preisexperiment, Design-Partner-Vertrag, Sponsor, Support-Anfrage oder bezahlten Pilot ist jede Monetarisierung reine Hypothese.

3. **Kein kaufbares Angebot.** Selbst bei vorhandener Nachfrage gibt es im Repository nichts, das ein Kunde auswählen, bestellen, lizenzieren oder vertraglich beziehen kann.

4. **Kein sauberer OSS→Paid-Wertzaun.** Der freie Kern enthält bereits lokale Engine, 38 MCP-Tools, Web, IDE und PR-Suite. Was ein Team zusätzlich bezahlen soll, ist nicht definiert.

5. **Keine Produktanalytik und kein freiwilliger Feedbackkanal.** Die harte No-Telemetry-Haltung ist markenkonsistent, aber ohne privacy-kompatible Opt-in-Metriken oder sichtbare Feedbackstrecke bleiben Activation, Retention und Feature-Nutzen UNKNOWN.

6. **Team-Distribution nicht verlässlich abgeschlossen.** GitHub Action und VS-Code-Erweiterung sind technisch vorhanden, aber ihre tatsächliche Marketplace-Verfügbarkeit ist repo-seitig nicht belegt; Homebrew/Scoop können still übersprungen werden.

7. **ROI-Claim noch nicht Ende-zu-Ende geschlossen.** Der Savings-Mechanismus ist gut instrumentiert, aber laut eigener Scope Boundary nicht automatisch über alle Engine-Calls gelegt. Vor Pricing- oder Sales-Nutzung muss der Claim auf echten Nutzerworkflows vollständig und transparent gemessen werden.

## Technische/kommerzielle Schulden

### Technische Schulden mit kommerzieller Wirkung

- **Zu viele Supportoberflächen:** CLI, zwei MCP-Transporte, HTTP/SSE, TUI, Web, VS Code und Action erhöhen Release-, Dokumentations- und Supportkosten (`docs/FEATURES.md:90-115`). Es fehlt eine geschäftliche Priorisierung nach Adoption oder Revenue Influence.
- **Pre-1.0-/Latest-only-Support:** Security-Fixes gelten nur für den neuesten Release (`SECURITY.md:27-30`). Für ein mögliches Enterprise-Angebot fehlen repo-belegt LTS, Backport-Policy und Supportfenster.
- **GitHub-Action-Versionsdrift:** Default `v0.43.0` versus dokumentierter Produktrelease `0.4.0` ist ein unmittelbares Vertrauens- und Aktivierungsrisiko (`extensions/github-action/action.yml:36-43`, `CHANGELOG.md:64-95`).
- **Unvollständige Savings-Automation:** Die Compose-Kette existiert, automatische Abdeckung aller Calls ist deferred (`docs/savings/cap-readout.md:72-79`).
- **Real-World-Reife bleibt partiell:** Die dokumentierten Feldtests enthielten einen falschen Security-All-Clear sowie massive Monorepo-Kosten (`docs/real-world-report.md:9-21`). Fix-Gates sind positiv, ersetzen aber keine breitere Nutzungsvalidierung.

### Kommerzielle Schulden

- Kein ICP- und Buyer-Dokument.
- Keine problemorientierte Packaging-Entscheidung; stattdessen Feature-Katalog.
- Kein Preis, keine Preismetrik, keine Angebotsstufen.
- Keine Lead-Erfassung, Demo, Kontakt- oder Pilotstrecke.
- Keine Terms of Service, Privacy Policy für einen Hosted-Dienst, DPA, SLA oder kommerzielle Supportbedingungen.
- Kein belegter Marketplace-Eintrag für VS Code oder GitHub Action.
- Keine Adoption-, Activation-, Retention-, Conversion- oder Revenue-Scorecard; die vorhandene 100/100-Scorecard misst interne technische Bereiche und synthetische Szenarien, nicht Markttraktion (`docs/release-scorecard.md:9-24`, `docs/release-scorecard.md:27-49`).
- Kein Community-/Governance-Nachweis jenseits allgemeiner Contribution-Anleitung; Maintainer-Kapazität, externe Contributors und Bus-Factor sind UNKNOWN.

## Konkrete Belege mit Pfad:Zeile

| Aussage | Repo-Beleg |
|---|---|
| Klarer Kernnutzen: einmal indexieren, strukturelle Fragen lokal beantworten | `readme.md:13-17`, `readme.md:96-110` |
| Zwei benannte Nutzergruppen | `readme.md:112-115` |
| Kein Account, keine Telemetrie, keine Runtime-Netzaufrufe | `readme.md:117-117` |
| One-Line-Installation für Unix und Windows | `readme.md:41-60` |
| Setup für mehrere MCP-Clients | `readme.md:80-91` |
| 38 MCP-Tools | `docs/FEATURES.md:56-87` |
| Breite Multi-Surface-Distribution | `docs/FEATURES.md:90-115` |
| Landingpage-Angebot kostet USD 0 | `site/index.html:35-46` |
| Haupt-CTA ist Star/Install statt Buy/Contact | `site/index.html:583-590` |
| Apache-2.0 erlaubt kostenlose Weitergabe, Derivate und Unterlizenzierung | `LICENSE:66-71`, `LICENSE:89-128` |
| Release Assets werden cross-kompiliert und als GitHub Release hochgeladen | `.github/workflows/release.yml:135-186` |
| Homebrew/Scoop kann unkonfiguriert übersprungen werden | `.github/workflows/release.yml:187-205` |
| VS-Code-Erweiterung ist package-fähig | `extensions/vscode/package.json:108-116` |
| PR Action bietet optionales Merge-Gate und Outputs | `extensions/github-action/action.yml:54-68`, `extensions/github-action/action.yml:85-95` |
| GitHub-Action-Default nennt `v0.43.0` | `extensions/github-action/action.yml:36-43` |
| Dokumentierte Releases reichen aktuell bis `0.4.0` | `CHANGELOG.md:64-95` |
| Zwei externe Feldtests fanden gravierende Real-World-Lücken | `docs/real-world-report.md:9-21` |
| Reproduzierbare technische Verbesserungen nach Feldtests | `docs/real-world-report.md:23-40`, `docs/real-world-report.md:61-74` |
| Savings basiert auf internem Whole-File-Read-Vergleich | `docs/meter/metering.md:31-55` |
| Automatische Savings-Verkabelung aller Engine-Calls ist deferred | `docs/savings/cap-readout.md:72-79` |
| Technische Scorecard misst keine Markttraktion | `docs/release-scorecard.md:9-24`, `docs/release-scorecard.md:27-49` |

## UNKNOWN

Mangels Repository-Beleg sind folgende Punkte ausdrücklich **UNKNOWN**:

- Marktgröße, Marktwachstum, Konkurrenzposition und ersetztes Budget.
- GitHub Stars, Forks, Unique Visitors, Release-Downloads und Installer-Nutzung.
- Tatsächliche Veröffentlichung bzw. Nutzung in Homebrew, Scoop, VS Code Marketplace oder GitHub Marketplace.
- Daily/Weekly/Monthly Active Users, Aktivierungsrate und Time-to-First-Value auf Nutzerrechnern.
- Retention, Churn, wiederkehrende Nutzung und Zahl installierter Repositories pro Nutzer/Team.
- Anzahl Organisationen, produktiver CI-Installationen und echter Merge-Gate-Nutzung.
- Zahl externer Contributors, Community-Gesundheit und Maintainer-Bus-Factor.
- Kundeninterviews, ICP, Buyer, Budget Owner und Kaufprozess.
- Zahlungsbereitschaft, akzeptierte Preismetrik, Preiselastizität und Sales Cycle.
- Umsatz, MRR/ARR, Sponsoring, bezahlter Support, Piloten oder Design Partner.
- Kosten für Entwicklung, Support, Security Response, Hosting oder Vertrieb.
- Rechtliche Inhaberschaft von Marke/IP sowie Bereitschaft zu CLA oder Dual Licensing.
- Nachfrage nach Hosted SaaS versus strikt lokalem/self-hosted Betrieb.
- Ob die ausgewiesenen Token-/USD-Einsparungen bei realen Nutzerworkflows zu Budgeteinsparungen führen.
- Ob Nutzer primär Graph Queries, Security/Taint, PR Review, IDE, Agent Context oder Refactoring wertschätzen.

## Harte Empfehlung

**Empfehlung: Refactor.** Kein technischer Full Rewrite und kein weiterer horizontaler Feature-Ausbau. Gemeint ist ein harter **Produkt-, Packaging- und Distributions-Refactor** um einen einzigen validierbaren Team-Workflow.

### Direkte Begründung

Die Engine besitzt genügend Substanz; ein Rewrite würde vorhandene technische Assets, Trust-Gates und Releaseautomation vernichten, ohne den eigentlichen Engpass zu lösen. „Weiterbauen“ im bisherigen Muster wäre ebenfalls falsch: Das Repository zeigt bereits extreme Featurebreite, aber null belegte Monetarisierung. Der Engpass ist nicht fehlende Code-Intelligence, sondern fehlende Marktvalidierung, Wertzaun und Distribution.

Die repo-nächste kommerzielle Hypothese ist **lokal/self-hosted Team- und CI-Nutzung**, nicht Hosted SaaS: PR Review, Risk Output und Merge-Gate sind bereits vorhanden, während Zero-Egress und No-Accounts Teil des Markenkerns sind. Diese Hypothese ist noch nicht validiert und darf nicht als Marktbehauptung verstanden werden.

Konkret sollte der nächste Zyklus ausschließlich Folgendes liefern:

1. **Ein ICP und ein Hero-Workflow:** beispielsweise „Teams sichern riskante PRs in privaten Repositories mit lokalem Graph-Evidence-Gate“ – erst nach Interviews festlegen.
2. **Ein kaufbares Pilotangebot:** klarer Scope, Supportumfang, Preisexperiment und Erfolgskriterien; zunächst manuell statt Billing-Plattform.
3. **Ein OSS→Paid-Wertzaun:** freien lokalen Kern behalten; als Hypothesen für Paid nur teambezogene Fähigkeiten testen, etwa zentrale Policy-Konfiguration, organisationsweite Baselines/Reports, signierte Audit-Artefakte, SSO/RBAC oder SLA-Support. Welche davon zahlungswürdig ist, bleibt bis zur Validierung UNKNOWN.
4. **Distribution tatsächlich schließen:** Action-Version korrigieren, reale Action-/VS-Code-Koordinaten publizieren und dokumentieren, Homebrew/Scoop-Publishing nicht still als optionalen Dauerzustand belassen.
5. **Privacy-kompatible Business-Messung:** explizites Opt-in oder manuelle Pilotmetriken für Aktivierung, wiederholte Nutzung, Gate-Entscheidungen und realen ROI; keine heimliche Telemetrie.
6. **Stop-Regel:** keine neuen horizontalen Analyzer/Surfaces, bis mindestens ein repo-dokumentierter Design Partner den fokussierten Workflow wiederholt nutzt und entweder bezahlt oder eine konkrete Kaufzusage unter definierten Bedingungen abgibt.

Ohne diese Refokussierung bleibt `graphi` wahrscheinlich ein beeindruckendes, kostenloses Engineering-Projekt mit hohen Wartungskosten. Mit ihr besteht eine plausible – aber heute unbewiesene – Chance auf ein OSS-getriebenes, self-hosted Teamprodukt.
