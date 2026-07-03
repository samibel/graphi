fn hello(name: &str) -> String {
    format!("hello {}", name)
}

trait Greeter {
    fn greet(&self, name: &str) -> String;
}

struct EnglishGreeter;
impl Greeter for EnglishGreeter {
    fn greet(&self, name: &str) -> String { hello(name) }
}

struct SpanishGreeter;
impl Greeter for SpanishGreeter {
    fn greet(&self, name: &str) -> String { format!("hola {}", name) }
}

fn chain_a(name: &str) -> String { chain_b(name) }
fn chain_b(name: &str) -> String { chain_c(name) }
fn chain_c(name: &str) -> String { hello(name) }

fn source() -> String { user_input() }
fn user_input() -> String { String::from("user") }
fn sink(v: &str) { let _ = v; }

fn taint_flow() {
    sink(&source());
}

fn clone_pair_a(x: i32, y: i32) -> i32 { x + y + 1 }
fn clone_pair_b(x: i32, y: i32) -> i32 { x + y + 1 }
