package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"tyrtop.com/iontk/internal/app"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/view"
)

func ViewsDir(st *config.Store) string { return app.ViewsDir(st) }

func legacyViewsPath(st *config.Store) string { return app.LegacyViewsPath(st) }

func (m *tuiModel) loadRuntime(st *config.Store) {
	m.runtime().SetViews(app.LoadDataViews(st))
	m.loadColumns(st)

	files, err := view.Load(ViewsDir(st))
	if err != nil {
		logging.L().Warn("views refused", "err", err)
	}

	for _, f := range files {
		switch f.Kind {
		case view.KindList:
			set, perr := column.Parse(f.Bytes)
			if perr != nil {
				m.columnsErr = joinErr(m.columnsErr, filepath.Base(f.Source)+": "+perr.Error())
				continue
			}
			base, _ := defaultColumns()
			cols, berr := buildColumns(set, base)
			if berr != nil {
				m.columnsErr = joinErr(m.columnsErr, filepath.Base(f.Source)+": "+berr.Error())
				continue
			}
			m.listSpec, m.listFrom = cols, f.Source
			m.runtime().SetColumns(set.Columns)

		case view.KindMenu:
			entries, perr := parseMenu(f.Bytes, m.runsCommand)
			if perr != nil {
				m.menuErr = joinErr(m.menuErr, filepath.Base(f.Source)+": "+perr.Error())
				continue
			}
			m.menuSpec, m.menuFrom = entries, f.Source
		}
	}
	m.viewFiles = files
	m.loadWrites(st)
	app.ReloadCommands(m.runtime())
	m.loadKeys(st)
}

func (m tuiModel) actionNames() []string {
	var out []string
	for _, c := range m.runtime().CommandsNamed(app.ActionPrefix) {
		out = append(out, strings.TrimPrefix(c.Name, app.ActionPrefix))
	}
	return out
}

func (m tuiModel) hasAction(name string) bool {
	_, ok := m.runtime().Command(app.ActionPrefix + name)
	return ok
}

func (m tuiModel) actionsErr() string {
	var out []string
	for _, w := range m.runtime().Warnings() {
		if rest, ok := strings.CutPrefix(w, app.ActionsRefused); ok {
			out = append(out, rest)
		}
	}
	return strings.Join(out, "; ")
}

func (m tuiModel) runtimeErr() error {
	for _, e := range []string{m.columnsErr, m.runtime().ViewsErr(), m.menuErr, m.keysErr, m.actionsErr(), m.writesErr} {
		if e != "" {
			return fmt.Errorf("%s", e)
		}
	}
	return nil
}

func joinErr(have, add string) string {
	switch {
	case have == "":
		return add
	case strings.Contains(have, add):
		return have
	}
	return have + "; " + add
}

func (m tuiModel) sourceOf(kind, name string) (string, bool) {
	for _, f := range m.viewFiles {
		if f.Kind == kind && (name == "" || strings.EqualFold(f.Name, name)) {
			return f.Source, true
		}
	}
	return "", false
}

func (m tuiModel) viewFileCount(kind string) int {
	n := 0
	for _, f := range m.viewFiles {
		if f.Kind == kind {
			n++
		}
	}
	return n
}

func (m tuiModel) runtimeLine() string {
	dir := ViewsDir(m.store())
	if dir == "" {
		return ""
	}
	n := len(m.viewFiles)
	return fmt.Sprintf("%d files", n)
}
