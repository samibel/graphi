package app;

import impl.English;

// Service calls English.core through declared-typed receivers from two
// methods, so the binder confirms the cross-package calls edges.
public class Service {
    private English english;
    public String serve(English g, String who) { return g.core(who); }
    public String twice(English g) { return g.core("a") + g.salute("b"); }
}
