package auth

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// Option is a selectable menu item.
type Option struct {
	ID          string
	Label       string
	Description string
}

// Prompter collects interactive input.
type Prompter interface {
	Select(label string, options []Option) (string, error)
	Secret(label string) (string, error)
	Text(label string) (string, error)
	Confirm(label string) (bool, error)
}

// TerminalPrompter reads from stdin and writes to out.
type TerminalPrompter struct {
	In  io.Reader
	Out io.Writer
	// FD is used for password input when In is a terminal (default 0 = stdin).
	FD int
}

// Select prints a numbered menu and returns the chosen option ID.
func (p *TerminalPrompter) Select(label string, options []Option) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("no options")
	}
	fmt.Fprintln(p.Out, label)
	fmt.Fprintln(p.Out)
	for i, o := range options {
		line := fmt.Sprintf("  %d. %s", i+1, o.Label)
		if o.Description != "" {
			line += "  — " + o.Description
		}
		fmt.Fprintln(p.Out, line)
	}
	fmt.Fprintln(p.Out)
	fmt.Fprint(p.Out, "> ")
	sc := bufio.NewScanner(p.In)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no selection")
	}
	raw := strings.TrimSpace(sc.Text())
	// allow selecting by ID
	for _, o := range options {
		if strings.EqualFold(raw, o.ID) {
			return o.ID, nil
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > len(options) {
		return "", fmt.Errorf("invalid selection %q", raw)
	}
	return options[n-1].ID, nil
}

// Secret reads a hidden line when possible.
func (p *TerminalPrompter) Secret(label string) (string, error) {
	fmt.Fprint(p.Out, label+": ")
	fd := p.FD
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(p.Out)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	// Non-interactive: read one line (e.g. piped key).
	sc := bufio.NewScanner(p.In)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no secret provided")
	}
	return strings.TrimSpace(sc.Text()), nil
}

// Text reads a visible line.
func (p *TerminalPrompter) Text(label string) (string, error) {
	fmt.Fprint(p.Out, label+": ")
	sc := bufio.NewScanner(p.In)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no input")
	}
	return strings.TrimSpace(sc.Text()), nil
}

// Confirm asks y/n.
func (p *TerminalPrompter) Confirm(label string) (bool, error) {
	fmt.Fprint(p.Out, label+" [y/N]: ")
	sc := bufio.NewScanner(p.In)
	if !sc.Scan() {
		return false, sc.Err()
	}
	v := strings.ToLower(strings.TrimSpace(sc.Text()))
	return v == "y" || v == "yes", nil
}
