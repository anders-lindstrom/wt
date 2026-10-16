package refpins

import (
	"encoding/json"
	"strings"
	"testing"
)

// Raw can carry credentials; only its json:"-" tag keeps it out of RunMeta
// and the --json outputs.
func TestEndpointRawIsNeverMarshalled(t *testing.T) {
	e := Endpoint{
		URL:    "https://example.com/r.git",
		Digest: "abc",
		Raw:    "https://user:" + "secret" + "@example.com/r.git",
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "secret") || strings.Contains(string(out), "Raw") {
		t.Errorf("Endpoint JSON carries Raw: %s", out)
	}
}
