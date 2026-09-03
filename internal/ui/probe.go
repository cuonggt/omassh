package ui

import (
	"context"
	"fmt"
	"image/color"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/probe"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// probeEvent carries one host's reachability, or the end of a sweep.
type probeEvent struct {
	key   string
	state probe.State
	done  bool
}

func waitProbe(ch <-chan probeEvent) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// startProbe checks every host in the current group.
func (m Model) startProbe() (tea.Model, tea.Cmd) {
	hosts := m.visibleHosts()
	if len(hosts) == 0 {
		return m, nil
	}
	// Resolve first: inheritance decides whether a host has a jump host, and
	// therefore whether probing it directly means anything.
	targets := make([]store.Host, 0, len(hosts))
	for _, h := range hosts {
		targets = append(targets, m.d.resolver.Resolve(h).Host)
	}

	ch := m.probeCh
	timeout := m.opts.ProbeTimeout
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
		defer cancel()
		probe.CheckAll(ctx, targets, 8, timeout, func(key string, s probe.State) {
			ch <- probeEvent{key: key, state: s}
		})
		ch <- probeEvent{done: true}
	}()

	m.probing = true
	m.setStatus(fmt.Sprintf("probing %d host%s…", len(hosts), plural(len(hosts))))
	return m, waitProbe(ch)
}

func (m Model) handleProbeEvent(ev probeEvent) (tea.Model, tea.Cmd) {
	if ev.done {
		m.probing = false
		up, down := 0, 0
		for _, s := range m.probes {
			switch s {
			case probe.Up:
				up++
			case probe.Down:
				down++
			}
		}
		m.setStatus(fmt.Sprintf("%d up, %d down", up, down))
		return m, nil
	}
	if m.probes == nil {
		m.probes = map[string]probe.State{}
	}
	m.probes[ev.key] = ev.state
	return m, waitProbe(m.probeCh)
}

// hostMarker is the dot beside a host in the list.
func (m Model) hostMarker(key string) (string, color.Color) {
	switch m.probes[key] {
	case probe.Up:
		return "●", theme.Green
	case probe.Down:
		return "✖", theme.Red
	case probe.Skipped:
		return "◌", theme.TextDim
	default:
		return "○", theme.TextDim
	}
}
