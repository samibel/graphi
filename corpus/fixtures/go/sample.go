package fixture

// Hello returns a greeting.
func Hello(name string) string {
	return "hello " + name
}

// Greeter is implemented by concrete types.
type Greeter interface {
	Greet(name string) string
}

type EnglishGreeter struct{}

func (EnglishGreeter) Greet(name string) string { return Hello(name) }

type SpanishGreeter struct{}

func (SpanishGreeter) Greet(name string) string { return "hola " + name }

// CallChain demonstrates a multi-hop call chain.
func ChainA(name string) string { return ChainB(name) }
func ChainB(name string) string { return ChainC(name) }
func ChainC(name string) string { return Hello(name) }

// Source is a synthetic taint source.
func Source() string { return userInput() }

func userInput() string { return "user" }

// Sink is a synthetic taint sink.
func Sink(v string) { _ = v }

func TaintFlow() {
	Sink(Source())
}

// ClonePairA and ClonePairB are intentionally similar for clone detection.
func ClonePairA(x, y int) int { return x + y + 1 }
func ClonePairB(x, y int) int { return x + y + 1 }
