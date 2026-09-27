#!/usr/bin/env bash
# Run bats under a deadline, so a test that hangs fails the run instead of
# blocking it for good.
#
#   scripts/bats.sh test/                    # what `make bats` runs
#   BATS_DEADLINE=1200 scripts/bats.sh test/ # seconds; default 600
#
# The hang this was written for: bash 5.1 up to 5.3 patch 15 on macOS writes
# a here-string of up to 64 KiB into a pipe, but while the machine's pipes
# hold a lot of memory (several test suites at once) a new macOS pipe takes
# 512 bytes. bats' `run` feeds every output back through a here-string, so a
# test whose output passes 512 bytes blocks writing to itself, for ever.
# bash53-016 fixes it; `brew upgrade bash`.
#
# On the deadline the whole bats process tree is killed: the stuck shell
# holds both ends of its pipe, and nothing else would ever end it.
set -u
deadline=${BATS_DEADLINE:-600}

# The bash bats will run under is the first on the PATH.
if [ "$(uname -s)" = Darwin ]; then
    read -r major minor patch < <(bash -c 'echo "${BASH_VERSINFO[@]:0:3}"')
    if [ "$major" -eq 5 ] && { [ "$minor" -eq 1 ] || [ "$minor" -eq 2 ] ||
        { [ "$minor" -eq 3 ] && [ "$patch" -lt 16 ]; }; }; then
        echo "warning: bash $major.$minor.$patch can hang bats on a loaded Mac; brew upgrade bash" >&2
    fi
fi

exec perl -e '
    my ($deadline, @cmd) = @ARGV;
    my $pid = fork // die "fork: $!\n";
    if ($pid == 0) { exec @cmd or die "$cmd[0]: $!\n" }
    # Ctrl-C reaches bats directly; this waits for it to finish.
    $SIG{INT} = "IGNORE";
    $SIG{ALRM} = sub {
        print STDERR "bats did not finish within ${deadline}s; killing it\n";
        my %kids;
        for (`ps -A -o pid= -o ppid=`) {
            my ($p, $pp) = split;
            push @{$kids{$pp}}, $p;
        }
        my @tree = ($pid);
        for (my $i = 0; $i < @tree; $i++) { push @tree, @{$kids{$tree[$i]} // []} }
        kill "TERM", @tree;
        sleep 2;
        kill "KILL", @tree;
        exit 124;
    };
    alarm $deadline;
    waitpid $pid, 0;
    exit($? & 127 ? 128 + ($? & 127) : $? >> 8);
' "$deadline" bats "$@"
