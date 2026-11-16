package main

import (
	"context"
	"fmt"
	"io"
	"os"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/runtime"
)

func newSessionOpenCmd(use string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   "Open an interactive CLI session to one element",
		Long:    "Attaches your terminal to the element's CLI over a websocket. Ctrl-] disconnects.",
		Example: `  iontk session open 1234567890`,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eid := flagElement
			if len(args) == 1 {
				eid = args[0]
			}
			return runShell(cmd.Context(), eid)
		},
	}
}

var shellCmd = deprecated(newSessionOpenCmd("shell"), "iontk session open")

func runShell(ctx context.Context, eid string) error {
	if flagSearch != "" {
		return fmt.Errorf("a shell takes one element; run `iontk fleet search %q` to find its ID", flagSearch)
	}
	if eid == "" {
		return fmt.Errorf("an element ID is required: iontk session open <element-id>")
	}
	rt := cliRuntime()
	noteTenant()
	if cols, rows, err := term.GetSize(int(os.Stdout.Fd())); err == nil && cols > 0 && rows > 0 {
		rt.SetTerminal(cols, rows)
	}
	say := func(l string) { fmt.Fprintln(os.Stderr, l) }
	if err := rt.Exec(&runtime.Call{Ctx: ctx, Args: map[string]string{"elements": eid}, Out: say}, "session.open"); err != nil {
		return err
	}
	s := rt.Sessions().Find(eid)
	if s == nil {
		return fmt.Errorf("element %s: the session did not open", eid)
	}
	defer s.Close()
	defer func() {
		if err := rt.SaveCommands(); err != nil {
			fmt.Fprintln(os.Stderr, "command history:", err)
		}
	}()

	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("element %s: make raw: %w", eid, err)
	}
	defer term.Restore(fd, old)

	var out io.Writer = os.Stdout
	if app.Defaults.Verbose {
		out = quoted{os.Stdout}
	}
	_, _, err = s.Attach(os.Stdin, out, ionterm.AttachOptions{
		Catch: func(k uv.Key) bool { return k.String() == "ctrl+]" },
		Enter: func() { rt.RecordLine(s) },
	})
	if err != nil {
		return err
	}
	if s.Hung() {
		return fmt.Errorf("element %s stopped responding", eid)
	}
	return s.Err()
}

type quoted struct{ w io.Writer }

func (q quoted) Write(p []byte) (int, error) {
	if _, err := fmt.Fprintf(q.w, "%q\r\n", p); err != nil {
		return 0, err
	}
	return len(p), nil
}
