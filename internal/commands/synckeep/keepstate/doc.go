// Package keepstate holds the keeper's state file, its lock and its launchd
// job: what wt sync keep writes, and what the wt sync table and wt sync
// doctor read back. It sits below synckeep, which runs passes through
// synccmd, so that synccmd can read it.
package keepstate
