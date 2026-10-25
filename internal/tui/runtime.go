package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"tyrtop.com/iontk/internal/app"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/view"
)

func ViewsDir(st *config.Store) string { return app.ViewsDir(st) }

func legacyViewsPath(st *config.Store) string { return app.LegacyViewsPath(st) }

func (m *tuiModel) drawRuntime(l app.List) {
	st := m.store()
	m.loadColumns(l)

	files, err := view.Load(ViewsDir(st))
	if err != nil {
		logging.L().Warn("views refused", "err", err)
	}

	for _, f := range files {
		switch f.Kind {
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
	for _, e := range []string{m.columnsErr, m.runtime().ViewsErr(), m.menuErr, m.keysErr, m.actionsErr(), m.writesErr()} {
		if e != "" {
			return fmt.Errorf("%s", e)
		}
	}
	return nil
}

func (m tuiModel) writesErr() string {
	_, problem := m.runtime().Writes()
	return problem
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
