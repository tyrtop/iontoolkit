package app

import (
	"path/filepath"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/write"
	"tyrtop.com/iontk/internal/workflow"
)

func WriteGuard(st *config.Store, cfg *config.Config, tenant string) write.Guard {
	g := write.Guard{Tenant: tenant}
	if st != nil {
		g.Log = filepath.Join(st.Root, write.LogName)
	}
	return g
}

func WritesPath(st *config.Store) string {
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, write.FileName)
}

func ReloadWrites(rt *runtime.Runtime) {
	set, err := LoadWrites(rt.Store(), WritesFile)
	problem := ""
	if err != nil {
		problem = err.Error()
		logging.L().Warn("writes refused", "err", err)
	}
	rt.SetWrites(set, problem)
}

func LoadWrites(st *config.Store, override string) (*write.Set, error) {
	if override != "" {
		return write.Load(override)
	}
	return write.Load(WritesPath(st))
}

func WorkflowsDir(st *config.Store) string {
	if st == nil {
		return ""
	}
	return workflow.Dir(st.Root)
}

func LoadWorkflows(st *config.Store, override string) ([]workflow.Workflow, error) {
	if override != "" {
		return workflow.Load(override)
	}
	return workflow.Load(WorkflowsDir(st))
}
