package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"tyrtop.com/iontk/internal/app"
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
		_ = rt.Set("session_cols", strconv.Itoa(cols))
		_ = rt.Set("session_rows", strconv.Itoa(rows))
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
	out := s.Tap()

	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("element %s: make raw: %w", eid, err)
	}
	defer term.Restore(fd, old)

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if i := bytes.IndexByte(buf[:n], 0x1d); i >= 0 {
					if i > 0 {
						_ = s.WriteRaw(buf[:i])
					}
					s.Close()
					return
				}
				if bytes.IndexByte(buf[:n], '\r') >= 0 {
					rt.RecordLine(s)
				}
				if s.WriteRaw(buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	defer func() {
		if err := rt.SaveCommands(); err != nil {
			fmt.Fprintln(os.Stderr, "command history:", err)
		}
	}()
	for data := range out {
		if app.Defaults.Verbose {
			fmt.Printf("%q\r\n", data)
		} else {
			os.Stdout.Write(data)
		}
	}
	if s.Hung() {
		return fmt.Errorf("element %s stopped answering", eid)
	}
	return s.Err()
}
