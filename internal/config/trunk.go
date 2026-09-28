package config

// TrunkSource says how a repository's trunk was determined. --json prints it
// as trunkSource, a closed enum.
type TrunkSource string

const (
	// TrunkFromConfig is MAIN_BRANCH in the configuration file.
	TrunkFromConfig TrunkSource = "config"
	// TrunkFromOriginHead is the branch refs/remotes/origin/HEAD names.
	TrunkFromOriginHead TrunkSource = "originHead"
	// TrunkConventional is the first of development, main and master that
	// exists here or on origin.
	TrunkConventional TrunkSource = "conventional"
)
