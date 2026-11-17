package app

import (
	"path/filepath"
	"strings"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/view"
)

var ViewsFolder string

func ViewsDir(st *config.Store) string {
	if ViewsFolder != "" {
		return ViewsFolder
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, view.DirName)
}

func LegacyViewsPath(st *config.Store) string {
	if ViewsFolder != "" && view.IsFile(ViewsFolder) {
		return ViewsFolder
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, dataview.FileName)
}

func LoadDataViews(st *config.Store) (*dataview.Set, string) {
	var problems []string
	set, err := dataview.Load(LegacyViewsPath(st))
	if err != nil {
		problems = append(problems, err.Error())
	}
	if set == nil {
		return nil, strings.Join(problems, "; ")
	}
	files, err := view.Load(ViewsDir(st))
	if err != nil {
		problems = append(problems, err.Error())
	}
	for _, f := range files {
		if f.Kind != view.KindData {
			continue
		}
		v, perr := dataview.ParseOne(f.Bytes)
		if perr != nil {
			problems = append(problems, filepath.Base(f.Source)+": "+perr.Error())
			continue
		}
		v.Source = f.Source
		set.Replace(*v)
	}
	return set, strings.Join(problems, "; ")
}

func ReloadViews(rt *runtime.Runtime) {
	rt.SetViews(LoadDataViews(rt.Store()))
}
