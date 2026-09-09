// Package keymap resolves key presses to actions, so bindings can be changed
// from the config file without the interface knowing which key did what.
package keymap

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Action is something the interface can do in the main browse view.
type Action string

const (
	None        Action = ""
	Quit        Action = "quit"
	Help        Action = "help"
	Connect     Action = "connect"
	Search      Action = "search"
	NewItem     Action = "new"
	Edit        Action = "edit"
	Delete      Action = "delete"
	Reload      Action = "reload"
	Redraw      Action = "redraw"
	Probe       Action = "probe"
	SFTP        Action = "sftp"
	Forward     Action = "forward"
	Theme       Action = "theme"
	Pane        Action = "pane"
	NextPanel   Action = "next-panel"
	PrevPanel   Action = "prev-panel"
	PanelGroups Action = "panel-groups"
	PanelHosts  Action = "panel-hosts"
	Up          Action = "up"
	Down        Action = "down"
)

// defaults are the rebindable bindings.
var defaults = map[Action]string{
	Quit: "q", Help: "?", Connect: "enter", Search: "/",
	NewItem: "n", Edit: "e", Delete: "d", Reload: "r", Probe: "p",
	Redraw: "ctrl+l",
	SFTP:   "s", Pane: "t", Theme: "T", Forward: "f",
	NextPanel: "tab", PrevPanel: "shift+tab",
	PanelGroups: "1", PanelHosts: "2",
	Up: "k", Down: "j",
}

// fixed bindings always work and cannot be reassigned. Arrow keys stay usable
// whatever else is configured, and ctrl+c must always quit — a config file
// should never be able to trap someone in the program.
var fixed = map[string]Action{
	"ctrl+c": Quit,
	"up":     Up,
	"down":   Down,
}

// Map resolves a key press to an action.
type Map struct {
	byKey    map[string]Action
	byAction map[Action]string
}

func Default() Map {
	m, err := New(nil)
	if err != nil {
		panic("keymap: built-in defaults are inconsistent: " + err.Error())
	}
	return m
}

// New builds a map from the defaults plus overrides, given as action name to
// key. It reports unknown actions, keys claimed twice, and attempts to rebind
// a key that is reserved.
func New(overrides map[string]string) (Map, error) {
	byAction := make(map[Action]string, len(defaults))
	for a, k := range defaults {
		byAction[a] = k
	}

	// Overrides are applied in a stable order so a conflict is reported the
	// same way on every run.
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)

	// Which actions the file spoke for, so a clash can say whether the key it
	// names was chosen or merely inherited.
	explicit := map[Action]bool{}

	for _, name := range names {
		key := overrides[name]
		action := Action(strings.TrimSpace(name))
		if _, ok := defaults[action]; !ok {
			return Map{}, fmt.Errorf("unknown action %q (known: %s)", name, strings.Join(Names(), ", "))
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return Map{}, fmt.Errorf("action %q has no key", name)
		}
		if _, reserved := fixed[key]; reserved {
			return Map{}, fmt.Errorf("%q is reserved and cannot be rebound", key)
		}
		byAction[action] = key
		explicit[action] = true
	}

	byKey := make(map[string]Action, len(byAction)+len(fixed))
	// Sorted, so a file with two clashes is reported the same way every run
	// rather than by whichever the map happened to yield first.
	for _, action := range sortedActions(byAction) {
		key := byAction[action]
		if other, clash := byKey[key]; clash {
			return Map{}, clashError(key, action, other, explicit)
		}
		byKey[key] = action
	}
	// Fixed bindings are added last and win, so no override can displace them.
	for key, action := range fixed {
		byKey[key] = action
	}
	return Map{byKey: byKey, byAction: byAction}, nil
}

// clashError explains two actions wanting one key.
//
// Which of them the file actually asked for is the useful part. A new default
// binding lands on whatever key it was given, and a file that had already
// claimed that key for something else then stops the program at startup — as
// it should, since silently dropping one of the two would leave a key doing
// something other than what the file says. Naming the one that came from
// Omassh rather than from the file is what turns that into a line to change.
func clashError(key string, a, b Action, explicit map[Action]bool) error {
	first, second := string(a), string(b)
	if first > second {
		first, second = second, first
	}
	msg := fmt.Sprintf("key %q is bound to both %q and %q", key, first, second)
	switch {
	case explicit[a] && !explicit[b]:
		return fmt.Errorf("%s; %q is a built-in default, so give it a key of its own to free this one", msg, b)
	case explicit[b] && !explicit[a]:
		return fmt.Errorf("%s; %q is a built-in default, so give it a key of its own to free this one", msg, a)
	}
	return errors.New(msg)
}

func sortedActions(m map[Action]string) []Action {
	out := make([]Action, 0, len(m))
	for a := range m {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Lookup returns the action bound to a key press, or None.
func (m Map) Lookup(key string) Action { return m.byKey[key] }

// Key returns the key shown in help for an action.
func (m Map) Key(a Action) string {
	if k, ok := m.byAction[a]; ok {
		return k
	}
	return "?"
}

// Names lists every rebindable action, sorted.
func Names() []string {
	out := make([]string, 0, len(defaults))
	for a := range defaults {
		out = append(out, string(a))
	}
	sort.Strings(out)
	return out
}
