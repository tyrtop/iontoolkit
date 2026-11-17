package tui

func (m *tuiModel) setCacheState(c cacheState) {
	for name, s := range cacheStates {
		if s == c {
			m.runtime().SetFleetState(name)
		}
	}
}

func (m *tuiModel) setMode(mode string) { m.runtime().SetMode(mode) }
