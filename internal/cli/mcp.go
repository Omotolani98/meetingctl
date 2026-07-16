package cli

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Inspect meeting MCP access for local AI clients",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show MCP endpoint and daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			st, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "endpoint: %s\n", defaultMCPURL())
			fmt.Fprintf(cmd.OutOrStdout(), "transport: streamable-http\n")
			fmt.Fprintf(cmd.OutOrStdout(), "daemon: %v\n", st["daemon"])
			fmt.Fprintf(cmd.OutOrStdout(), "listen: %v\n", st["listen"])
			fmt.Fprintf(cmd.OutOrStdout(), "transcription: %v\n", st["transcription_provider"])
			fmt.Fprintf(cmd.OutOrStdout(), "analysis: %v\n", st["analysis_provider"])
			if active, _ := st["active_meeting"].(string); active != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "active_meeting: %s\n", active)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "config",
		Short: "Print MCP client configuration hints",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "streamable-http:")
			fmt.Fprintf(cmd.OutOrStdout(), "  url: %s\n", defaultMCPURL())
			fmt.Fprintln(cmd.OutOrStdout(), "  auth: Authorization: Bearer <control-token>")
			fmt.Fprintln(cmd.OutOrStdout(), "stdio:")
			fmt.Fprintln(cmd.OutOrStdout(), "  command: meeting-mcp")
			fmt.Fprintln(cmd.OutOrStdout(), "  auth: none (local process)")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "tools",
		Short: "List MCP tools exposed by meetingctl",
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, name := range []string{
				"get_active_meeting",
				"get_meeting",
				"get_transcript",
				"get_action_items",
				"get_decisions",
				"add_manual_note",
				"correct_transcript_segment",
				"finalize_meeting",
			} {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	})
	return cmd
}

func defaultMCPURL() string {
	if listen := strings.TrimSpace(os.Getenv("MEETINGCTL_MCP_LISTEN")); listen != "" {
		return "http://" + listen + "/mcp"
	}
	listen := strings.TrimSpace(os.Getenv("MEETINGCTL_LISTEN"))
	if listen == "" {
		listen = "127.0.0.1:7337"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://127.0.0.1:7338/mcp"
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return "http://127.0.0.1:7338/mcp"
	}
	return fmt.Sprintf("http://%s:%d/mcp", host, portNum+1)
}
