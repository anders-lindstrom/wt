package config

// TrunkSource says how a repository's trunk was determined. --json prints it
// as trunkSource, a closed enum.
type TrunkSource string

const (
	// TrunkFromConfig is MAIN_BRANCH in the configuration file.
	TrunkFromConfig TrunkSource = "config"
	// TrunkFromOriginHead is the branch refs/remotes/origin/HEAD names.
	TrunkFromOriginHead TrunkSource = "originHead"
	// TrunkConventional is a branch called main, master, trunk, development
	// or develop that exists.
	TrunkConventional TrunkSource = "conventional"
	// TrunkCurrentBranchGuess is the main checkout's branch, taken because
	// nothing else named trunk. It may be a feature branch.
	TrunkCurrentBranchGuess TrunkSource = "currentBranchGuess"
)
