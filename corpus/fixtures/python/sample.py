def hello(name: str) -> str:
    return "hello " + name

class Greeter:
    def greet(self, name: str) -> str: ...

class EnglishGreeter(Greeter):
    def greet(self, name: str) -> str:
        return hello(name)

class SpanishGreeter(Greeter):
    def greet(self, name: str) -> str:
        return "hola " + name

def chain_a(name: str) -> str: return chain_b(name)
def chain_b(name: str) -> str: return chain_c(name)
def chain_c(name: str) -> str: return hello(name)

def source() -> str: return user_input()
def user_input() -> str: return "user"
def sink(v: str) -> None: pass

def taint_flow() -> None:
    sink(source())

def clone_pair_a(x: int, y: int) -> int: return x + y + 1
def clone_pair_b(x: int, y: int) -> int: return x + y + 1
