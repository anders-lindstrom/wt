package wtsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// OpenAPI resolves a generated OpenAPI document by merging its mutable
// sections key by key. It never regenerates: the deferred step declared beside
// it produces the authoritative bytes once, at the end of the rebase. springdoc
// emits paths and schemas in registration order, so this cannot reproduce the
// generator byte for byte; it exists to let the replay continue.
type OpenAPI struct {
	// Rule decides info.version: max-plus-patch, keep-branch or keep-trunk.
	Rule string
}

// Name identifies this strategy in errors and reports.
func (OpenAPI) Name() string { return "openapi" }

// Resolve merges the document's mutable sections and decides info.version by
// Rule; anything else differing between base and branch is a refusal.
func (s OpenAPI) Resolve(c Conflict) ([]byte, error) {
	base, err := parseDoc(c.Path, "base", c.Base)
	if err != nil {
		return nil, err
	}
	trunk, err := parseDoc(c.Path, "trunk", c.Trunk)
	if err != nil {
		return nil, err
	}
	branch, err := parseDoc(c.Path, "branch", c.Branch)
	if err != nil {
		return nil, err
	}

	// Everything the merge does not own must be identical on base and branch.
	// Anything else means the branch edited part of the document this
	// strategy has no rule for, and guessing there is exactly what it must
	// not do.
	if !reflect.DeepEqual(remainder(base), remainder(branch)) {
		return nil, Refuse(c.Path, "the branch changed the document outside paths, schemas, tags and version")
	}

	sections, err := loadSections(c.Path, base, trunk, branch)
	if err != nil {
		return nil, err
	}
	paths, pathConflicts := merge3Keys(sections.paths[0], sections.paths[1], sections.paths[2])
	schemas, schemaConflicts := merge3Keys(sections.schemas[0], sections.schemas[1], sections.schemas[2])
	tags, tagConflicts := merge3Keys(sections.tags[0], sections.tags[1], sections.tags[2])
	var bad []string
	var keys []string
	if len(pathConflicts) > 0 {
		bad = append(bad, "paths: "+strings.Join(pathConflicts, ", "))
		keys = append(keys, pathConflicts...)
	}
	if len(schemaConflicts) > 0 {
		bad = append(bad, "schemas: "+strings.Join(schemaConflicts, ", "))
		keys = append(keys, schemaConflicts...)
	}
	if len(tagConflicts) > 0 {
		bad = append(bad, "tags: "+strings.Join(tagConflicts, ", "))
		keys = append(keys, tagConflicts...)
	}
	if len(bad) > 0 {
		sort.Strings(keys)
		return nil, &Refusal{Path: c.Path, Reason: fmt.Sprintf("both sides changed %s", strings.Join(bad, "; ")), Keys: keys}
	}

	version, err := s.version(c.Path, branch.version(), trunk.version())
	if err != nil {
		return nil, err
	}
	components, err := trunk.components()
	if err != nil {
		return nil, Refuse(c.Path, "trunk: %v", err)
	}
	info, err := trunk.info()
	if err != nil {
		return nil, Refuse(c.Path, "trunk: %v", err)
	}

	// Trunk is the skeleton: it carries the newest generator output for every
	// section this strategy does not merge, and the guard above proved the
	// branch did not touch any of them. out aliases trunk's *omap, so every
	// section is read out of trunk above before out is mutated below.
	out := trunk
	out.set("paths", paths.raw())
	components.set("schemas", schemas.raw())
	out.set("components", components.raw())
	out.set("tags", tags.values())
	info.set("version", mustRaw(version))
	out.set("info", info.raw())
	return writeJackson(out.raw())
}

// merged holds the three mergeable sections of base, trunk and branch, in
// that order.
type merged struct {
	paths, schemas, tags [3]*omap
}

// loadSections parses every section the merge touches, on every side, and
// refuses the file on the first malformed one.
func loadSections(path string, base, trunk, branch doc) (merged, error) {
	var m merged
	for i, d := range [...]doc{base, trunk, branch} {
		side := [...]string{"base", "trunk", "branch"}[i]
		var err error
		if m.paths[i], err = d.section("paths"); err != nil {
			return m, Refuse(path, "%s: %v", side, err)
		}
		if m.schemas[i], err = d.schemas(); err != nil {
			return m, Refuse(path, "%s: %v", side, err)
		}
		if m.tags[i], err = d.tagsByName(); err != nil {
			return m, Refuse(path, "%s: %v", side, err)
		}
	}
	return m, nil
}

func (s OpenAPI) version(path, branchV, trunkV string) (string, error) {
	switch s.Rule {
	case "keep-branch":
		if branchV == "" {
			return "", Refuse(path, "info.version missing")
		}
		return branchV, nil
	case "keep-trunk":
		if trunkV == "" {
			return "", Refuse(path, "info.version missing")
		}
		return trunkV, nil
	case "max-plus-patch", "":
		if !exactSemverRE.MatchString(branchV) || !exactSemverRE.MatchString(trunkV) {
			return "", Refuse(path, "info.version is not X.Y.Z on both sides (%q, %q)", branchV, trunkV)
		}
		return MaxPlusPatch(branchV, trunkV), nil
	}
	return "", fmt.Errorf("unknown rule %q", s.Rule)
}

// omap is a JSON object that remembers key order, holding raw values.
type omap struct {
	keys []string
	vals map[string]json.RawMessage
}

func newOmap() *omap { return &omap{vals: map[string]json.RawMessage{}} }

func (o *omap) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *omap) get(k string) json.RawMessage { return o.vals[k] }

// has reports whether k was present in the source object, distinguishing a
// key that was dropped entirely from one whose value happens to be null.
func (o *omap) has(k string) bool {
	_, ok := o.vals[k]
	return ok
}

func (o *omap) raw() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// values renders the map's values as a JSON array, in key order.
func (o *omap) values() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(o.vals[k])
	}
	b.WriteByte(']')
	return b.Bytes()
}

// parseOmap decodes one JSON object, keeping key order. A null or missing
// object decodes to an empty map.
func parseOmap(data json.RawMessage) (*omap, error) {
	o := newOmap()
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('{') {
		return nil, fmt.Errorf("expected an object, got %v", tok)
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
	}
	// The object must close, and nothing may follow it: a truncated or
	// trailing-garbage document is not one to rewrite.
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, fmt.Errorf("object not closed")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after the document")
	}
	return o, nil
}

// doc is one OpenAPI document with the accessors the merge needs.
type doc struct{ *omap }

func parseDoc(path, side string, data []byte) (doc, error) {
	o, err := parseOmap(data)
	if err != nil {
		return doc{}, Refuse(path, "%s is not a JSON object: %v", side, err)
	}
	return doc{o}, nil
}

// section parses one required object-valued key. A missing key and a
// malformed one are both errors, never an empty map: a branch that dropped
// the whole key must not read as "deleted every element in it".
func (d doc) section(name string) (*omap, error) {
	if !d.has(name) {
		return nil, fmt.Errorf("%s is missing", name)
	}
	o, err := parseOmap(d.get(name))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return o, nil
}

func (d doc) components() (*omap, error) { return d.section("components") }
func (d doc) info() (*omap, error)       { return d.section("info") }

func (d doc) schemas() (*omap, error) {
	c, err := d.components()
	if err != nil {
		return nil, err
	}
	if !c.has("schemas") {
		return nil, fmt.Errorf("components.schemas is missing")
	}
	o, err := parseOmap(c.get("schemas"))
	if err != nil {
		return nil, fmt.Errorf("components.schemas: %v", err)
	}
	return o, nil
}

// tagsByName keys the tags array by each tag's name, so it merges like a map.
func (d doc) tagsByName() (*omap, error) {
	if !d.has("tags") {
		return nil, fmt.Errorf("tags is missing")
	}
	var tags []json.RawMessage
	if err := json.Unmarshal(d.get("tags"), &tags); err != nil {
		return nil, fmt.Errorf("tags: %v", err)
	}
	o := newOmap()
	for _, t := range tags {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(t, &named); err != nil {
			return nil, fmt.Errorf("tags: entry is not an object")
		}
		if named.Name == "" {
			return nil, fmt.Errorf("tags: an entry has an empty name")
		}
		o.set(named.Name, t)
	}
	return o, nil
}

func (d doc) version() string {
	var v struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(orNull(d.get("info")), &v)
	return v.Version
}

// remainder is the document with the merged sections and the version
// blanked, as a structure, for order-insensitive comparison.
func remainder(d doc) any {
	decoded, _ := decodeExact(d.raw())
	v, _ := decoded.(map[string]any)
	delete(v, "paths")
	delete(v, "tags")
	if comp, ok := v["components"].(map[string]any); ok {
		delete(comp, "schemas")
	}
	if info, ok := v["info"].(map[string]any); ok {
		info["version"] = nil
	}
	return v
}

// merge3Keys merges three maps key by key. A key changed on one side only
// takes that side's value; a key removed on one side and untouched on the
// other is dropped; a key both sides changed differently is a conflict. The
// result keeps trunk's key order and appends the branch's additions after.
func merge3Keys(base, trunk, branch *omap) (*omap, []string) {
	out := newOmap()
	var conflicts []string
	seen := map[string]bool{}
	all := append(append([]string{}, trunk.keys...), branch.keys...)
	all = append(all, base.keys...)
	for _, k := range all {
		if seen[k] {
			continue
		}
		seen[k] = true
		b, t, r := base.get(k), trunk.get(k), branch.get(k)
		trunkChanged := !jsonEqual(t, b)
		branchChanged := !jsonEqual(r, b)
		switch {
		case trunkChanged && branchChanged && !jsonEqual(t, r):
			conflicts = append(conflicts, k)
		case trunkChanged:
			if t != nil {
				out.set(k, t)
			}
		default:
			if r != nil {
				out.set(k, r)
			}
		}
	}
	sort.Strings(conflicts)
	return out, conflicts
}

// jsonEqual compares two raw values structurally; nil means absent. Numbers
// are compared as their literal text (UseNumber), not as float64, so two
// large integers that differ in the last digit are not "equal".
func jsonEqual(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	va, errA := decodeExact(a)
	vb, errB := decodeExact(b)
	if errA != nil || errB != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(va, vb)
}

// decodeExact decodes into generic values with numbers kept as json.Number.
func decodeExact(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func orNull(r json.RawMessage) json.RawMessage {
	if r == nil {
		return json.RawMessage("null")
	}
	return r
}

func mustRaw(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// writeJackson writes raw the way springdoc's Jackson ObjectMapper does with
// its DefaultPrettyPrinter: objects two-space indented with one field per
// line, arrays written inline on a single line — including arrays of
// objects, whose braces sit inline with the array's own brackets — and no
// newline at the end. Keys and string values are both decoded then
// re-encoded through an encoder with HTML-escaping off, so the two escape
// the same way regardless of how the source document escaped them.
func writeJackson(raw json.RawMessage) ([]byte, error) {
	var b bytes.Buffer
	if err := writeJacksonValue(&b, raw, 0); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeJacksonValue(b *bytes.Buffer, raw json.RawMessage, indent int) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty JSON value")
	}
	switch trimmed[0] {
	case '{':
		return writeJacksonObject(b, raw, indent)
	case '[':
		return writeJacksonArray(b, raw, indent)
	default:
		return writeJacksonScalar(b, trimmed)
	}
}

// writeJacksonObject writes one field per line at indent+1, closing on its
// own line at indent; an empty object is written inline as "{ }".
func writeJacksonObject(b *bytes.Buffer, raw json.RawMessage, indent int) error {
	o, err := parseOmap(raw)
	if err != nil {
		return err
	}
	if len(o.keys) == 0 {
		b.WriteString("{ }")
		return nil
	}
	b.WriteByte('{')
	child := indent + 1
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
		writeJacksonIndent(b, child)
		if err := writeJacksonString(b, k); err != nil {
			return err
		}
		b.WriteString(" : ")
		if err := writeJacksonValue(b, o.vals[k], child); err != nil {
			return err
		}
	}
	b.WriteByte('\n')
	writeJacksonIndent(b, indent)
	b.WriteByte('}')
	return nil
}

// writeJacksonArray writes every element inline on the array's own line,
// including an object element's opening brace; an empty array is written
// inline as "[ ]". Elements are not indented relative to the array itself —
// only an object element's own fields, one level in from indent — because
// Jackson's default array indenter never breaks a line.
func writeJacksonArray(b *bytes.Buffer, raw json.RawMessage, indent int) error {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return err
	}
	if len(elems) == 0 {
		b.WriteString("[ ]")
		return nil
	}
	b.WriteString("[ ")
	for i, e := range elems {
		if i > 0 {
			b.WriteString(", ")
		}
		if err := writeJacksonValue(b, e, indent); err != nil {
			return err
		}
	}
	b.WriteString(" ]")
	return nil
}

func writeJacksonScalar(b *bytes.Buffer, raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case string:
		return writeJacksonString(b, v)
	case json.Number:
		b.WriteString(v.String())
		return nil
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil
	case nil:
		b.WriteString("null")
		return nil
	default:
		return fmt.Errorf("unexpected JSON token %v", tok)
	}
}

// writeJacksonString encodes s with HTML-escaping off, so it escapes the
// same way whether it came in as an object key or a string value.
func writeJacksonString(b *bytes.Buffer, s string) error {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	b.Truncate(b.Len() - 1) // Encode appends a trailing newline this format does not want.
	return nil
}

func writeJacksonIndent(b *bytes.Buffer, level int) {
	for i := 0; i < level; i++ {
		b.WriteString("  ")
	}
}
