def hello(name)
  "hello #{name}"
end

class Greeter
  def greet(name)
    raise NotImplementedError
  end
end

class EnglishGreeter < Greeter
  def greet(name)
    hello(name)
  end
end

class SpanishGreeter < Greeter
  def greet(name)
    "hola #{name}"
  end
end

def chain_a(name); chain_b(name); end
def chain_b(name); chain_c(name); end
def chain_c(name); hello(name); end

def source; user_input; end
def user_input; "user"; end
def sink(v); end

def taint_flow
  sink(source)
end

def clone_pair_a(x, y); x + y + 1; end
def clone_pair_b(x, y); x + y + 1; end
