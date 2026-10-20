package tui

import (
	"time"

	"tyrtop.com/iontk/internal/output"
)

func (m tuiModel) saveBatchResults() string {
	dir := m.cfg.BatchOutDir()
	if dir == "" {
		return ""
	}

	format := m.batchFormat()
	out, where, err := output.Open("", dir, format, time.Now())
	if err != nil {
		return "save failed: " + err.Error()
	}
	defer out.Close()

	sink := output.NewSink(out, format, output.WithOrientation(m.batchOrient()))
	for i, r := range m.results {
		if r.ElementID == "" {
			continue
		}
		if err := sink.Add(i, r); err != nil {
			return "save failed: " + err.Error()
		}
	}
	if err := sink.Close(); err != nil {
		return "save failed: " + err.Error()
	}
	return "saved to " + where
}
