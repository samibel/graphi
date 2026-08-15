package other;

import api.Greeter;

// Spanish also implements Greeter but NEVER calls core — the negative anchor
// for the callers-of-core scenario.
public class Spanish implements Greeter {
    public String salute(String name) { return "Hola " + name; }
}
