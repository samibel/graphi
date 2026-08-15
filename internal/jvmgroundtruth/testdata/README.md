# jvmgroundtruth testdata

`cart.javap.txt` is REAL `javap -c -p` output, captured (not hand-written) from:

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
