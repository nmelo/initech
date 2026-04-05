package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/nmelo/initech/internal/config"
	"github.com/spf13/cobra"
)

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all config keys with type, default, and description",
	Long: `Lists every available configuration field. Does not require an
initech.yaml file — it reads from the built-in field registry.`,
	Args: cobra.NoArgs,
	RunE: runConfigList,
}

func init() {
	configCmd.AddCommand(configListCmd)
}

func runConfigList(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)

	fmt.Fprintf(w, "KEY\tTYPE\tDEFAULT\tDESCRIPTION\n")
	for _, f := range config.AllFields() {
		def := f.Default
		if def == "" {
			def = "-"
		}
		desc := f.Description
		if f.EnvVar != "" {
			desc += fmt.Sprintf(" (env: %s)", f.EnvVar)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Key, f.Type, def, desc)
	}
	w.Flush()
	return nil
}
