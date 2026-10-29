package synccmd

import (
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// tokenRepo is a repository and its HEAD, to hash a plan against.
func tokenRepo(t *testing.T) (*kit.Context, string) {
	t.Helper()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, err := kit.Open(main, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, testkit.GitOut(t, main, "rev-parse", "HEAD")
}

// A declaration git could not read is not the same as none on trunk: a
// token hashed over the failure would match a later read that found one.
func TestSurveyTokenRefusesADeclarationGitCouldNotRead(t *testing.T) {
	t.Parallel()
	ctx, sha := tokenRepo(t)
	ctx.Repo.MainRoot = t.TempDir() // not a repository: every git there fails
	sv := Survey{Rows: []OverviewWorktree{{Branch: kit.Strp("b"), Verdict: VerdictProceed}}}
	tok, err := sv.Token(ctx, "main", sha)
	if tok != nil {
		t.Fatalf("a token over a declaration git could not read: %s", *tok)
	}
	if err == nil {
		t.Fatal("no error for a declaration git could not read")
	}
}

// tokenReads is a kit.Token that has hashed, by name, every exported field
// of the plan base builds whose change changes token: the fields the hand
// built tokens read, found by changing each in turn rather than listed, for
// kit.TokenGaps to check. A row field is named through the plan's first row,
// which base must give every slice of rows it should look into.
func tokenReads[P any](t *testing.T, base func() P, token func(P) string) *kit.Token {
	t.Helper()
	want := token(base())
	reads := kit.NewToken("", "reads")
	var walk func(typ reflect.Type, path []int, prefix string)
	walk = func(typ reflect.Type, path []int, prefix string) {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			at := append(slices.Clone(path), i)
			switch {
			case f.Anonymous && f.Type.Kind() == reflect.Struct:
				walk(f.Type, at, prefix)
				continue
			case !f.IsExported():
				continue
			}
			name := prefix + f.Name
			p := base()
			changeField(t, fieldAt(reflect.ValueOf(&p).Elem(), at))
			if token(p) != want {
				reads.Str(name, "")
			}
			switch {
			case f.Type.Kind() == reflect.Struct:
				walk(f.Type, at, name+".")
			case f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Struct:
				if fieldAt(reflect.ValueOf(base()), at).Len() > 0 {
					walk(f.Type.Elem(), append(at, -1), name+".")
				}
			}
		}
	}
	walk(reflect.TypeOf(base()), nil, "")
	return reads
}

// fieldAt follows path from v: a field index, or -1 for a slice's first row.
func fieldAt(v reflect.Value, path []int) reflect.Value {
	for _, i := range path {
		if i < 0 {
			v = v.Index(0)
		} else {
			v = v.Field(i)
		}
	}
	return v
}

// changeField gives v a value other than the one it has.
func changeField(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-changed")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int:
		v.SetInt(v.Int() + 1)
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.New(v.Type().Elem()).Elem()))
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				changeField(t, v.Field(i))
			}
		}
	case reflect.Pointer:
		n := reflect.New(v.Type().Elem())
		if !v.IsNil() {
			n.Elem().Set(v.Elem())
		}
		if k := n.Elem().Kind(); k == reflect.Struct && !v.IsNil() {
			v.Set(reflect.Zero(v.Type()))
			return
		} else if k != reflect.Struct {
			changeField(t, n.Elem())
		}
		v.Set(n)
	default:
		t.Fatalf("no way to change a %s", v.Type())
	}
}

// Every field of what wt sync --json shows a run would start on is in the
// overview token or left out on purpose, with why: a field added to a row
// fails here until someone decides. The token is built by hand, to keep the
// value docs/json.md promises, so what it reads is found by changing each
// field rather than by kit.Token's record.
func TestSurveyTokenCoversEveryRowField(t *testing.T) {
	t.Parallel()
	ctx, sha := tokenRepo(t)
	base := func() Survey {
		return Survey{Rows: []OverviewWorktree{{
			Work: "w", Branch: kit.Strp("b"), Path: "/w", Group: GroupReady, Class: "clean", Verdict: VerdictProceed,
			Stack:     []PlanStackMember{{Work: "w", Branch: "b", Path: "/w"}},
			OwnRemote: syncrun.OwnRemote{Blocks: kit.Strp(wtsync.OwnBlockDiverged), Commit: kit.Strp("c0ffee")},
		}}}
	}
	token := func(sv Survey) string {
		tok, err := sv.Token(ctx, "main", sha)
		if err != nil {
			t.Fatal(err)
		}
		if tok == nil {
			return ""
		}
		return *tok
	}
	reads := tokenReads(t, base, token)
	gaps := kit.TokenGaps(reads, Survey{}, "", nil)
	gaps = append(gaps, kit.TokenGaps(reads, OverviewWorktree{}, "Rows.", map[string]string{
		"Rows.Work":             "the branch names the worktree",
		"Rows.Path":             "the branch names the worktree",
		"Rows.Group":            "how the overview files the row; the verdict and class are in it",
		"Rows.Reason":           "the verdict, which is in it, in words",
		"Rows.Behind":           "a newer trunk commit is not in it; what it changes about the class is",
		"Rows.Ahead":            "a newer trunk commit is not in it; what it changes about the class is",
		"Rows.Dirty":            "the verdict it gives is in it",
		"Rows.HandedOver":       "the verdict it gives is in it",
		"Rows.PlanFile":         "the verdict it gives is in it",
		"Rows.Sessions":         "the verdict they give is in it; a run weighs them again when it starts",
		"Rows.Stops":            "what the simulation met on the way; the class it came to is in it",
		"Rows.Strategies":       "trunk's declaration, which is in it",
		"Rows.DeferredDeclared": "trunk's declaration, which is in it",
		"Rows.Notes":            "words",
		"Rows.Error":            "the verdict it gives is in it",
	})...)
	gaps = append(gaps, kit.TokenGaps(reads, PlanStackMember{}, "Rows.Stack.", map[string]string{
		"Rows.Stack.Work": "the branch names the member",
		"Rows.Stack.Path": "the branch names the member",
	})...)
	gaps = append(gaps, kit.TokenGaps(reads, syncrun.OwnRemote{}, "Rows.OwnRemote.", ownRemoteLeftOut("Rows.OwnRemote."))...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in Survey.Token or here:\n%s", strings.Join(gaps, "\n"))
	}
}

// ownRemoteLeftOut is the fields of a branch against its own remote that no
// token reads: only the commit of a remote a branch has diverged from is in
// one, and Blocks, which says whether it has.
func ownRemoteLeftOut(prefix string) map[string]string {
	return map[string]string{
		prefix + "Ref":          "the branch names it",
		prefix + "State":        "Blocks says what it does to a run",
		prefix + "Ahead":        "the commit is in it where it decides anything",
		prefix + "Behind":       "the commit is in it where it decides anything",
		prefix + "Fetched":      "whether this read fetched; the commit is what counts",
		prefix + "NoPushReason": "the push after the run, not what it starts on",
		prefix + "FixCommand":   "the push after the run, not what it starts on",
	}
}

// Every field of wt status <work> --json is in its token or left out on
// purpose, with why. As with the overview, the token is built by hand, so
// what it reads is found by changing each field.
func TestUpPlanTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	ctx, _ := tokenRepo(t)
	base := func() UpPlan {
		return UpPlan{Trunk: kit.Strp("main"), Stack: []PlanStack{{
			PlanStackMember: PlanStackMember{Work: "w", Branch: "b", Path: "/w"},
			OwnRemote:       syncrun.OwnRemote{Blocks: kit.Strp(wtsync.OwnBlockDiverged), Commit: kit.Strp("c0ffee")},
		}}}
	}
	// What the run's own --expect check hashes, from the same inputs.
	if got, want := upPlanToken(ctx, base()), syncrun.PlanToken(ctx, "main", []string{"b"}, map[string]string{"b": "c0ffee"}); got != want {
		t.Fatalf("the plan's token %s is not the run's %s", got, want)
	}
	reads := tokenReads(t, base, func(p UpPlan) string { return upPlanToken(ctx, p) })
	gaps := kit.TokenGaps(reads, UpPlan{}, "", map[string]string{
		"Schema":             "the format, not the plan",
		"SchemaVersion":      "the format, not the plan",
		"Command":            "the same for every plan of its kind",
		"Token":              "the token itself",
		"TrunkSource":        "how trunk was found; trunk itself is in it",
		"TrunkRef":           "trunk's name gives it",
		"TrunkRefExists":     "a newer trunk commit is not in it",
		"TrunkTip":           "a newer trunk commit is not in it",
		"TrunkRefUpdatedAt":  "a newer trunk commit is not in it",
		"TrunkSync":          "local trunk is brought along by wt up as it finds it then",
		"Configured":         "the configuration's own fingerprint is in it",
		"Worktree":           "its branch heads the stack, which is in it",
		"UpEligible":         "a plan wt up would not start on has no token, but for divergence, which is in it",
		"UpIneligibleCode":   "a plan wt up would not start on has no token, but for divergence, which is in it",
		"UpIneligibleReason": "a plan wt up would not start on has no token, but for divergence, which is in it",
		"Sessions":           "wt up weighs the sessions again when it starts",
		"SessionsError":      "wt up weighs the sessions again when it starts",
	})
	gaps = append(gaps, kit.TokenGaps(reads, PlanStack{}, "Stack.", map[string]string{
		"Stack.Work":             "the branch names the member",
		"Stack.Path":             "the branch names the member",
		"Stack.DeferredDeclared": "trunk's declaration, read again when wt up starts",
	})...)
	gaps = append(gaps, kit.TokenGaps(reads, syncrun.OwnRemote{}, "Stack.OwnRemote.", ownRemoteLeftOut("Stack.OwnRemote."))...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in upPlanToken or here:\n%s", strings.Join(gaps, "\n"))
	}
}
