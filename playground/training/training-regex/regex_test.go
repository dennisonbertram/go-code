package regex

import (
	"testing"
)

// Helper for errorless compile
func mustCompile(pat string) *Regexp {
	r, err := Compile(pat)
	if err != nil {
		panic(err)
	}
	return r
}

func TestLiteral(t *testing.T) {
	r := mustCompile("abc")
	if !r.Match("abc") || r.Match("ab") || r.Match("abcd") {
		t.Errorf("literal failed")
	}
}

func TestAnyDot(t *testing.T) {
	r := mustCompile("a.c")
	if !r.Match("abc") || !r.Match("a1c") || r.Match("ac") {
		t.Errorf("dot failed")
	}
}

func TestStar(t *testing.T) {
	r := mustCompile("ab*c")
	if !r.Match("ac") || !r.Match("abc") || !r.Match("abbbc") || r.Match("abx") {
		t.Errorf("star failed")
	}
}

func TestPlus(t *testing.T) {
	r := mustCompile("ab+c")
	if r.Match("ac") || !r.Match("abc") || !r.Match("abbbbbc") {
		t.Errorf("plus failed")
	}
}

func TestQuest(t *testing.T) {
	r := mustCompile("ab?c")
	if !r.Match("abc") || !r.Match("ac") || r.Match("abbc") {
		t.Errorf("quest failed")
	}
}

func TestAlt(t *testing.T) {
	r := mustCompile("foo|bar")
	if !r.Match("foo") || !r.Match("bar") || r.Match("foobar") {
		t.Errorf("alt failed")
	}
}

func TestGroup(t *testing.T) {
	r := mustCompile("a(bc|de)f")
	if !r.Match("abcf") || !r.Match("adef") || r.Match("abdef") {
		t.Errorf("group/alt failed")
	}
}

func TestNestedGroup(t *testing.T) {
	r := mustCompile("a(b(cd|ef))g")
	if !r.Match("abcdg") || !r.Match("abefg") {
		t.Errorf("nested group failed")
	}
}

func TestAnchors(t *testing.T) {
	r1 := mustCompile("^abc")
	if !r1.Match("abc") || r1.Match("xabc") {
		t.Errorf("begin anchor failed")
	}
	r2 := mustCompile("abc$")
	if !r2.Match("abc") || r2.Match("abcc") {
		t.Errorf("end anchor failed")
	}
	r3 := mustCompile("^abc$")
	if !r3.Match("abc") || r3.Match("aabc") || r3.Match("abcc") {
		t.Errorf("both anchor failed")
	}
}

func TestEmptyPattern(t *testing.T) {
	r := mustCompile("")
	if !r.Match("") || r.Match("a") {
		t.Errorf("empty pattern failed")
	}
}

func TestEmptyString(t *testing.T) {
	r := mustCompile("a*")
	if !r.Match("") {
		t.Errorf("empty string star failed")
	}
	if mustCompile("a").Match("") {
		t.Errorf("empty string lit failed")
	}
}

func TestEdgeCases(t *testing.T) {
	r := mustCompile("(a|)")
	if !r.Match("") || !r.Match("a") {
		t.Errorf("alt empty failed")
	}

	r2 := mustCompile("(|a)")
	if !r2.Match("") || !r2.Match("a") {
		t.Errorf("alt empty reverse failed")
	}
}
