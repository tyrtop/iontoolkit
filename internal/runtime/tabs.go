package runtime

import "fmt"

const (
	FrontList    = "list"
	FrontSession = "session"
	FrontData    = "data"
)

func (rt *Runtime) Tabs(front string) (count, current int) {
	sessions := rt.Sessions().Count()
	count = 1 + sessions + len(rt.DataTabs())
	switch front {
	case FrontSession:
		current = 1 + rt.Sessions().ActiveIndex()
	case FrontData:
		current = 1 + sessions + rt.DataActive()
	}
	return count, current
}

func (rt *Runtime) SelectTab(i int) string {
	n, _ := rt.Tabs(FrontList)
	i = ((i % n) + n) % n
	sessions := rt.Sessions().Count()
	switch {
	case i == 0:
		return FrontList
	case i <= sessions:
		rt.Sessions().Select(i - 1)
		return FrontSession
	}
	rt.SelectData(i - 1 - sessions)
	return FrontData
}

func tabCommands() []Command {
	step := func(by int) func(rt *Runtime, c *Call) error {
		return func(rt *Runtime, c *Call) error {
			front := c.Arg("from")
			switch front {
			case FrontList, FrontSession, FrontData:
			default:
				return fmt.Errorf("from= is list, session or data, not %q", front)
			}
			_, cur := rt.Tabs(front)
			c.Result = rt.SelectTab(cur + by)
			c.Say("%s", c.Result)
			return nil
		}
	}
	from := Arg{Name: "from", Default: FrontList, Help: "the tab in front now: list, session or data"}
	return []Command{
		{Name: "tab.next", Help: "the next tab: the list, then each session, then each data tab", Args: []Arg{from}, Run: step(1)},
		{Name: "tab.prev", Help: "the tab before", Args: []Arg{from}, Run: step(-1)},
	}
}
