# Security- & Privacy-Review

## Kurzurteil

graphi hat eine überdurchschnittlich gute **Netzwerk-Minimierung**: TCP-Listener werden auf Loopback beschränkt, der Daemon nutzt einen Unix-Socket mit `0600`, Fehler der REST-Oberfläche werden grundsätzlich bereinigt, und die VS-Code-Webview ist sauber mit CSP und validierten Nachrichten abgeschottet (`surfaces/guard/guard.go:25-83`, `surfaces/daemon/daemon.go:141-159`, `surfaces/http/server.go:163-178`, `extensions/vscode/src/webview/graphWebview.ts:152-167`, `extensions/vscode/src/webview/graphWebview.ts:180-209`).

Trotzdem ist die aktuelle Sicherheitsstory nicht release-reif für sensible/private Repositories. Der Kernfehler ist konzeptionell: **„Loopback-only“ wird wie Authentisierung behandelt, ist aber keine.** Der REST-Server registriert sämtliche Datenrouten ohne Auth-Middleware und prüft weder Bearer-Token noch `Origin`/`Host`; der VS-Code-Client bietet gleichzeitig einen Auth-Token an, den der Server vollständig ignoriert (`surfaces/http/server.go:181-212`, `surfaces/http/server.go:298-312`, `extensions/vscode/src/extension.ts:58-69`, `extensions/vscode/src/graphiClient.ts:138-147`). Damit sind Repository-Metadaten für jeden lokalen Prozess und bei DNS-Rebinding potenziell für eine fremde Website lesbar. Die separat vorhandene MCP-HTTP-Oberfläche ist ebenfalls unauthentisiert und reicht bis zu mutierenden MCP-Tools; Memory-Export kann sogar einen frei gewählten lokalen Pfad anlegen/überschreiben (`surfaces/mcp/http.go:16-49`, `surfaces/mcp/mcp.go:230-260`, `surfaces/mcp/mcp.go:345-368`, `surfaces/client/direct.go:658-677`).

Auch Privacy-at-rest ist schwach: Agent-Memory wird als Klartext-JSONL mit Modus `0644` angelegt; erkannte Secrets werden nur markiert und trotzdem vollständig gespeichert (`engine/memory/memory.go:101-117`, `engine/memory/memory.go:239-297`, `engine/memory/memory.go:300-317`, `engine/memory/memory.go:406-439`).

## Score 0–10

**5,0 / 10**

- Netzwerk-/Egress-Design: **8/10** — gute zentrale Loopback-Guards und echte CI-Isolation (`surfaces/guard/guard.go:25-83`, `.github/workflows/privacy-audit.yml:21-55`).
- Auth/API-Sicherheit: **3/10** — keine serverseitige Authentisierung, kein Host-/Origin-Schutz, inkonsistenter Token-Vertrag (`surfaces/http/server.go:181-212`, `surfaces/http/server.go:298-312`, `extensions/vscode/src/graphiClient.ts:138-156`).
- Secrets/Privacy-at-rest: **3/10** — Memory im Klartext und potenziell world-readable; Secret-Erkennung ist nur ein Label (`engine/memory/memory.go:101-117`, `engine/memory/memory.go:239-317`).
- Lokale Angriffsflächen: **5/10** — Unix-Socket gut, HTTP/MCP-HTTP und unbeschränkte JSON-Bodies schlecht (`surfaces/daemon/daemon.go:141-159`, `surfaces/http/server.go:698-745`).
- Supply Chain: **5/10** — Lockfiles/Checksummen vorhanden, aber normale Workflows nutzen Floating Tags und Release-Prüfsumme kommt vom selben Ursprung wie das Binary (`web/package-lock.json:1`, `extensions/vscode/package-lock.json:1`, `.github/workflows/release.yml:41-42`, `install.sh:117-145`).

## Größte Stärken

1. **Zentraler Loopback-/Egress-Guard.** Nicht-Loopback-Binds werden vor dem Öffnen des Sockets abgewiesen; der bereitgestellte Dialer verweigert nicht-lokale Ziele (`surfaces/guard/guard.go:25-83`). Die REST-Oberfläche validiert ebenfalls explizit den Bind-Host (`surfaces/http/server.go:251-295`).

2. **Daemon-Socket mit restriktiven Rechten.** Der Daemon nutzt ausschließlich Unix Domain Sockets und setzt die Socket-Datei auf `0600`; unmittelbare Symlinks und world-writable Parent-Verzeichnisse werden abgewiesen (`surfaces/daemon/daemon.go:141-159`, `surfaces/daemon/daemon.go:384-412`).

3. **Fehlerbereinigung an der REST-Grenze.** Unerwartete interne Fehler werden als generisches `500/internal error` ausgegeben statt Pfade und Engine-Details zu leaken (`surfaces/http/server.go:163-178`).

4. **Begrenzung mehrerer Parser-/Transport-Eingaben.** MCP-stdio und MCP-HTTP haben 16-MiB-Grenzen; Compound/AST/Clone-REST-Bodies werden auf 1 MiB begrenzt (`surfaces/mcp/mcp.go:68-98`, `surfaces/mcp/http.go:12-40`, `surfaces/http/server.go:390-452`).

5. **Solide Webview- und Markdown-Härtung.** Die VS-Code-Webview nutzt Nonce-CSP, beschränkt lokale Ressourcen und validiert eingehende Nachrichten; Wiki-Markdown deaktiviert Raw HTML und `dangerouslySetInnerHTML` (`extensions/vscode/src/webview/graphWebview.ts:66-80`, `extensions/vscode/src/webview/graphWebview.ts:152-167`, `extensions/vscode/src/webview/graphWebview.ts:180-209`, `web/src/WikiMarkdown.tsx:24-39`).

6. **Token nicht in argv/URL.** GitHub-Tokens kommen aus der Umgebung und werden ausschließlich im Authorization-Header gesetzt (`surfaces/forge/forge.go:171-188`, `surfaces/forge/forge.go:291-305`, `engine/review/githubhost.go:95-129`, `engine/review/githubhost.go:203-225`).

7. **Secret-Hygiene im Repository.** `.gitignore` deckt übliche Secret-Dateien ab (`.gitignore:20-28`). Eine statische Suche im Review fand nur absichtliche Testwerte in `engine/memory/provenance_test.go:120-149`, keine belegbar echten Zugangsdaten. Das ist eine Momentaufnahme, kein Ersatz für Secret-Scanning in CI.

## Größte Schwächen

1. **Keine Authentisierung oder Autorisierung am HTTP-Server.** Alle Query-, Search-, Analyze-, PR-, Memory-, Distill- und Skillgen-Routen hängen direkt am Mux; `schemaGuard` prüft nur eine optionale Versionsnummer (`surfaces/http/server.go:181-212`, `surfaces/http/server.go:298-312`). Es gibt keine Rollen, Scopes oder Trennung zwischen öffentlichen Health-Routen und repository-sensitiven Daten.

2. **Der VS-Code-Token ist Security-Theater.** Die Extension speichert einen Token sicher und sendet ihn als Bearer-Header, aber die Go-Serverseite liest `Authorization` nirgendwo in ihrer Request-Pipeline (`extensions/vscode/src/connection.ts:79-94`, `extensions/vscode/src/graphiClient.ts:138-156`, `surfaces/http/server.go:181-212`). Nutzer können daher glauben, der Daemon sei geschützt, obwohl jeder lokale Client ohne Token dieselben Antworten erhält.

3. **Kein DNS-Rebinding-/Browser-Schutz.** Der Server validiert beim Start die Bind-Adresse, aber pro Request weder `r.Host` noch `Origin`; `Handler()` gibt den nackten Mux zurück (`surfaces/http/server.go:181-212`, `surfaces/http/server.go:270-295`). Ohne zulässige Hostliste und Origin-Policy kann eine Domain, die auf `127.0.0.1` reboundet, unter derselben Browser-Origin auf private Graphdaten zugreifen. Das Fehlen von CORS-Headern allein behebt DNS-Rebinding nicht.

4. **MCP-HTTP ist unauthentisiert und kann mutierende Tools dispatchen.** Der Handler akzeptiert jedes POST und ruft direkt `s.handle` auf; `tools/call` umfasst Refactor/Undo, Memory und potenziell PR-Publishing (`surfaces/mcp/http.go:16-49`, `surfaces/mcp/mcp.go:101-131`, `surfaces/mcp/mcp.go:230-260`, `surfaces/mcp/mcp.go:617-646`). Der aktuelle CLI-Einstieg startet nur MCP-stdio (`cmd/graphi/main.go:800-813`), aber die exportierte HTTP-API ist eine gefährliche latente/embedded Angriffsfläche.

5. **Arbitrary file write beim Memory-Export.** `export_to_path` wird unverändert bis `os.Create` weitergereicht; es gibt keine Root-Policy, Allowlist, `O_EXCL`, Symlink-Prüfung oder sichere Dateiberechtigung (`surfaces/mcp/mcp.go:345-368`, `surfaces/client/direct.go:658-677`). Sobald Memory über einen nicht vertrauenswürdigen Transport verdrahtet wird, kann ein Angreifer jede durch den Prozess beschreibbare Datei truncaten und mit JSONL-Inhalt überschreiben.

6. **Unbegrenzte REST-JSON-Bodies.** `/memory`, `/distill` und `/skillgen` decodieren direkt aus `r.Body`, ohne `http.MaxBytesReader`, `LimitReader` oder globale Request-Grenze (`surfaces/http/server.go:698-745`). Zusammen mit fehlendem `ReadTimeout`/`IdleTimeout` — der Server setzt nur `ReadHeaderTimeout` — ermöglicht das lokalen Memory-/Connection-DoS (`surfaces/http/server.go:255-267`).

7. **Memory ist unverschlüsselter Klartext mit zu weitem Modus.** `OpenFile(..., 0644)` kann Memory-Inhalte für andere lokale Nutzer lesbar machen, abhängig von umask und Verzeichnisrechten (`engine/memory/memory.go:101-117`). Payload, Source und Evidence landen vollständig im Journal (`engine/memory/memory.go:276-297`, `engine/memory/memory.go:406-439`). Ein Secret-Treffer setzt nur `secret_suspected=true`; die Speicherung wird nicht abgewiesen, redigiert oder verschlüsselt (`engine/memory/memory.go:251-297`, `engine/memory/memory.go:300-317`).

8. **`.gitignore` wird nicht standardmäßig respektiert.** Das Verhalten ist explizit opt-in über `GRAPHI_RESPECT_GITIGNORE` (`engine/ingest/ignore.go:17-33`, `engine/ingest/ignore.go:123-138`). Dadurch können ignorierte lokale Konfigurations-/Fixture-Dateien mit sensitiven Namen oder Inhalten in den persistenten Graphen gelangen, soweit ein Parser für ihren Dateityp registriert ist. Die Default-Denylist betrifft primär Build-/Dependency-Verzeichnisse, nicht beliebige geheime Dateien (`engine/ingest/ignore.go:36-46`).

9. **Outbound-GitHub-Boundaries validieren Scheme/Host nicht.** `GITHUB_API_URL` fließt als frei konfigurierbare Base URL ein; der Konstruktor trimmt nur `/` und der Token wird danach an diese URL gesendet (`surfaces/forge/forge.go:159-188`, `surfaces/forge/forge.go:291-305`, `engine/review/githubhost.go:76-92`, `engine/review/githubhost.go:203-225`). Eine fehlkonfigurierte oder manipulierte Umgebung kann den Bearer-Token an HTTP oder einen fremden Host schicken. Das widerspricht der Behauptung, dies sei kontrolliert „der eine Host“ (`engine/review/githubhost.go:31-45`).

10. **Supply-Chain-Gates sind inkonsistent.** Die ausgelieferte Composite Action pinnt ihre internen Actions auf volle SHAs (`extensions/github-action/action.yml:103-109`), aber die Repository-Workflows nutzen vielfach mutable Major-Tags, darunter gerade der Releasepfad (`.github/workflows/release.yml:41-42`, `.github/workflows/release.yml:148-164`). Der Installer prüft SHA-256, lädt Binary und Prüfsummen aber aus demselben GitHub-Release-Ursprung; es fehlt eine unabhängige Signatur/Provenienzprüfung (`install.sh:117-145`).

11. **Daemon-Service-Generator escaped untrusted options nicht.** XML-Werte werden roh in die launchd-Plist eingebettet und systemd-Argumente mit Leerzeichen verbunden (`surfaces/daemon/service.go:40-60`, `surfaces/daemon/service.go:64-79`). Ein Label/Pfad/Argument mit XML- oder systemd-spezifischen Zeichen kann die Unit-Struktur verändern. Das ist primär eine lokale Operator-/Konfigurationsgrenze, aber vermeidbar.

## Kritische Blocker

### Blocker 1 — Fehlende Authentisierung plus fehlender Host-/Origin-Schutz

Vor jeder breiteren Nutzung muss der HTTP-Server repository-sensitive Routen mit einem pro Prozess zufällig erzeugten Bearer-Token schützen, Requests mit unerwartetem `Host` verwerfen und Browser-Origins default-deny behandeln. Der aktuelle Token-Client ohne Serverprüfung ist irreführender als gar keine Token-Option (`extensions/vscode/src/extension.ts:58-69`, `extensions/vscode/src/graphiClient.ts:138-156`, `surfaces/http/server.go:181-212`).

### Blocker 2 — MCP-HTTP darf ohne Capability-/Auth-Grenze keine Mutationen exponieren

`HTTPHandler()` braucht zwingend Auth, Host-/Origin-Prüfung, eine read-only Default-Capability und explizite opt-in Scopes für `refactor`, `undo`, `memory store/forget/export` und `pr_comment publish` (`surfaces/mcp/http.go:16-49`, `surfaces/mcp/mcp.go:230-260`). Bis dahin sollte MCP-HTTP nicht als öffentliche/exportierte Produktionsoberfläche gelten.

### Blocker 3 — Arbitrary file write in Memory-Export

Der Export muss entweder nur Bytes zurückgeben oder ausschließlich in ein explizit konfiguriertes Exportverzeichnis schreiben, nach `filepath.Abs`/`EvalSymlinks`/`Rel`-Containment und mit `0600`; das heutige `os.Create(req.ExportToPath)` ist nicht vertretbar (`surfaces/client/direct.go:658-677`).

### Blocker 4 — Privacy-at-rest

Persistenter Agent-Memory darf nicht mit `0644` und ungefilterten Secret-Payloads geschrieben werden. Mindeststandard: `0600`, privates Parent-Verzeichnis `0700`, sichere Migration bestehender Dateien, Secret-Policy „reject/redact/explicit override“, dokumentierte Retention und optional OS-Keychain-/envelope-basierte Verschlüsselung (`engine/memory/memory.go:101-117`, `engine/memory/memory.go:239-317`).

## Technische Schulden

- Zwei konkurrierende Loopback-Implementierungen (`surfaces/http/server.go:270-295` und `surfaces/guard/guard.go:33-55`) können semantisch driften; HTTP sollte ausschließlich den zentralen Guard verwenden.
- Der HTTP-Kommentar nennt die Oberfläche „read-only“, obwohl POST-Routen für Memory/Distill/Skillgen registriert sind und Memory `store`, `forget` sowie Dateiexport unterstützt (`surfaces/http/server.go:1-12`, `surfaces/http/server.go:201-203`, `surfaces/client/direct.go:600-680`). Das ist eine falsche Trust-Boundary-Dokumentation.
- Body-Limits sind pro Handler uneinheitlich: 1 MiB, 16 MiB oder gar kein Limit (`surfaces/http/server.go:390-452`, `surfaces/mcp/http.go:12-40`, `surfaces/http/server.go:698-745`).
- Der MCP-HTTP-LimitReader liest höchstens 16 MiB, weist einen größeren Request aber nicht anhand `Content-Length` oder eines `max+1`-Reads explizit als „zu groß“ zurück (`surfaces/mcp/http.go:12-40`).
- Der Daemon gibt rohe Handlerfehler an lokale Clients zurück, anders als die REST-Sanitization (`surfaces/daemon/daemon.go:241-338`, `surfaces/http/server.go:163-178`). Darin können absolute Pfade und interne Details stecken.
- `resolvePath` beim Editieren prüft Containment nur lexikalisch und löst Symlinks nicht auf (`engine/edit/edit.go:361-381`). Der finale atomare Rename ersetzt zwar typischerweise den Symlink im Repo statt dessen externes Ziel, aber das vorherige `os.ReadFile` folgt Symlinks (`engine/edit/edit.go:266-274`); damit kann externer Inhalt in Snapshots/Fehlerpfade einfließen. Eine konsistente `Lstat`/`EvalSymlinks`-Policy fehlt.
- `NewHTTP` behauptet „No proxy, no redirects“, konfiguriert aber lediglich einen Standard-`http.Client`; weder `Transport.Proxy=nil` noch `CheckRedirect` ist explizit gesetzt (`surfaces/client/http_client.go:67-86`). Die Loopback-Validierung gilt nur für die Start-URL (`surfaces/client/http_client.go:89-100`).
- Normale CI-Actions sind nicht SHA-gepinnt, obwohl das Projekt den strengeren Standard bereits für seine ausgelieferte Action implementiert (`extensions/github-action/action.yml:103-109`, `.github/workflows/privacy-audit.yml:28-29`).
- Das Privacy-Audit prüft Egress/Telemetry gut, behandelt „No accounts required“ aber als deklarativen PASS und deckt weder lokale Auth, Dateirechte, Datenretention noch Secret-Persistenz ab (`internal/audit/audit.go:126-146`, `internal/audit/audit.go:297-300`).

## Konkrete Codebeispiele

### 1. Zentrale HTTP-Sicherheitsmiddleware

Problemstelle: Der Mux wird direkt zurückgegeben (`surfaces/http/server.go:181-212`). Zielbild:

```go
func (s *Server) Handler() http.Handler {
    mux := s.routes()
    return requireHost(
        []string{"127.0.0.1", "localhost", "[::1]"},
        requireBearer(s.token, rejectCrossOrigin(mux)),
    )
}
```

`/healthz` kann separat tokenlos bleiben; alle graph-, wiki-, event- und MCP-Datenrouten müssen geschützt werden. Tokenvergleich mit `subtle.ConstantTimeCompare`; Token zufällig, pro Start und nicht in argv. Der Server muss dem gestarteten Browser/Extension-Handshake einen sicheren Übergabekanal geben.

### 2. Globales Body-Limit und Server-Timeouts

Problemstellen: unbegrenztes Decode (`surfaces/http/server.go:698-745`) und nur `ReadHeaderTimeout` (`surfaces/http/server.go:255-267`). Zielbild:

```go
const maxJSONBody = 1 << 20

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
    r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
    dec := json.NewDecoder(r.Body)
    dec.DisallowUnknownFields()
    return dec.Decode(dst)
}

srv := &http.Server{
    Handler:           securedHandler,
    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:       10 * time.Second,
    WriteTimeout:      30 * time.Second,
    IdleTimeout:       60 * time.Second,
}
```

Für SSE muss `WriteTimeout` bewusst separat behandelt werden, statt deshalb alle übrigen Routen ohne Timeouts zu lassen.

### 3. Export ohne frei wählbaren Dateipfad

Problemstelle: `os.Create(req.ExportToPath)` (`surfaces/client/direct.go:658-677`). Sicherste API:

```go
case "export":
    var buf bytes.Buffer
    if err := d.memoryStore.ExportMemory(ctx, q, &buf); err != nil {
        return nil, err
    }
    return marshalJSON(MemoryResponse{Export: buf.String()})
```

Die CLI darf diese Bytes anschließend als explizite lokale Operatoraktion in eine Datei schreiben. So hat ein Remote-/MCP-Transport niemals eine generische Filesystem-Write-Primitive.

### 4. Sichere Memory-Datei

Problemstelle: `0644` (`engine/memory/memory.go:101-117`). Mindestfix:

```go
if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { /* ... */ }
f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
if err == nil {
    err = f.Chmod(0o600) // migriert auch bestehende zu weite Modi
}
```

Zusätzlich muss `SecretSuspect` vor `appendEntry` eine Policy auslösen; die aktuelle Reihenfolge persistiert den Klartext trotz Erkennung (`engine/memory/memory.go:251-297`).

### 5. GitHub API Base validieren

Problemstellen: freie Base URL (`surfaces/forge/forge.go:159-168`, `engine/review/githubhost.go:76-92`). Zielbild:

```go
u, err := url.Parse(base)
if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" {
    return nil, errors.New("GitHub API base must be a clean HTTPS origin")
}
```

Enterprise-Hosts sollten explizit zugelassen werden; Redirects auf einen anderen Origin müssen blockiert werden, bevor ein Authorization-Header weitergereicht werden kann.

### 6. Workflow-Pinning und Release-Provenienz

Alle `uses: actions/...@vN` im Release-/Auditpfad auf Full SHA umstellen, wie bereits in `extensions/github-action/action.yml:103-109`. Zusätzlich Sigstore/Cosign oder GitHub Artifact Attestations erzeugen und im Installer verifizieren; SHA-256 aus demselben kompromittierbaren Release ist nur Integritäts-, kein Herkunftsnachweis (`install.sh:117-145`).

## UNKNOWN

- **UNKNOWN:** Es gibt keine vorliegende Threat-Model-Datei, die festlegt, ob andere lokale Benutzer/Prozesse, Browser-DNS-Rebinding und kompromittierte MCP-Clients explizit im Angreifermodell liegen. Die Implementierung exponiert Loopback und Unix-Socket; daher müssen diese Angreifer bis zur gegenteiligen dokumentierten Entscheidung als relevant gelten (`surfaces/http/server.go:251-295`, `surfaces/daemon/daemon.go:141-159`).
- **UNKNOWN:** Die exportierte MCP-HTTP-API ist im aktuellen `cmd/graphi` nicht verdrahtet; nur stdio wird gestartet (`cmd/graphi/main.go:800-813`). Unklar ist, ob ein anderer Consumer sie produktiv einbettet. Ihr Sicherheitsvertrag muss trotzdem vor Freigabe gehärtet werden (`surfaces/mcp/http.go:16-49`).
- **UNKNOWN:** Die normalen `graphi http`- und Zero-Config-Pfade verdrahten derzeit Memory/Edit nicht (`cmd/graphi/main.go:1237-1248`, `cmd/graphi/zeroconfig.go:102-113`). Unklar ist, ob geplante Builds/Integrationen `WithMemory` oder `WithEditor` an denselben unauthentisierten Server hängen. Die Routen existieren bereits (`surfaces/http/server.go:201-203`).
- **UNKNOWN:** Es wurde kein dynamischer DNS-Rebinding-PoC gegen einen laufenden Build durchgeführt. Der statische Befund — Bind-Prüfung ohne per-request `Host`-/`Origin`-Prüfung — ist eindeutig (`surfaces/http/server.go:181-212`, `surfaces/http/server.go:270-295`).
- **UNKNOWN:** Keine belastbare Aussage zu bekannten CVEs der Go-/npm-Abhängigkeiten ohne einen aktuellen `govulncheck`-/OSV-/npm-audit-Lauf gegen externe Advisory-Daten. Lockfiles und Go-Prüfsummen fixieren Artefakte, beweisen aber keine Vulnerability-Freiheit (`go.mod:5-51`, `web/package-lock.json:1`, `extensions/vscode/package-lock.json:1`).
- **UNKNOWN:** At-rest-Verschlüsselung der Graph-/Meta-SQLite-Daten ist nicht erkennbar; `ingest-meta.db` wird als normale SQLite-Datei geöffnet (`engine/ingest/ingest.go:269-286`). Ob Graphdaten personenbezogene oder vertrauliche Source-Snippets enthalten, hängt von Parser/Modell und Repository-Inhalt ab; eine Datenklassifikation/Retention-Policy fehlt im geprüften Security-/Privacy-Dokument (`docs/setup-privacy.md:42-63`).
- **UNKNOWN:** Das Review hat keinen vollständigen Build mit `graphi_broad` fuzz-/sanitizer-getestet. Die eigene Security Policy benennt für den CGo/Tree-sitter-Broad-Build ein Restrisiko bei untrusted Source (`SECURITY.md:32-40`).
- **UNKNOWN:** Keine Belege für branch protection, verpflichtende Reviews, Dependabot/Renovate, GitHub secret scanning oder Artifact Attestations, da diese teilweise Repository-Settings außerhalb des Codes sind.

## Harte Empfehlung

**Teil-Rewrite der Surface-Security, danach Weiterbauen. Kein Full Rewrite des Engines.**

Direkte Begründung: Die Engine- und Parserarchitektur zeigt sinnvolle Security-Grundlagen — zentrale Egress-Grenze, ressourcenbegrenzte Parserpfade, atomare Writes, sanitizierte REST-Fehler und gehärtete Webview (`surfaces/guard/guard.go:25-83`, `surfaces/http/server.go:163-178`, `engine/edit/write.go:9-56`, `extensions/vscode/src/webview/graphWebview.ts:180-209`). Diese Arbeit wegzuwerfen wäre falsch.

Die Transport-/Trust-Boundary-Schicht braucht jedoch eine gezielte Neufassung: ein gemeinsames Security-Envelope für HTTP, SSE und MCP-HTTP mit Authentisierung, Host-/Origin-Prüfung, Capability-Scopes, globalen Limits und sicheren Defaults; eine getrennte lokale CLI-Schreibschicht für Dateiexporte; sowie eine klare Privacy-at-rest-Policy. Diese Punkte lassen sich nicht seriös durch einzelne verstreute `if`-Checks reparieren, weil der heutige nackte Mux und die gemeinsame `client.Client`-Schnittstelle Read- und Write-Capabilities vermischen (`surfaces/http/server.go:181-212`, `surfaces/client/client.go:290-356`).

**Go/No-Go:** Weiterentwicklung intern: **Go**, aber nur parallel zu einem priorisierten Security-Refactor. Veröffentlichung als „sicher für private/sensible Repositories“ oder Aktivierung von MCP-HTTP/Memory über HTTP: **No-Go**, bis alle vier Blocker geschlossen und mit negativen Integrationstests belegt sind.
