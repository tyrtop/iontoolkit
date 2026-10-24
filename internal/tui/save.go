package tui

func (m tuiModel) saveBatchResults() string {
	dir := m.cfg().BatchOutDir()
	if dir == "" {
		return ""
	}
	where, err := m.runtime().SaveBatch("", dir, m.batchFormat(), m.batchOrient())
	if err != nil {
		return "save failed: " + err.Error()
	}
	return "saved to " + where
}
