package wtsync

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// spec builds a document in the generator's shape.
func spec(version, paths, schemas, tags string) string {
	return `{"openapi":"3.1.0","info":{"title":"T","version":"` + version + `"},"servers":[{"url":"/"}],"tags":` + tags +
		`,"paths":` + paths + `,"components":{"schemas":` + schemas + `,"securitySchemes":{"k":{"type":"apiKey"}}}}`
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, data)
	}
	return v
}

func keys(m any) string {
	obj, _ := m.(map[string]any)
	var out []string
	for k := range obj {
		out = append(out, k)
	}
	sortStrings(out)
	return strings.Join(out, ",")
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func TestOpenAPIMergesDisjointPathsSchemasAndTagsAndLiftsTheVersion(t *testing.T) {
	c := conflict(
		spec("2.38.0", `{"/a":{"get":{}}}`, `{"A":{}}`, `[{"name":"TA"}]`),
		spec("2.38.5", `{"/a":{"get":{}},"/t":{"get":{}}}`, `{"A":{},"T":{}}`, `[{"name":"TA"},{"name":"TT"}]`),
		spec("2.38.3", `{"/a":{"get":{}},"/b":{"get":{}}}`, `{"A":{},"B":{}}`, `[{"name":"TA"},{"name":"TB"}]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	v := decode(t, out)
	if keys(v["paths"]) != "/a,/b,/t" {
		t.Errorf("paths = %s", keys(v["paths"]))
	}
	if keys(v["components"].(map[string]any)["schemas"]) != "A,B,T" {
		t.Errorf("schemas = %s", keys(v["components"].(map[string]any)["schemas"]))
	}
	var names []string
	for _, tag := range v["tags"].([]any) {
		names = append(names, tag.(map[string]any)["name"].(string))
	}
	sortStrings(names)
	if strings.Join(names, ",") != "TA,TB,TT" {
		t.Errorf("tags = %v", names)
	}
	if v["info"].(map[string]any)["version"] != "2.38.6" {
		t.Errorf("version = %v", v["info"])
	}
}

func TestOpenAPIKeepBranchKeepsTheBranchsVersion(t *testing.T) {
	c := conflict(
		spec("2.38.0", `{"/a":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{}},"/t":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{}},"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "keep-branch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if v := decode(t, out)["info"].(map[string]any)["version"]; v != "2.38.3" {
		t.Errorf("version = %v", v)
	}
}

func TestOpenAPIKeepTrunkKeepsTrunksVersion(t *testing.T) {
	c := conflict(
		spec("2.38.0", `{"/a":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{}},"/t":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{}},"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "keep-trunk"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if v := decode(t, out)["info"].(map[string]any)["version"]; v != "2.38.5" {
		t.Errorf("version = %v", v)
	}
}

func TestOpenAPIKeyChangedIdenticallyOnBothSidesIsNotAConflict(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{"x":1}}}`, `{}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{"x":1}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"x" : 1`) {
		t.Errorf("out = %s", out)
	}
}

func TestOpenAPIDropsAKeyRemovedOnOneSideAndUntouchedOnTheOther(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"get":{}},"/old":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{}},"/old":{"get":{}},"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	if keys(decode(t, out)["paths"]) != "/a,/b" {
		t.Errorf("paths = %s", keys(decode(t, out)["paths"]))
	}
}

func TestOpenAPITakesTrunksOtherSectionsAsTheSkeleton(t *testing.T) {
	trunk := `{"openapi":"3.1.0","info":{"title":"T","version":"2.38.5"},"servers":[{"url":"/new"}],"tags":[],"paths":{"/t":{"get":{}}},"components":{"schemas":{},"securitySchemes":{"k":{"type":"apiKey"},"k2":{"type":"http"}}}}`
	c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), trunk, spec("2.38.3", `{"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	v := decode(t, out)
	if v["servers"].([]any)[0].(map[string]any)["url"] != "/new" {
		t.Errorf("servers = %v", v["servers"])
	}
	if keys(v["components"].(map[string]any)["securitySchemes"]) != "k,k2" {
		t.Errorf("securitySchemes = %s", keys(v["components"].(map[string]any)["securitySchemes"]))
	}
}

func TestOpenAPIRefusesNamingTheKeysBothSidesChanged(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"get":{}}}`, `{"S":{"a":1}}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{"x":1}}}`, `{"S":{"a":2}}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{"x":2}}}`, `{"S":{"a":3}}`, `[]`))
	_, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "paths: /a") || !strings.Contains(err.Error(), "schemas: S") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAPIRefusesWhenTheBranchChangedOutsideTheMergedSections(t *testing.T) {
	c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), spec("2.38.5", `{"/t":{"get":{}}}`, `{}`, `[]`),
		strings.Replace(spec("2.38.3", `{}`, `{}`, `[]`), `"title":"T"`, `"title":"Changed"`, 1))
	_, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "outside") {
		t.Errorf("err = %v", err)
	}
	var r *Refusal
	if !errors.As(err, &r) || len(r.Keys) != 0 {
		t.Errorf("a section-guard refusal must carry no Keys: %+v", r)
	}
}

func TestOpenAPIRefusalCarriesTheSortedConflictingKeys(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"get":{}}}`, `{"S":{"a":1}}`, `[]`),
		spec("2.38.5", `{"/a":{"get":{"x":1}}}`, `{"S":{"a":2}}`, `[]`),
		spec("2.38.3", `{"/a":{"get":{"x":2}}}`, `{"S":{"a":3}}`, `[]`))
	_, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(r.Keys, ","); got != "/a,S" {
		t.Errorf("keys = %q, want the sorted conflicting keys \"/a,S\"", got)
	}
}

func TestOpenAPIKeepBranchRefusesAnEmptyVersion(t *testing.T) {
	c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), spec("2.38.5", `{"/t":{"get":{}}}`, `{}`, `[]`),
		spec("", `{"/b":{"get":{}}}`, `{}`, `[]`))
	_, err := (OpenAPI{Rule: "keep-branch"}).Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "info.version missing") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAPIKeepTrunkRefusesAnEmptyVersion(t *testing.T) {
	c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), spec("", `{"/t":{"get":{}}}`, `{}`, `[]`),
		spec("2.38.3", `{"/b":{"get":{}}}`, `{}`, `[]`))
	_, err := (OpenAPI{Rule: "keep-trunk"}).Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "info.version missing") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAPIWritesJacksonStyleJSONWithNoTrailingNewlineAndTrunksKeyOrder(t *testing.T) {
	c := conflict(spec("2.38.0", `{}`, `{}`, `[]`), spec("2.38.5", `{"/t":{"get":{}}}`, `{}`, `[]`), spec("2.38.3", `{"/b":{"get":{}}}`, `{}`, `[]`))
	out, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "{\n  \"openapi\" : \"3.1.0\",\n  \"info\" : {") || strings.HasSuffix(s, "\n") || !strings.HasSuffix(s, "}") {
		t.Errorf("format: %q ... %q", s[:40], s[len(s)-20:])
	}
	if !strings.Contains(s, `"tags" : [ ]`) {
		t.Errorf("empty tags should be written inline: %s", s)
	}
	// trunk's path comes first, the branch's addition after it
	if strings.Index(s, `"/t"`) > strings.Index(s, `"/b"`) {
		t.Error("trunk's keys should come first")
	}
}

func TestOpenAPIRefusesInvalidJSON(t *testing.T) {
	good := spec("2.38.5", `{"/t":{"get":{}}}`, `{}`, `[]`)
	cases := map[string]struct {
		side string // which of base, trunk, branch carries the malformed document
		doc  string
	}{
		"syntax":           {"trunk", `{not json`},
		"truncated":        {"trunk", strings.TrimSuffix(good, "}")},
		"trailing":         {"trunk", good + `{"x":1}`},
		"paths not object": {"trunk", strings.Replace(good, `"paths":{"/t":{"get":{}}}`, `"paths":[1]`, 1)},
		"tag without name": {"trunk", strings.Replace(good, `"tags":[]`, `"tags":[{"x":1}]`, 1)},
		"no paths":         {"base", strings.Replace(spec("2.38.0", `{}`, `{}`, `[]`), `"paths":{},`, "", 1)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			base, trunk, branch := spec("2.38.0", `{}`, `{}`, `[]`), good, spec("2.38.3", `{"/b":{"get":{}}}`, `{}`, `[]`)
			switch tc.side {
			case "base":
				base = tc.doc
			case "trunk":
				trunk = tc.doc
			case "branch":
				branch = tc.doc
			}
			c := conflict(base, trunk, branch)
			if _, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c); !IsRefusal(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestOpenAPIComparesNumbersExactly(t *testing.T) {
	c := conflict(spec("2.38.0", `{"/a":{"max":9007199254740992}}`, `{}`, `[]`),
		spec("2.38.5", `{"/a":{"max":9007199254740993}}`, `{}`, `[]`),
		spec("2.38.3", `{"/a":{"max":9007199254740994}}`, `{}`, `[]`))
	_, err := (OpenAPI{Rule: "max-plus-patch"}).Resolve(c)
	if !IsRefusal(err) {
		t.Errorf("two large integers differing in the last digit must not compare equal: %v", err)
	}
}

func TestWriteJacksonRoundTripsTheGeneratorsStyle(t *testing.T) {
	sample := `{
  "title" : "T",
  "count" : 3,
  "ok" : true,
  "tags" : [ "a", "b" ],
  "empty" : { },
  "missing" : [ ],
  "nested" : {
    "inner" : "v"
  },
  "items" : [ {
    "name" : "x"
  }, {
    "name" : "y"
  } ]
}`
	// The sample's own whitespace is irrelevant to a JSON parser; feeding it
	// straight in as the raw value is exactly "parse the sample".
	out, err := writeJackson(json.RawMessage(sample))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != sample {
		t.Errorf("writeJackson output differs:\ngot:  %s\nwant: %s", out, sample)
	}
}

func TestFromRuleBuildsOpenAPI(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "openapi", Rule: "max-plus-patch"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	o, ok := s.(OpenAPI)
	if !ok || o.Rule != "max-plus-patch" {
		t.Errorf("FromRule = %#v, want OpenAPI{Rule: \"max-plus-patch\"}", s)
	}
	if s.Name() != "openapi" {
		t.Errorf("Name() = %q", s.Name())
	}
}
