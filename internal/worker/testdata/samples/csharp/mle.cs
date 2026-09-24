using System.Collections.Generic;
class Sol { static void Main() { var l = new List<long[]>(); while (true) { var a = new long[1 << 20]; for (int i = 0; i < a.Length; i++) a[i] = i; l.Add(a); } } }
