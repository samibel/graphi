# jvmgroundtruth testdata

Both files are REAL javap output, captured (never hand-written) — a parser
validated against a fiction proves nothing.

## `cart.javap.txt` — the legacy `-c -p` capture

Captured from:

```
tax/Rate.java   package tax; class Rate { int rate(); int scaled(Rate other){other.rate();} }
shop/Cart.java  package shop; class Cart { Rate stored; int checkout(Rate){r.rate()+stored.rate();}
                                            class Helper { int assist(Rate r){r.rate();} } }
```

Regenerate:

```
javac -g -d out $(find . -name '*.java')
javap -c -p -classpath out shop.Cart 'shop.Cart$Helper' tax.Rate > cart.javap.txt
```

It exercises: cross-package invokevirtual (checkout→rate, twice → one fact),
a SAME-CLASS call with NO owner prefix in the ref (scaled→rate, `// Method rate:()I`),
a nested class (Cart$Helper.assist→rate), a field access (getfield, ignored),
and external constructor calls (invokespecial java/lang/Object.<init>, CalleeFile "").

**It is deliberately kept WITHOUT `-s`.** It is the fixture that proves the
parser degrades legibly on a capture that carries no `descriptor:` lines: every
fact still parses at `ByName` — the pre-SW-172 key, unchanged — and the arity
and signature precisions DECLINE under `bytecode_no_descriptor_table` rather
than answering from an unwalked symbolic owner. `TestParseJavap_NoDescriptors‑
DegradesLegibly` is that proof, so do not "modernise" this file.

## `overloads.javap.txt` — the `-c -p -s` capture (SW-172)

`-s` prints a `descriptor:` line under every declared method. That is what
makes the signature precisions possible at all: it is the only place javap
states each DECLARED method's exact signature, which the JVM method-resolution
walk (JVMS 5.4.3.3) needs to map a symbolic owner to the declaring class.

Captured from:

```java
// a/Thing.java
package a;
public class Thing {}

// a/Base.java
package a;
public class Base {
    public int seed() { return 3; }
}

// a/Rate.java
package a;
public class Rate<T extends Number> extends Base {
    public int apply(int x) { return x; }
    public int apply(int x, int y) { return x + y; }
    public int apply(Thing t) { return 1; }
    public int apply(Thing[] ts) { return ts.length; }
    public int tag(String s) { return s.length(); }
}

// a/App.java
package a;
public class App {
    public int run(Rate<Integer> r, Thing t, Thing[] ts) {
        return r.apply(1) + r.apply(1, 2) + r.apply(t) + r.apply(ts) + r.seed();
    }
    public int lam(Base b) {
        java.util.function.IntSupplier s = () -> b.seed();
        return s.getAsInt();
    }
}
```

Regenerate:

```
javac -g -d out a/*.java
javap -c -p -s -classpath out a.App a.Base a.Rate a.Thing > overloads.javap.txt
```

Each element earns its place:

| what it pins | why it is in the fixture |
|---|---|
| four `apply` overloads called from ONE method | the by-name key collapses all four to ONE fact — the blindness SW-172 removes, shown rather than asserted |
| `apply(int)` vs `apply(int,int)` | the arity precision must separate them |
| `apply(Thing)` vs `apply(Thing[])` | the signature precision must separate them at EQUAL arity |
| `r.seed()`, declared on `Base`, invoked through `Rate` | javac writes the SYMBOLIC owner `a/Rate.seed`; the walk must land on `a/Base.java`, or every inherited call becomes a false counterexample |
| `class a.Rate<T extends Number> extends a.Base` | the header contains `extends` TWICE; reading the generic bound as the superclass would send the walk up an imaginary chain |
| `tag(String)` | an external parameter type: the signature must ABSTAIN by name, not invent `java.lang` |
| the lambda in `lam` | javac compiles it to `lambda$lam$0`; the caller must normalize back to `lam`, which is where graphi attributes it |
