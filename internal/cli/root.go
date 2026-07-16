package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewRoot builds the meetingctl command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "meetingctl",
		Short: "Local-first meeting memory via MCP",
		Long: `meetingctl captures meetings, stores encrypted transcript memory,
and exposes it through an MCP server.

Set MEETINGCTL_ENCRYPTION_KEY to a 32-byte key (64 hex characters).`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newStartCmd(),
		newStatusCmd(),
		newNoteCmd(),
		newMarkCmd(),
		newWatchCmd(),
		newStopCmd(),
		newMeetingsCmd(),
		newDeleteCmd(),
		newKeygenCmd(),
		newDoctorCmd(),
		newAuthCmd(),
		newMCPCmd(),
		newUpdateCmd(),
	)
	return root
}

// Execute runs the root command.
func Execute() {
	if err := NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
