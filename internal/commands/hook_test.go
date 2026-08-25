package commands

import (
	"bytes"
	"strings"
	"testing"
)

func TestHookCreatePrintsPathOnStdoutOnly(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var out, logw bytes.Buffer
	in := strings.NewReader(`{"name":"hooked","cwd":"/x","hook_event_name":"WorktreeCreate"}`)
	if err := HookCreate(ctx, in, &out, &logw); err != nil {
		t.Fatalf("HookCreate: %v", err)
	}
	path := strings.TrimSpace(out.String())
	if !strings.HasSuffix(path, "/demo_wt/feat_wt/hooked") {
		t.Errorf("stdout must be the path alone, got %q", path)
	}
	if strings.Count(path, "\n") != 0 {
		t.Errorf("stdout must be a single line, got %q", path)
	}
}

func TestHookCreateRejectsMissingName(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var out, logw bytes.Buffer
	if err := HookCreate(ctx, strings.NewReader(`{"cwd":"/x"}`), &out, &logw); err == nil {
		t.Error("want an error when name is absent")
	}
}

func TestHookRemoveIsMergeChecked(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var out, logw bytes.Buffer
	if err := HookCreate(ctx, strings.NewReader(`{"name":"gone"}`), &out, &logw); err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(out.String())
	out.Reset()
	if err := HookRemove(ctx, strings.NewReader(`{"path":"`+path+`"}`), &logw); err != nil {
		t.Fatalf("HookRemove: %v", err)
	}
	if ctx.Repo.BranchExists("feat_wt/gone") {
		t.Error("a merged branch should have been deleted by the hook")
	}
}

// Unknown fields must not break the hook when the harness adds one.
func TestHookToleratesUnknownFields(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var out, logw bytes.Buffer
	in := strings.NewReader(`{"name":"tol","brand_new_field":42,"nested":{"a":1}}`)
	if err := HookCreate(ctx, in, &out, &logw); err != nil {
		t.Fatalf("unknown fields should be ignored: %v", err)
	}
}
