fun hello(name: String): String = "hello $name"

interface Greeter {
    fun greet(name: String): String
}

class EnglishGreeter : Greeter {
    override fun greet(name: String): String = hello(name)
}

class SpanishGreeter : Greeter {
    override fun greet(name: String): String = "hola $name"
}

fun chainA(name: String): String = chainB(name)
fun chainB(name: String): String = chainC(name)
fun chainC(name: String): String = hello(name)

fun source(): String = userInput()
fun userInput(): String = "user"
fun sink(v: String) {}

fun taintFlow() {
    sink(source())
}

fun clonePairA(x: Int, y: Int): Int = x + y + 1
fun clonePairB(x: Int, y: Int): Int = x + y + 1
