// Package keymap resolves key presses to actions, so bindings can be changed
// from the config file without the interface knowing which key did what.
package keymap

import (
	"fmt"
	"sort"
	"strings"
)

// Action is something the interface can do in the main browse view.
type Action string

const (
	None          Action = ""
	Quit          Action = "quit"
	Help          Action = "help"
	Connect       Action = "connect"
	Handoff       Action = "handoff"
	Search        Action = "search"
	NewItem       Action = "new"
	Edit          Action = "edit"
	Delete        Action = "delete"
	Import        Action = "import"
	Reload        Action = "reload"
	Redraw        Action = "redraw"
	Probe         Action = "probe"
	Credentials   Action = "credentials"
	SFTP          Action = "sftp"
	Pane          Action = "pane"
	Snippets      Action = "snippets"
	NextPanel     Action = "next-panel"
	PrevPanel     Action = "prev-panel"
	PanelGroups   Action = "panel-groups"
	PanelHosts    Action = "panel-hosts"
	PanelForwards Action = "panel-forwards"
	Up            Action = "up"
	Down          Action = "down"
)

// defaults are the rebindable bindings.
var defaults = map[Action]string{
	Quit: "q", Help: "?", Connect: "enter", Handoff: "o", Search: "/",
	NewItem: "n", Edit: "e", Delete: "d", Import: "i", Reload: "r", Probe: "p",
	Redraw:      "ctrl+l",
	Credentials: "K", SFTP: "s", Snippets: "S", Pane: "t",
	NextPanel: "tab", PrevPanel: "shift+tab",
	PanelGroups: "1", PanelHosts: "2", PanelForwards: "3",
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
	}

	byKey := make(map[string]Action, len(byAction)+len(fixed))
	for action, key := range byAction {
		if other, clash := byKey[key]; clash {
			first, second := string(action), string(other)
			if first > second {
				first, second = second, first
			}
			return Map{}, fmt.Errorf("key %q is bound to both %q and %q", key, first, second)
		}
		byKey[key] = action
	}
	// Fixed bindings are added last and win, so no override can displace them.
	for key, action := range fixed {
		byKey[key] = action
	}
	return Map{byKey: byKey, byAction: byAction}, nil
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
