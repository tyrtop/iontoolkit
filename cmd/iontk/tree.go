package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/runtime"
)

const ownCommand = "iontk/own"

func own(c *cobra.Command) *cobra.Command {
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[ownCommand] = "yes"
	return c
}

func addOwn(root *cobra.Command, path string, c *cobra.Command) {
	words := strings.Fields(path)
	parent := root
	for _, w := range words[:len(words)-1] {
		parent = group(parent, w)
	}
	parent.AddCommand(own(c))
}

func addRegistry(root *cobra.Command, in io.Reader, cmds []runtime.Command) {
	for _, rc := range cmds {
		words := strings.Split(rc.Name, ".")
		parent := root
		for _, w := range words[:len(words)-1] {
			parent = group(parent, w)
		}
		leaf := words[len(words)-1]
		c := child(parent, leaf)
		if c == nil {
			c = &cobra.Command{Use: leaf}
			parent.AddCommand(c)
		} else if c.Annotations[ownCommand] == "yes" {
			continue
		}
		fromRuntime(c, rc, in)
	}
	describeGroups(root)
}

func child(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func group(parent *cobra.Command, name string) *cobra.Command {
	if c := child(parent, name); c != nil {
		return c
	}
	c := &cobra.Command{Use: name, Args: cobra.NoArgs}
	parent.AddCommand(c)
	return c
}

func describeGroups(c *cobra.Command) {
	for _, sub := range c.Commands() {
		describeGroups(sub)
	}
	if c.Short != "" || !c.HasSubCommands() || c.Parent() == nil {
		return
	}
	var names []string
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() {
			names = append(names, sub.Name())
		}
	}
	sort.Strings(names)
	c.Short = strings.Join(names, ", ")
}

func fromRuntime(c *cobra.Command, rc runtime.Command, in io.Reader) {
	c.Short = rc.Help
	if rc.Writes {
		c.Short += " (dry run unless --doit)"
	}
	c.Long = rc.Help
	if rc.Long != "" {
		c.Long = rc.Long
	}
	c.Args = cobra.ArbitraryArgs

	f := c.Flags()
	strs := map[string]*string{}
	bools := map[string]*bool{}
	for _, a := range rc.Args {
		if a.Name == "help" || a.Name == "doit" {
			continue
		}
		help := a.Help
		if a.Required {
			help = strings.TrimSpace(help + " (required)")
		}
		if a.Bool {
			bools[a.Name] = f.Bool(a.Name, false, help)
			continue
		}
		strs[a.Name] = f.String(a.Name, a.Default, help)
	}
	var doit *bool
	if rc.Writes {
		doit = f.Bool("doit", false, "send the writes; without it the run is a dry run that only lists them")
	}

	name, declared := rc.Name, rc.Args
	c.RunE = func(cmd *cobra.Command, words []string) error {
		args := map[string]string{}
		for n, v := range strs {
			if cmd.Flags().Changed(n) {
				args[n] = *v
			}
		}
		for n, v := range bools {
			if cmd.Flags().Changed(n) && *v {
				args[n] = "yes"
			}
		}
		if err := readDashes(in, declared, args); err != nil {
			return err
		}
		for n, v := range args {
			if path, ok := strings.CutPrefix(v, "@"); ok && path != "" {
				b, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("--%s: %w", n, err)
				}
				args[n] = string(b)
			}
		}
		return execPrinting(cmd.Context(), cmd.OutOrStdout(), name, args, words, doit != nil && *doit)
	}
}

func readDashes(in io.Reader, declared []runtime.Arg, args map[string]string) error {
	var lines *bufio.Scanner
	for _, a := range declared {
		if args[a.Name] != "-" {
			continue
		}
		if lines == nil {
			lines = bufio.NewScanner(in)
		}
		if !lines.Scan() {
			return fmt.Errorf("--%s - found nothing to read on standard input", a.Name)
		}
		args[a.Name] = strings.TrimRight(lines.Text(), "\r")
	}
	return nil
}

func buildTree() {
	addRegistry(rootCmd, os.Stdin, cliRuntime().Commands())
	aliasWorkflowPaths(rootCmd)
}

func aliasWorkflowPaths(root *cobra.Command) {
	wf := child(root, "workflow")
	if wf == nil {
		return
	}
	for name, path := range workflowAliases {
		if c := child(wf, name); c != nil {
			c.Aliases = append(c.Aliases, path)
		}
	}
}
