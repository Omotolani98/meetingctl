package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Omotolani98/meetingctl/internal/client"
	"github.com/Omotolani98/meetingctl/internal/crypto"
	"github.com/Omotolani98/meetingctl/internal/meetings"
	"github.com/spf13/cobra"
)

func openClient(cmd *cobra.Command) (*client.Client, error) {
	c, err := client.Load()
	if err != nil {
		return nil, err
	}
	if err := c.Health(cmd.Context()); err != nil {
		return nil, err
	}
	return c, nil
}

func newStartCmd() *cobra.Command {
	var (
		title        string
		participants string
		source       string
		input        string
	)
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Begin recording a meeting via meetingd",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			out, err := c.StartMeeting(cmd.Context(), title, meetings.ParseParticipants(participants), source, input)
			if err != nil {
				return err
			}
			m, _ := out["meeting"].(map[string]any)
			fmt.Fprintf(cmd.OutOrStdout(), "started meeting %v\n", m["id"])
			fmt.Fprintf(cmd.OutOrStdout(), "  title: %v\n", m["title"])
			fmt.Fprintf(cmd.OutOrStdout(), "  status: %v\n", m["status"])
			if n, ok := out["ingested_segments"]; ok {
				fmt.Fprintf(cmd.OutOrStdout(), "  ingested segments: %v\n", n)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "meeting title")
	cmd.Flags().StringVar(&participants, "participants", "", "comma-separated participant names")
	cmd.Flags().StringVar(&source, "source", "none", "audio source: none | fixture | mic | mic+system")
	cmd.Flags().StringVar(&input, "input", "", "fixture directory (with --source=fixture)")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon and active meeting status",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			st, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "daemon: %v\n", st["daemon"])
			fmt.Fprintf(cmd.OutOrStdout(), "  listen: %v\n", st["listen"])
			fmt.Fprintf(cmd.OutOrStdout(), "  transcription: %v\n", st["transcription_provider"])
			fmt.Fprintf(cmd.OutOrStdout(), "  analysis: %v\n", st["analysis_provider"])
			if id, _ := st["active_meeting"].(string); id != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  active_meeting: %s\n", id)
				fmt.Fprintf(cmd.OutOrStdout(), "  source: %v\n", st["source"])
				fmt.Fprintf(cmd.OutOrStdout(), "  ingesting: %v\n", st["ingesting"])
				m, err := c.GetCurrent(cmd.Context())
				if err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  title: %v\n", m["title"])
					fmt.Fprintf(cmd.OutOrStdout(), "  status: %v\n", m["status"])
				}
				tr, err := c.GetTranscript(cmd.Context(), id, 0, 500, "")
				if err == nil {
					if segs, ok := tr["segments"].([]any); ok {
						fmt.Fprintf(cmd.OutOrStdout(), "  segments: %d\n", len(segs))
						if len(segs) > 0 {
							last := segs[len(segs)-1].(map[string]any)
							fmt.Fprintf(cmd.OutOrStdout(), "  last_sequence: %v\n", last["sequence"])
						}
					}
				}
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "  no active meeting")
			}
			if e, _ := st["last_error"].(string); e != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  last_error: %s\n", e)
			}
			return nil
		},
	}
}

func newNoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "note [text]",
		Short: "Add a manual note to the active meeting",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			note := strings.Join(args, " ")
			n, err := c.AddNote(cmd.Context(), "current", note)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "note %v added to %v\n", n["id"], n["meeting_id"])
			return nil
		},
	}
}

func newMarkCmd() *cobra.Command {
	var (
		text  string
		owner string
	)
	cmd := &cobra.Command{
		Use:   "mark [type]",
		Short: "Mark an important moment (decision, action-item, ...)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := meetings.ParseMarkType(args[0]); err != nil {
				return err
			}
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			if text == "" {
				text = args[0]
			}
			ins, err := c.Mark(cmd.Context(), "current", args[0], text, owner)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "marked %v (%v) on %v\n", ins["type"], ins["id"], ins["meeting_id"])
			return nil
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "insight text")
	cmd.Flags().StringVar(&owner, "owner", "", "optional owner")
	return cmd
}

func newWatchCmd() *cobra.Command {
	var since int64
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream new finalized transcript segments",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			m, err := c.GetCurrent(ctx)
			if err != nil {
				return err
			}
			id, _ := m["id"].(string)
			fmt.Fprintf(cmd.OutOrStdout(), "watching %s (ctrl-c to stop)\n", id)
			seq := since
			for {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				tr, err := c.GetTranscript(ctx, id, seq, 50, "")
				if err != nil {
					return err
				}
				if segs, ok := tr["segments"].([]any); ok {
					for _, raw := range segs {
						seg := raw.(map[string]any)
						speaker, _ := seg["speaker"].(string)
						if speaker == "" {
							speaker = "?"
						}
						fmt.Fprintf(cmd.OutOrStdout(), "[%v] %s: %v\n", seg["sequence"], speaker, seg["text"])
						if s, ok := seg["sequence"].(float64); ok {
							seq = int64(s)
						}
					}
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(500 * time.Millisecond):
				}
			}
		},
	}
	cmd.Flags().Int64Var(&since, "since", 0, "start after this sequence number")
	return cmd
}

func newStopCmd() *cobra.Command {
	var (
		meetingID string
		input     string
	)
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Finish the meeting and generate summary/insights",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			out, err := c.StopMeeting(cmd.Context(), meetingID, input)
			if err != nil {
				return err
			}
			m, _ := out["meeting"].(map[string]any)
			fmt.Fprintf(cmd.OutOrStdout(), "stopped meeting %v (%v)\n", m["id"], m["status"])
			if sum, ok := out["summary"].(map[string]any); ok {
				fmt.Fprintf(cmd.OutOrStdout(), "summary v%v (through seq %v):\n%v\n",
					sum["version"], sum["through_sequence"], sum["text"])
			}
			actions, err := c.GetActionItems(cmd.Context(), fmt.Sprint(m["id"]))
			if err == nil {
				if items, ok := actions["items"].([]any); ok && len(items) > 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "action items:")
					for _, raw := range items {
						it := raw.(map[string]any)
						owner, _ := it["owner"].(string)
						if owner == "" {
							owner = "unassigned"
						}
						fmt.Fprintf(cmd.OutOrStdout(), "  - [%s] %v\n", owner, it["text"])
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&meetingID, "meeting", "", "meeting id (default: active)")
	cmd.Flags().StringVar(&input, "input", "", "fixture directory for analysis (optional)")
	return cmd
}

func newMeetingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "meetings",
		Short: "List recent meetings",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			out, err := c.ListMeetings(cmd.Context())
			if err != nil {
				return err
			}
			list, _ := out["meetings"].([]any)
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no meetings")
				return nil
			}
			for _, raw := range list {
				m := raw.(map[string]any)
				ended := m["ended_at"]
				if ended == nil {
					ended = "-"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%v  %-12v  %v  started=%v ended=%v\n",
					m["id"], m["status"], m["title"], m["started_at"], ended)
			}
			return nil
		},
	}
}

func newDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete [meeting-id]",
		Short: "Permanently delete a meeting",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete without --yes")
			}
			c, err := openClient(cmd)
			if err != nil {
				return err
			}
			if err := c.DeleteMeeting(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm permanent deletion")
	return cmd
}

func newKeygenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen",
		Short: "Generate a MEETINGCTL_ENCRYPTION_KEY value",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := crypto.GenerateKey()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), key)
			return nil
		},
	}
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check meetingd connectivity and configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client.Load()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "daemon url: %s\n", c.BaseURL)
			if c.Token == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "control token: MISSING (expected ~/.meetingctl/control.token)")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "control token: present")
			}
			if err := c.Health(cmd.Context()); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "health: FAIL (%v)\n", err)
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "health: ok")
			st, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "transcription: %v\n", st["transcription_provider"])
			fmt.Fprintf(cmd.OutOrStdout(), "analysis: %v\n", st["analysis_provider"])
			return nil
		},
	}
}


