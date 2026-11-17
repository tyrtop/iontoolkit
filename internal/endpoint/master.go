package endpoint

import (
	_ "embed"
	"sync"
)

//go:embed sdwan.yaml
var sdwanYAML []byte

var master = sync.OnceValue(func() *Set { return MustParse("iontk", sdwanYAML) })

func Master() *Set { return master() }

func (s *Set) All() []Endpoint {
	out := make([]Endpoint, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.byName[name])
	}
	return out
}

func (s *Set) Declares(name string) bool {
	_, ok := s.byName[name]
	return ok
}
