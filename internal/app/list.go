package app

import (
	"path/filepath"
	"strings"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/view"
)

var ColumnsFile string

func ColumnsPath(st *config.Store) string {
	if ColumnsFile != "" {
		return ColumnsFile
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, column.FileName)
}

type List struct {
	Set  *column.Set
	From string
	Err  string
}

func LoadList(st *config.Store) List {
	var out List
	var problems []string
	if set, err := column.Load(ColumnsPath(st)); err != nil {
		problems = append(problems, err.Error())
	} else if err := checkList(set); err != nil {
		problems = append(problems, filepath.Base(ColumnsPath(st))+": "+err.Error())
	} else if set != nil {
		out.Set, out.From = set, ColumnsPath(st)
	}
	files, err := view.Load(ViewsDir(st))
	if err != nil {
		problems = append(problems, err.Error())
	}
	for _, f := range files {
		if f.Kind != view.KindList {
			continue
		}
		set, err := parseList(f.Bytes)
		if err != nil {
			problems = append(problems, filepath.Base(f.Source)+": "+err.Error())
			continue
		}
		out.Set, out.From = set, f.Source
	}
	if out.Set == nil {
		out.Set, _ = column.Builtin()
		out.From = ""
	}
	out.Err = strings.Join(problems, "; ")
	return out
}

func parseList(b []byte) (*column.Set, error) {
	set, err := column.Parse(b)
	if err != nil {
		return nil, err
	}
	return set, checkList(set)
}

func checkList(set *column.Set) error {
	if set == nil {
		return nil
	}
	return runtime.CheckJoins(set)
}

func UseList(rt *runtime.Runtime, l List) {
	if l.Set == nil {
		return
	}
	rt.SetColumns(l.Set.Columns)
	rt.SetListCalls(l.Set.Calls())
}
