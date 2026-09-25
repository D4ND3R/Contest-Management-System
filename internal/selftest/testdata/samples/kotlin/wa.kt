fun main() { val (a, b) = readLine()!!.trim().split(Regex("\\s+")).map { it.toLong() }; println(a - b) }
