#!/usr/bin/env bash
# Migrate one repository onto wt: shims, optional provision.sh, retired keys.
#
#   scripts/migrate-repo.sh <repo-path> <trunk> <provision:none|aws|aws-decrypt>
#
# Runs entirely in a NEW worktree cut from the repo's trunk. The main checkout
# is never touched — several of these repos sit on feature branches with
# uncommitted work and stashes. No existing worktree is moved and no branch
# other than the migration branch is created.
set -euo pipefail

repo="${1:?repo path required}"
trunk="${2:?trunk branch required}"
provision="${3:?none|aws|aws-decrypt}"

repo=$(cd "$repo" && pwd -P)
name=$(basename "$repo")
parent=$(dirname "$repo")
wtdir="$parent/${name}_wt/chore_wt/wt-migration"

echo "──────── $name (trunk=$trunk, provision=$provision)"

git -C "$repo" worktree list > "/tmp/$name.worktrees.before"

if git -C "$repo" show-ref --verify --quiet refs/heads/chore_wt/wt-migration; then
    echo "  migration branch already exists — skipping create" >&2
else
    git -C "$repo" worktree add -q -b chore_wt/wt-migration "$wtdir" "$trunk"
fi
cd "$wtdir"

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/lib-shims.sh"
write_shims

[ -f bin/worktree/remove_me_function.sh ] && git rm -q bin/worktree/remove_me_function.sh
for h in bin/hooks/worktree-create.sh bin/hooks/worktree-remove.sh; do
    [ -f "$h" ] && git rm -q "$h"
done

if [ "$provision" != "none" ]; then
    {
        cat <<'EOF'
#!/usr/bin/env bash
# This repository's own worktree setup step, run by `wt setup` with the new
# worktree as its working directory.
#
# Replaces the AWS_SETUP_ENABLED flag: the behaviour belongs to the repo, not
# to the generic tool.
set -euo pipefail

identity=$(aws sts get-caller-identity --query 'Arn' --output text 2>/dev/null || echo "unknown")
if [[ "$identity" == "unknown" ]]; then
    echo "Error: no usable AWS credentials; run your AWS login first" >&2
    exit 1
fi
echo "AWS access confirmed: $identity"
EOF
        if [ "$provision" = "aws-decrypt" ]; then
            cat <<'EOF'

if [[ -f etc/encrypted/bin/decrypt.sh ]]; then
    echo "Decrypting local secrets..."
    if ! etc/encrypted/bin/decrypt.sh local; then
        echo "Error: failed to decrypt local secrets." >&2
        echo "  Check AWS permissions, region and KMS key access." >&2
        exit 1
    fi
fi
EOF
        fi
    } > bin/worktree/provision.sh
    chmod +x bin/worktree/provision.sh
fi

# Drop retired keys together with the comment block that introduced them.
NOTE_PROVISION="$provision" python3 - <<'PY'
import os, re, pathlib
p = pathlib.Path("bin/worktree/worktree.conf")
lines = p.read_text().splitlines(keepends=True)
retired = ("AWS_SETUP_ENABLED", "REPO_NAME", "WORKTREE_LAYOUT")
out, i = [], 0
while i < len(lines):
    stripped = lines[i].lstrip()
    key = stripped.split("=", 1)[0].strip().lstrip("# ")
    is_assign = re.match(r"^\s*(%s)=" % "|".join(retired), lines[i])
    is_commented_example = re.match(r"^\s*#\s*(%s)=" % "|".join(retired), lines[i])
    if is_assign or is_commented_example:
        # walk back over the contiguous comment block that introduced it
        while out and out[-1].lstrip().startswith("#") and out[-1].strip() != "#!/usr/bin/env bash":
            out.pop()
        i += 1
        continue
    out.append(lines[i]); i += 1
text = "".join(out)
if os.environ["NOTE_PROVISION"] != "none":
    text = text.replace("# Worktree configuration file\n",
        "# Worktree configuration file — see README.md\n")
p.write_text(re.sub(r"\n{3,}", "\n\n", text))
PY

echo "  retired keys remaining: $(grep -cE '^\s*(AWS_SETUP_ENABLED|REPO_NAME|WORKTREE_LAYOUT)=' bin/worktree/worktree.conf || true)"
echo "  worktree: $wtdir"
