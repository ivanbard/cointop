package cmd

import (
	"fmt"
	"os"
)

// Execute executes the program
func Execute() {
	rootCmd := RootCmd()
	rootCmd.AddCommand(
		VersionCmd(),
		CleanCmd(),
		ResetCmd(),
		HoldingsCmd(),
		PriceCmd(),
		DominanceCmd(),
		APICmd(),
		MCPCmd(),
		DataCmd(),
		ServerCmd(),
		TestCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
