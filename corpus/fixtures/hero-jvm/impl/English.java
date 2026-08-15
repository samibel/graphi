package impl;

import api.Greeter;

// English implements Greeter and delegates through the uniquely-named core,
// the shared callee the hero callers/callees/impact scenarios pivot on. core
// reads the prefix field (this.prefix) so a confirmed references edge exists.
public class English implements Greeter {
    private String prefix = "Hi ";
    public String salute(String name) { return core(name); }
    public String core(String name) { return this.prefix + name; }
}
