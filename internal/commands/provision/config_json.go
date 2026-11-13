package provision

import (
	"io"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/jsonout"
	"github.com/anlindstrom/wt/schema"
)

type configResult struct {
	Schema               int               `json:"schema"`
	SchemaVersion        string            `json:"schemaVersion"`
	Command              string            `json:"command"`
	Repo                 string            `json:"repo"`
	MainRoot             string            `json:"mainRoot"`
	Configured           bool              `json:"configured"`
	Trunk                *string           `json:"trunk"`
	TrunkSource          *string           `json:"trunkSource"`
	BranchPrefix         *string           `json:"branchPrefix"`
	TypeSuffix           *string           `json:"typeSuffix"`
	BranchSuffix         *string           `json:"branchSuffix"`
	DefaultType          *string           `json:"defaultType"`
	Types                []string          `json:"types"`
	TypeNames            map[string]string `json:"typeNames"`
	DeveloperConfigDirs  []string          `json:"developerConfigDirs"`
	DeveloperConfigFiles []string          `json:"developerConfigFiles"`
	CacheDirs            []string          `json:"cacheDirs"`
	RequiredBins         []string          `json:"requiredBins"`
	BuildInitEnabled     bool              `json:"buildInitEnabled"`
	BuildInitCommand     *string           `json:"buildInitCommand"`
	ProvisionScript      bool              `json:"provisionScript"`
}

// ConfigJSON prints the repository values resolved by Config as one JSON object.
func ConfigJSON(ctx *kit.Context, w io.Writer) error {
	c := ctx.Config
	suffix, _ := ctx.BranchSuffix()
	names, _ := ctx.TypeNames()
	if names == nil {
		names = map[string]string{}
	}
	return jsonout.WriteJSON(w, configResult{
		Schema: 1, SchemaVersion: schema.VersionOf("config"), Command: "config",
		Repo: ctx.Repo.Name, MainRoot: ctx.Repo.MainRoot, Configured: ctx.Configured(),
		Trunk: jsonout.Strp(c.MainBranch), TrunkSource: ctx.TrunkSource(),
		BranchPrefix: jsonout.Strp(c.BranchPrefix), TypeSuffix: jsonout.Strp(c.TypeSuffix),
		BranchSuffix: jsonout.Strp(suffix), DefaultType: jsonout.Strp(c.DefaultType),
		Types: append([]string{}, c.Types...), TypeNames: names,
		DeveloperConfigDirs:  append([]string{}, c.DeveloperConfigDirs...),
		DeveloperConfigFiles: append([]string{}, c.DeveloperConfigFiles...),
		CacheDirs:            append([]string{}, c.CacheDirs...), RequiredBins: append([]string{}, c.RequiredBins...),
		BuildInitEnabled: c.BuildInitEnabled, BuildInitCommand: jsonout.Strp(c.BuildInitCommand),
		ProvisionScript: ctx.HasProvisionScript(),
	})
}
