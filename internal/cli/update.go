package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	commandContext = exec.CommandContext
	findGoTool     = exec.LookPath
	getExecutable  = os.Executable
)

func newUpdateCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update meetingctl and companion binaries",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, version)
		},
	}
	cmd.Flags().StringVar(&version, "version", "latest", "module version to install")
	return cmd
}

func runUpdate(cmd *cobra.Command, version string) error {
	version = updateVersionLabel(version)
	goBin, err := findGoTool("go")
	if err != nil {
		return fmt.Errorf("go toolchain not found: %w", err)
	}
	binDir, err := installDir()
	if err != nil {
		return err
	}
	for _, pkg := range updatePackages() {
		if err := installPackage(cmd.Context(), goBin, binDir, pkg, version, cmd.OutOrStdout(), cmd.OutOrStderr()); err != nil {
			return err
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "updated meetingctl to %s in %s\n", version, binDir)
	fmt.Fprintln(cmd.OutOrStdout(), "restart meetingd if it is running")
	return nil
}

func updatePackages() []string {
	return []string{
		"github.com/Omotolani98/meetingctl/cmd/meetingctl",
		"github.com/Omotolani98/meetingctl/cmd/meetingd",
		"github.com/Omotolani98/meetingctl/cmd/meeting-mcp",
	}
}

func installPackage(ctx context.Context, goBin, binDir, pkg, version string, stdout, stderr io.Writer) error {
	cmd := commandContext(ctx, goBin, "install", pkg+"@"+version)
	cmd.Env = append(os.Environ(), "GOBIN="+binDir)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install %s: %w", pkg, err)
	}
	return nil
}

func installDir() (string, error) {
	exe, err := getExecutable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func updateVersionLabel(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "latest"
	}
	return v
}
