package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const HistoryMax = 200

const FavouriteSlots = 9

type CommandStore struct {
	History      []CommandUse `json:"history,omitempty"`
	Favourites   []Favourite  `json:"favourites,omitempty"`
	Macros       []Macro      `json:"macros,omitempty"`
	Acknowledged []string     `json:"acknowledged,omitempty"`
}

type CommandUse struct {
	Command  string    `json:"command"`
	Model    string    `json:"model,omitempty"`
	Uses     int       `json:"uses"`
	LastUsed time.Time `json:"last_used"`
}

type Favourite struct {
	Slot    int    `json:"slot"`
	Command string `json:"command"`
	Model   string `json:"model,omitempty"`
}

type Macro struct {
	Name     string   `json:"name"`
	Commands []string `json:"commands"`
	Model    string   `json:"model,omitempty"`
}

func (c *CommandStore) Record(command, model string, at time.Time) {
	command = strings.TrimSpace(command)
	if c == nil || command == "" {
		return
	}
	for i := range c.History {
		if c.History[i].Command == command && c.History[i].Model == model {
			c.History[i].Uses++
			c.History[i].LastUsed = at
			return
		}
	}
	c.History = append(c.History, CommandUse{Command: command, Model: model, Uses: 1, LastUsed: at})
	c.trimHistory()
}

func (c *CommandStore) trimHistory() {
	if len(c.History) <= HistoryMax {
		return
	}
	sort.SliceStable(c.History, func(i, j int) bool {
		return c.History[i].LastUsed.After(c.History[j].LastUsed)
	})
	c.History = c.History[:HistoryMax]
}

func (c *CommandStore) Ranked(model string) []CommandUse {
	if c == nil {
		return nil
	}
	out := make([]CommandUse, len(c.History))
	copy(out, c.History)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if mine := a.Model == model; mine != (b.Model == model) {
			return mine
		}
		if a.Uses != b.Uses {
			return a.Uses > b.Uses
		}
		if !a.LastUsed.Equal(b.LastUsed) {
			return a.LastUsed.After(b.LastUsed)
		}
		return a.Command < b.Command
	})
	return out
}

func (c *CommandStore) Forget(command, model string) bool {
	if c == nil {
		return false
	}
	for i := range c.History {
		if c.History[i].Command == command && c.History[i].Model == model {
			c.History = append(c.History[:i], c.History[i+1:]...)
			return true
		}
	}
	return false
}

func (c *CommandStore) SetFavourite(slot int, command, model string) error {
	command = strings.TrimSpace(command)
	switch {
	case c == nil:
		return fmt.Errorf("no command store")
	case slot < 1 || slot > FavouriteSlots:
		return fmt.Errorf("slot %d is outside 1-%d", slot, FavouriteSlots)
	case command == "":
		return fmt.Errorf("slot %d: nothing to save", slot)
	}
	for i := range c.Favourites {
		if c.Favourites[i].Slot == slot {
			c.Favourites[i] = Favourite{Slot: slot, Command: command, Model: model}
			return nil
		}
	}
	c.Favourites = append(c.Favourites, Favourite{Slot: slot, Command: command, Model: model})
	return nil
}

func (c *CommandStore) Favourite(slot int) (Favourite, bool) {
	if c == nil {
		return Favourite{}, false
	}
	for _, f := range c.Favourites {
		if f.Slot == slot {
			return f, true
		}
	}
	return Favourite{}, false
}

func (c *CommandStore) ClearFavourite(slot int) bool {
	if c == nil {
		return false
	}
	for i := range c.Favourites {
		if c.Favourites[i].Slot == slot {
			c.Favourites = append(c.Favourites[:i], c.Favourites[i+1:]...)
			return true
		}
	}
	return false
}

func (c *CommandStore) FavouriteList() []Favourite {
	if c == nil {
		return nil
	}
	out := make([]Favourite, len(c.Favourites))
	copy(out, c.Favourites)
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

func (c *CommandStore) SetMacro(m Macro) error {
	m.Name = strings.TrimSpace(m.Name)
	switch {
	case c == nil:
		return fmt.Errorf("no command store")
	case m.Name == "":
		return fmt.Errorf("a macro needs a name")
	case len(m.Commands) == 0:
		return fmt.Errorf("macro %q has no commands", m.Name)
	}
	for i := range c.Macros {
		if strings.EqualFold(c.Macros[i].Name, m.Name) {
			c.Macros[i] = m
			return nil
		}
	}
	c.Macros = append(c.Macros, m)
	return nil
}

func (c *CommandStore) Macro(name string) (Macro, bool) {
	if c == nil {
		return Macro{}, false
	}
	for _, m := range c.Macros {
		if strings.EqualFold(m.Name, name) {
			return m, true
		}
	}
	return Macro{}, false
}

func (c *CommandStore) RemoveMacro(name string) bool {
	if c == nil {
		return false
	}
	for i := range c.Macros {
		if strings.EqualFold(c.Macros[i].Name, name) {
			c.Macros = append(c.Macros[:i], c.Macros[i+1:]...)
			return true
		}
	}
	return false
}

func (c *CommandStore) Ack(id string) {
	if c == nil || id == "" || c.Acked(id) {
		return
	}
	c.Acknowledged = append(c.Acknowledged, id)
}

func (c *CommandStore) Acked(id string) bool {
	if c == nil {
		return false
	}
	for _, a := range c.Acknowledged {
		if a == id {
			return true
		}
	}
	return false
}

func (s *Store) commandsPath() string { return filepath.Join(s.Root, "commands.json") }

func (s *Store) Commands() (*CommandStore, error) {
	b, err := os.ReadFile(s.commandsPath())
	if os.IsNotExist(err) {
		return &CommandStore{}, nil
	}
	if err != nil {
		return &CommandStore{}, fmt.Errorf("read commands: %w", err)
	}
	var c CommandStore
	if err := json.Unmarshal(b, &c); err != nil {
		return &CommandStore{}, fmt.Errorf("parse commands: %w", err)
	}
	return &c, nil
}

func (s *Store) SaveCommands(c *CommandStore) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode commands: %w", err)
	}
	return writeAtomic(s.commandsPath(), append(b, '\n'), 0o600)
}
