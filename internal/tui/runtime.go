package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/script"
	"tyrtop.com/iontk/internal/view"
)

var Views string

func ViewsDir(st *config.Store) string {
	if Views != "" {
		return Views
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, view.DirName)
}

func legacyViewsPath(st *config.Store) string {
	if Views != "" && view.IsFile(Views) {
		return Views
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, dataview.FileName)
}

func (m *tuiModel) loadRuntime(st *config.Store) {
	m.views, m.viewsErr = m.loadDataViews(st)
	m.loadColumns(st)

	files, err := view.Load(ViewsDir(st))
	if err != nil {
		m.viewsErr = joinErr(m.viewsErr, err.Error())
		logging.L().Warn("views refused", "err", err)
	}

	for _, f := range files {
		switch f.Kind {
		case view.KindData:
			v, perr := dataview.ParseOne(f.Bytes)
			if perr != nil {
				m.viewsErr = joinErr(m.viewsErr, filepath.Base(f.Source)+": "+perr.Error())
				continue
			}
			v.Source = f.Source
			m.views.Replace(*v)

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

		case view.KindMenu:
			entries, perr := parseMenu(f.Bytes)
			if perr != nil {
				m.menuErr = joinErr(m.menuErr, filepath.Base(f.Source)+": "+perr.Error())
				continue
			}
			m.menuSpec, m.menuFrom = entries, f.Source
		}
	}
	m.viewFiles = files
	m.loadScripts(st)
	m.loadKeys(st)
}

func ScriptsPath(st *config.Store) string {
	if Scripts != "" {
		return Scripts
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, script.DirName)
}

var Scripts string

func (m *tuiModel) loadScripts(st *config.Store) {
	set, err := script.Load(ScriptsPath(st))
	m.scripts = set
	if err != nil {
		m.scriptsErr = err.Error()
		logging.L().Warn("lua action refused", "err", err)
	}
}

func (m *tuiModel) loadDataViews(st *config.Store) (*dataview.Set, string) {
	set, err := dataview.Load(legacyViewsPath(st))
	if err != nil {
		return set, err.Error()
	}
	return set, ""
}

func (m tuiModel) runtimeErr() error {
	for _, e := range []string{m.columnsErr, m.viewsErr, m.menuErr, m.keysErr, m.scriptsErr} {
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
	dir := ViewsDir(m.store)
	if dir == "" {
		return ""
	}
	n := len(m.viewFiles)
	return fmt.Sprintf("%d files", n)
}
