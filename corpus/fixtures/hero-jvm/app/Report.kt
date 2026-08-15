package app

import impl.English

// Report is the Kotlin caller: a declared-typed local binds English.core,
// exercising the cross-language confirmed edge.
class Report {
    fun run(g: English): String {
        val e: English = g
        return e.core("kt")
    }
}
