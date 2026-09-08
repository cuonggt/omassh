package ui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
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
	// Counted per sweep. The probes map keeps every result ever seen, so
	// summing it would report on hosts in groups this sweep never touched.
	m.probeCounts = map[probe.State]int{}
	m.setStatus(fmt.Sprintf("probing %d host%s…", len(hosts), plural(len(hosts))))
	return m, waitProbe(ch)
}

func (m Model) handleProbeEvent(ev probeEvent) (tea.Model, tea.Cmd) {
	if ev.done {
		m.probing = false
		m.setStatus(probeSummary(m.probeCounts))
		return m, nil
	}
	if m.probes == nil {
		m.probes = map[string]probe.State{}
	}
	m.probes[ev.key] = ev.state
	m.probeCounts[ev.state]++
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

// probeSummary reports what a sweep found.
//
// Counting only up and down used to leave a group of hosts behind a jump host
// reading "0 up, 0 down", which looks like the probe did nothing rather than
// like it deliberately declined. Every host lands in exactly one bucket, and
// the ones that were not dialled say why.
func probeSummary(counts map[probe.State]int) string {
	var parts []string
	for _, b := range []struct {
		state probe.State
		label string
	}{
		{probe.Up, "up"},
		{probe.Down, "down"},
		{probe.Skipped, "skipped"},
		{probe.Unknown, "no address"},
	} {
		if n := counts[b.state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, b.label))
		}
	}
	if len(parts) == 0 {
		return "nothing probed"
	}
	msg := strings.Join(parts, " · ")

	// Say why once, and only when the skips are the whole story — otherwise
	// the reason crowds out the counts it is explaining.
	if counts[probe.Skipped] > 0 && counts[probe.Up]+counts[probe.Down] == 0 {
		msg += " — a host behind a jump host is not dialled directly"
	}
	return msg
}
