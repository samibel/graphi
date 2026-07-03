# C# fixture parity

| Analyzer Path | Status | Notes |
|--------------|--------|-------|
| symbol | Partial | classes, static methods |
| reference | Partial | local refs and interface implementations |
| call graph | Partial | multi-hop chain |
| interface/protocol | Partial | `IGreeter` interface |
| taint | Partial | synthetic source→sink flow |
| clone | Partial | near-duplicate pair |
