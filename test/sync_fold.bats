#!/usr/bin/env bats

load helpers
bats_require_minimum_version 1.5.0

# The per-layer fold of a stack, end to end through the built wt: the demo
# script builds the case and runs the look, the fold in place, the rebase
# onto trunk, the CI check at every layer tip and the undo.
@test "a stack's generated spec passes its check at every layer tip after one rebase" {
    export TMPDIR="$BATS_TEST_TMPDIR"
    run env WT="$BATS_TEST_DIRNAME/../bin/wt" "$BATS_TEST_DIRNAME/../scripts/fold-demo.sh"
    [ "$status" -eq 0 ]
    for layer in f1 f2 f3 f4 f5; do
        [[ "$output" == *"✓ $layer "*"gen/ unchanged"* ]]
    done
    [[ "$output" == *"✓ folded into"*"feat(f1): a"* ]]
    [[ "$output" == *"✓ folded into"*"feat(f3): c"* ]]
    [[ "$output" == *"⏸ f3 held: dirty"* ]]
    [[ "$output" == *"tmp/fold  "*" → "* ]]
}

@test "the demo explains itself and refuses what it does not know" {
    run "$BATS_TEST_DIRNAME/../scripts/fold-demo.sh" -h
    [ "$status" -eq 0 ]
    [[ "$output" == *"usage: scripts/fold-demo.sh"* ]]
    run "$BATS_TEST_DIRNAME/../scripts/fold-demo.sh" --bogus
    [ "$status" -eq 2 ]
}
