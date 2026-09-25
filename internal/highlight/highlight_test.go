package highlight

import (
	"strings"
	"testing"
)

func TestHighlight(t *testing.T) {
	src := "#include <stdio.h>\n/* multi\nline */ int main() { // hi\n  printf(\"%d<\\\"x\", 0x1F); return 0;\n}"
	got := string(HTML(src, ForFile("sol.CPP")))
	for _, want := range []string{
		`<span class="l"><span class="p">#include &lt;stdio.h&gt;</span></span>`,
		// A block comment spanning lines is closed and reopened per line.
		`<span class="l"><span class="c">/* multi</span></span>` + "\n" + `<span class="l"><span class="c">line */</span> <span class="k">int</span> main() { <span class="c">// hi</span></span>`,
		`printf(<span class="s">&#34;%d&lt;\&#34;x&#34;</span>, <span class="n">0x1F</span>); <span class="k">return</span> <span class="n">0</span>;`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin\n%s", want, got)
		}
	}
	if n := strings.Count(got, `<span class="l">`); n != 5 {
		t.Errorf("%d lines, want 5", n)
	}
	if strings.Count(got, "<span") != strings.Count(got, "</span>") {
		t.Error("unbalanced spans")
	}
	py := string(HTML("def f(x):\n    '''doc\n    string'''\n    return x # done\n", ForFile("a.py")))
	if !strings.Contains(py, `<span class="k">def</span>`) || !strings.Contains(py, `<span class="s">    string&#39;&#39;&#39;</span>`) ||
		!strings.Contains(py, `<span class="c"># done</span>`) {
		t.Errorf("python:\n%s", py)
	}
	pas := string(HTML("BEGIN { c } writeln('it''s'); END.", ForFile("a.pas")))
	if !strings.Contains(pas, `<span class="k">BEGIN</span>`) || !strings.Contains(pas, `<span class="c">{ c }</span>`) {
		t.Errorf("pascal:\n%s", pas)
	}
	rs := string(HTML("fn f<'a>(x: &'a str) -> char { 'z' }", ForFile("a.rs")))
	if !strings.Contains(rs, `<span class="s">&#39;z&#39;</span>`) || strings.Contains(rs, `<span class="s">&#39;a&gt;`) {
		t.Errorf("rust:\n%s", rs)
	}
	// Unknown files are escaped only; HTML never leaks.
	if got := string(HTML("<script>\n", ForFile("notes.txt"))); got != `<span class="l">&lt;script&gt;</span>`+"\n"+`<span class="l"></span>` {
		t.Errorf("plain: %q", got)
	}
}

func BenchmarkHighlight(b *testing.B) {
	src := strings.Repeat("for (int i = 0; i < n; ++i) { s += a[i] * 2; } // sum\n", 2000)
	s := ForFile("a.cpp")
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		HTML(src, s)
	}
}
