package kit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Token builds a plan token: what --expect holds a run to. Same plan, same
// token; a different plan, a different token. Every field is written with
// its name, and every name and value with its length, so no two different
// sets of fields hash alike. The token is the prefix and the first 128 bits
// of the SHA-256, in hex. Its value is opaque to the --json contract and may
// change between versions; its meaning may not.
//
// A field is named after the plan's own field it reads, Rows.ID for a field
// of each row, so that TokenGaps can tell a plan field the token leaves out.
type Token struct {
	prefix string
	h      hash.Hash
	names  []string
}

// NewToken starts a token for one kind of plan: kind keeps two kinds that
// share a prefix apart.
func NewToken(prefix, kind string) *Token {
	t := &Token{prefix: prefix, h: sha256.New()}
	t.write("kind", kind)
	return t
}

func (t *Token) write(name string, values ...string) {
	if !slices.Contains(t.names, name) {
		t.names = append(t.names, name)
	}
	var n [binary.MaxVarintLen64]byte
	put := func(s string) {
		t.h.Write(n[:binary.PutUvarint(n[:], uint64(len(s)))])
		t.h.Write([]byte(s))
	}
	put(name)
	t.h.Write(n[:binary.PutUvarint(n[:], uint64(len(values)))])
	for _, v := range values {
		put(v)
	}
}

// Str hashes a string field.
func (t *Token) Str(name, v string) *Token { t.write(name, v); return t }

// Bool hashes a flag.
func (t *Token) Bool(name string, v bool) *Token { t.write(name, strconv.FormatBool(v)); return t }

// Int hashes a number.
func (t *Token) Int(name string, v int64) *Token { t.write(name, strconv.FormatInt(v, 10)); return t }

// List hashes a list, its order included.
func (t *Token) List(name string, vs ...string) *Token { t.write(name, vs...); return t }

// Config hashes the wt configuration file, as ConfigFingerprint reads it:
// for a plan whose outcome turns on the configuration.
func (t *Token) Config(fingerprint []byte) *Token {
	t.write("config", string(fingerprint))
	return t
}

// String is the token.
func (t *Token) String() string { return t.prefix + hex.EncodeToString(t.h.Sum(nil))[:32] }

// Strp is the token as --json writes it: nil for no token.
func (t *Token) Strp() *string {
	if t == nil {
		return nil
	}
	return Strp(t.String())
}

// TokenGaps is every exported field of plan, a struct, that t did not hash
// and skip does not name, and every name in skip that t hashed after all or
// plan does not have: what a test reports until someone decides whether a
// new field belongs in the token. prefix names the fields of a nested row
// type, "Rows." for plan's Rows; "" for plan's own. An embedded struct's
// fields count as plan's own. A field whose rows are
// hashed field by field counts as hashed, and its row type is checked on
// its own.
func TokenGaps(t *Token, plan any, prefix string, skip map[string]string) []string {
	var gaps []string
	fields := map[string]bool{}
	for _, name := range fieldNames(reflect.TypeOf(plan), prefix) {
		fields[name] = true
		_, skipped := skip[name]
		hashed := slices.ContainsFunc(t.names, func(n string) bool { return n == name || strings.HasPrefix(n, name+".") })
		switch {
		case hashed && skipped:
			gaps = append(gaps, name+" is hashed and listed as left out")
		case !hashed && !skipped:
			gaps = append(gaps, name+" is neither hashed nor listed as left out")
		}
	}
	for name := range skip {
		rest, ok := strings.CutPrefix(name, prefix)
		if ok && !strings.Contains(rest, ".") && !fields[name] {
			gaps = append(gaps, name+" is listed as left out but is no field")
		}
	}
	slices.Sort(gaps)
	return gaps
}

// fieldNames is every exported field of typ with prefix, an embedded
// struct's fields as its own.
func fieldNames(typ reflect.Type, prefix string) []string {
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		switch {
		case f.Anonymous && f.Type.Kind() == reflect.Struct:
			names = append(names, fieldNames(f.Type, prefix)...)
		case f.IsExported():
			names = append(names, prefix+f.Name)
		}
	}
	return names
}
