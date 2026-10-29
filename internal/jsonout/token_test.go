package jsonout

import (
	"strings"
	"testing"
)

func TestTokenFieldsDoNotRunTogether(t *testing.T) {
	a := NewToken("1:", "k").Str("a", "b\x00c").String()
	b := NewToken("1:", "k").Str("a", "b").Str("c", "").String()
	c := NewToken("1:", "k").List("a", "b", "c").String()
	d := NewToken("1:", "k").List("a", "b,c").String()
	e := NewToken("1:", "other").Str("a", "b\x00c").String()
	seen := map[string]string{}
	for name, tok := range map[string]string{"a": a, "b": b, "c": c, "d": d, "e": e} {
		if other, ok := seen[tok]; ok {
			t.Fatalf("%s and %s hash alike", name, other)
		}
		seen[tok] = name
	}
	if a != NewToken("1:", "k").Str("a", "b\x00c").String() {
		t.Fatal("the same fields gave another token")
	}
	if !strings.HasPrefix(a, "1:") || len(a) != len("1:")+32 {
		t.Fatalf("token %q", a)
	}
}

func TestTokenStrpOfNoTokenIsNil(t *testing.T) {
	var tok *Token
	if tok.Strp() != nil {
		t.Fatal("no token is not null")
	}
}

func TestTokenGapsNamesWhatNobodyDecided(t *testing.T) {
	type row struct{ ID, Name string }
	type base struct{ Repo string }
	type plan struct {
		base
		Trunk, Age string
		Rows       []row
		hidden     string
	}
	_ = plan{}.hidden
	tok := NewToken("1:", "k").Str("Repo", "r").Str("Age", "a").Str("Rows.ID", "x")
	got := TokenGaps(tok, plan{}, "", map[string]string{"Age": "why", "Gone": "why", "Rows.Name": "why"})
	want := []string{"Age is hashed and listed as left out", "Gone is listed as left out but is no field",
		"Trunk is neither hashed nor listed as left out"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("gaps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := TokenGaps(tok, row{}, "Rows.", map[string]string{"Rows.Name": "why", "Age": "why"}); len(got) != 0 {
		t.Fatalf("row gaps %v", got)
	}
}
