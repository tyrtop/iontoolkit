package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/write"
	"tyrtop.com/iontk/internal/tutorial"
	"tyrtop.com/iontk/internal/ui"
	"tyrtop.com/iontk/internal/view"
	"tyrtop.com/iontk/internal/workflow"
)

var KeybindsFile string

func within(path, root string) bool {
	if root == "" {
		return false
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func savable(st *config.Store, file string) (string, error) {
	path, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	roots := []string{ViewsFolder, ActionsFolder, WorkflowsFolder, TutorialFile,
		ColumnsFile, WritesFile, FixturesFile, ThemesFile, KeybindsFile}
	roots = append(roots, WorkflowPaths...)
	if st != nil {
		roots = append(roots, st.Root)
	}
	for _, r := range roots {
		if within(path, r) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s is not a file iontk reads its configuration from", file)
}

func saveCommand() runtime.Command {
	return runtime.Command{
		Name: "config.save", Help: "write one of iontk's config files and read the YAML again: file= text=",
		Args:      []runtime.Arg{{Name: "file", Required: true}, {Name: "text", Required: true}},
		FrontOnly: true,
		Run: func(rt *runtime.Runtime, c *runtime.Call) error {
			path, err := savable(rt.Store(), c.Arg("file"))
			if err != nil {
				return err
			}
			if err := checkFile(rt.Store(), path, []byte(c.Arg("text"))); err != nil {
				return err
			}
			if err := config.WriteFile(path, []byte(c.Arg("text"))); err != nil {
				return err
			}
			c.Result = Reload(rt)
			c.Say("saved %s", path)
			return nil
		},
	}
}

func checkFile(st *config.Store, path string, b []byte) error {
	name := filepath.Base(path)
	is := func(p string) bool {
		abs, err := filepath.Abs(p)
		return p != "" && err == nil && abs == path
	}
	var err error
	switch {
	case name == workflow.ManifestName:
		_, err = workflow.Parse(filepath.Dir(path), b)
	case is(FixturesPath(st)):
		_, err = fixture.Parse(b, name)
	case is(WritesFile) || is(WritesPath(st)):
		_, err = write.Parse(b)
	case is(ColumnsPath(st)):
		_, err = parseList(b)
	case within(path, TutorialsPath(st)):
		_, err = tutorial.Parse(b, name)
	case within(path, ThemesPath(st)):
		_, err = ui.ParseThemes(b)
	case name == dataview.FileName:
		_, err = dataview.Parse(b)
	case within(path, ViewsDir(st)):
		err = checkView(b)
	}
	return err
}

func checkView(b []byte) error {
	kind, err := view.KindOf(b)
	if err != nil {
		return err
	}
	switch kind {
	case view.KindList:
		_, err = parseList(b)
	case view.KindData:
		_, err = dataview.ParseOne(b)
	case view.KindMenu:
	case "":
		err = fmt.Errorf("a file under %s/ says what it is: kind: %s", view.DirName, strings.Join(view.Kinds(), ", "))
	default:
		err = fmt.Errorf("this file is a %q, which iontk does not draw; the kinds are %s", kind, strings.Join(view.Kinds(), ", "))
	}
	return err
}
