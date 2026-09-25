package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/schema"
)

func newSchemaCmd() *cobra.Command {
	var names []string
	for _, d := range schema.All() {
		names = append(names, d.Name)
	}
	return &cobra.Command{
		Use:   "schema [<command>]",
		Short: "Print the JSON Schema of a command's --json output",
		Long: "Print the JSON Schema (draft 2020-12) of a command's --json output, for a\n" +
			"tool that drives wt: to validate what it reads, or to generate its types.\n" +
			"The schemas are part of this binary, so they describe exactly the output\n" +
			"of the wt that prints them; validate against that one, since a newer wt\n" +
			"may add fields and enumeration values within the same schema number.\n" +
			"Each schema's $id is where it is published in the wt repository.\n\n" +
			"With no command it lists the schemas there are. docs/json.md explains\n" +
			"the fields and the rules.\n\n" +
			"Schemas: " + strings.Join(names, ", "),
		Example: "  wt schema             # the schemas there are, and their ids\n" +
			"  wt schema up          # the schema of wt up --json\n" +
			"  wt schema status > wt-status.schema.json  # save one for a validator",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: names,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				for _, d := range schema.All() {
					fmt.Fprintf(out, "%-8s %-18s %s\n", d.Name, "wt "+d.Name+" --json", d.ID())
				}
				return nil
			}
			d, ok := schema.Get(args[0])
			if !ok {
				return fmt.Errorf("no schema for %q; there are: %s", args[0], strings.Join(names, ", "))
			}
			_, err := out.Write(d.Bytes)
			return err
		},
	}
}
