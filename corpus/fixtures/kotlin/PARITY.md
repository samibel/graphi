# Kotlin fixture parity

| Analyzer Path | Status | Notes |
|--------------|--------|-------|
| symbol | Partial | functions and classes |
| reference | Partial | local refs and interface implementations |
| call graph | Partial | multi-hop chain |
| interface/protocol | Partial | `Greeter` interface |
| taint | Partial | synthetic source→sink flow |
| clone | Partial | near-duplicate pair |
