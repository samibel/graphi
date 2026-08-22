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

## `refreturns.javap.txt` — the `-c -p -s` capture that BREAKS the parser (SW-172 round 1)

Added because two truth-losing parser defects were invisible to every other
fixture here: `overloads.javap.txt` and `cart.javap.txt` between them contain
**zero** reference-returning invoke lines and **zero** interface refs
(`grep -c "// Method .*)L.*;$"` → 0, `grep -c "// InterfaceMethod "` → 0), and
both live-JDK fixtures declared only `int`/`void` methods. The gate was green
because the corpus avoided the constructs that broke it.

Captured from:

```java
// a/Base.java
package a;
public class Base { public Base self() { return this; } }

// a/Derived.java
package a;
public class Derived extends Base {
    @Override public Derived self() { return this; }
    public int tag() { return 1; }
}

// a/HasSeed.java
package a;
public interface HasSeed { int seed(); static int base() { return 5; } }

// a/Impl.java
package a;
public class Impl implements HasSeed { public int seed() { return 3; } }

// a/App.java
package a;
public class App {
    public int run(Derived d) { return d.self().tag(); }
    public int viaIface(HasSeed h) { return h.seed() + HasSeed.base(); }
    public int local(final Derived d) {
        class L { int go() { return d.tag(); } }
        return new L().go();
    }
}
```

Regenerate:

```
javac -g -d out a/*.java
javap -c -p -s -classpath out a.App 'a.App$1L' a.Base a.Derived a.HasSeed a.Impl > refreturns.javap.txt
```

Each element earns its place:

| what it pins | why it is in the fixture |
|---|---|
| `// Method a/Derived.self:()La/Derived;` | an invoke line whose callee returns a REFERENCE type ends with `;` and contains `(`, so `parseMethodHeader` read it as a constructor header — dropping the invoke fact AND re-parenting every later invoke in the method onto `<init>` |
| `// InterfaceMethod a/HasSeed.seed:()I` (invokeinterface) | `parseMethodRef` matched only `// Method `, so every call through an interface-typed receiver was MISSING from the truth set |
| `// InterfaceMethod a/HasSeed.base:()I` (invokestatic) | javac uses the same marker for an interface static call |
| the covariant override `Derived.self` | javac also emits the `()La/Base;` BRIDGE, so the declared-method table holds two same-name descriptors |
| the local class `a/App$1L` | the bytecode CALLER is `a/App$1L.go`, while graphi attributes the call to the enclosing `local` — the two sides cannot be aligned on the caller, and the fact must abstain rather than accuse |

**Do not "simplify" the return types in this file.** The primitive/void-only
shape of the other two fixtures is exactly what hid these defects.
