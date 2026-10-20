package tui

import (
	"context"
	"fmt"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
)

func fleetModel(b *testing.B, n int) tuiModel {
	b.Helper()
	m := benchModel(b)
	els := make([]api.Element, 0, n)
	for i := range n {
		els = append(els, api.Element{
			ID: fmt.Sprint(i), Name: fmt.Sprintf("store-%04d-rtr-%02d", i/2, i%2+1),
			Model: "ion 1200", Serial: fmt.Sprintf("ION1200%07d", i),
			SiteID: fmt.Sprint(i / 2), Connected: i%7 != 0,
			SpokeHA: &api.SpokeHA{ClusterID: fmt.Sprint(i / 2), Enable: true},
		})
	}
	setTestElements(&m, els)
	m.sites = map[string]string{}
	for i := range n / 2 {
		m.sites["1/"+fmt.Sprint(i)] = fmt.Sprintf("Store %04d", i)
	}
	m.phase = phasePicking
	m.width, m.height = 160, 40
	m.rebuildRows()
	return m
}

func BenchmarkFilterKeystroke(b *testing.B) {
	m := fleetModel(b, 5600)
	queries := []string{"s", "st", "sto", "stor", "store-0", "store-01", "store-012"}
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		m.filter.SetValue(queries[i%len(queries)])
		m.applyFilter()
	}
}

func BenchmarkRebuildRows(b *testing.B) {
	m := fleetModel(b, 5600)
	b.ResetTimer()
	for b.Loop() {
		m.rebuildRows()
	}
}

func BenchmarkCursorMove(b *testing.B) {
	m := fleetModel(b, 5600)
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		m.moveCursorTo(i % 200)
	}
}

func benchModel(b *testing.B) tuiModel {
	b.Helper()
	root := b.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(root+"/secrets.json"))
	cfg := &config.Config{Tenants: []config.Tenant{{
		Name: "Acme Co", TSGID: "1", Users: []config.IONUser{{Name: "u", Default: true}},
	}}}
	m := newTUIModel(context.Background(), st, cfg)
	m.tenant, m.hasTenant = cfg.Tenants[0], true
	return m
}
