package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/store"
)

// Whatever a row says is what clicking it selects.
//
// Hit-testing recomputes the geometry rather than keeping a copy of it, so the
// way it can go wrong is by recomputing something subtly different from what
// was drawn — and every offset that shifts the list is a chance to: the window
// a scrolled list starts at, the search box pushing the rows down, the height
// the boxes divide between them. Clicking the wrong row of a host list is not
// a cosmetic fault; the next key connects to it.
//
// The three here are the ones with distinct offsets. Each row is read off the
// screen immediately before it is clicked, because a click moves the selection
// and a moved selection can re-scroll the window under the pointer.
func TestClickingAHostRowSelectsTheHostDrawnOnIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hosts  int
		groups int
		filter string
		w, h   int
		scroll int
	}{
		{name: "scrolled to the bottom", hosts: 60, w: 120, h: 30, scroll: 59},
		{name: "filtered and scrolled", hosts: 60, filter: "host-1", w: 120, h: 30, scroll: 8},
		{name: "a tree and a short frame", hosts: 12, groups: 6, w: 120, h: 16, scroll: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			var gs []store.Group
			for i := range tc.groups {
				gs = append(gs, h.addGroup(fmt.Sprintf("g%d", i), ""))
			}
			for i := range tc.hosts {
				name := fmt.Sprintf("host-%02d", i)
				if len(gs) > 0 {
					h.addGroupedHost(name, gs[i%len(gs)].ID)
					continue
				}
				h.addHost(name, "10.0.0.1")
			}
			h.send(tea.WindowSizeMsg{Width: tc.w, Height: tc.h})
			if tc.filter != "" {
				h.press("/")
				h.type_(tc.filter)
				// ↵ keeps the filter and hands the keys back to the list: in
				// filter mode a "j" is a letter of the query, not a movement.
				h.press("enter")
			}
			h.press("2")
			for range tc.scroll {
				h.press("j")
			}

			l := h.m.layout()
			checked := 0
			for y := l.groupsH; y < l.content; y++ {
				screen := strings.Split(h.screen(), "\n")
				if y >= len(screen) {
					break
				}
				drawn := hostNameOn(screen[y])
				if drawn == "" {
					continue
				}
				h.click(2, y)
				got, ok := h.m.selectedHost()
				switch {
				case !ok:
					t.Errorf("row %d draws %q; clicking it selected nothing", y, drawn)
				case got.Name != drawn:
					t.Errorf("row %d draws %q; clicking it selected %q", y, drawn, got.Name)
				}
				checked++
			}
			if checked == 0 {
				t.Fatalf("no host rows were found to click:\n%s", h.screen())
			}
		})
	}
}

// The same for the group list, and for the wheel: what the pointer is over is
// what moves, and an unfocused list draws no selection, so it takes the focus
// with it.
func TestClickingAGroupRowSelectsTheGroupDrawnOnIt(t *testing.T) {
	h := newHarness(t)
	var gs []store.Group
	for i := range 20 {
		gs = append(gs, h.addGroup(fmt.Sprintf("group-%02d", i), ""))
	}
	h.addGroupedHost("only", gs[0].ID)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.press("1")
	// Far enough down that the box is showing a window rather than the start
	// of the list: an unscrolled list hides the offset this is checking.
	for range 15 {
		h.press("j")
	}

	checked := 0
	for y := range h.m.layout().groupsH {
		screen := strings.Split(h.screen(), "\n")
		drawn := groupNameOn(screen[y])
		if drawn == "" {
			continue
		}
		h.click(2, y)
		if got := currentGroupName(h.m); got != drawn {
			t.Errorf("row %d draws %q; clicking it selected %q", y, drawn, got)
		}
		checked++
	}
	if checked == 0 {
		t.Fatalf("no group rows were found:\n%s", h.screen())
	}

	before := currentGroupName(h.m)
	h.wheel(2, 1, false)
	if after := currentGroupName(h.m); after == before {
		t.Errorf("the wheel over the groups box moved nothing (still %q)", before)
	}
	if h.m.focus != panelGroups {
		t.Errorf("focus = %v, want the list the wheel moved", h.m.focus)
	}
}

// hostNameOn and groupNameOn pull a name out of a drawn sidebar row, or return
// "" where the row is not one. A host row carries the reachability mark; the
// search box above the list draws the query, which otherwise read as a name.
func hostNameOn(line string) string {
	if !strings.Contains(line, "○") && !strings.Contains(line, "●") {
		return ""
	}
	return nameAfter(line, "host-")
}

func groupNameOn(line string) string { return nameAfter(line, "group-") }

func nameAfter(line, prefix string) string {
	i := strings.Index(line, prefix)
	if i < 0 {
		return ""
	}
	name := line[i:]
	if j := strings.IndexAny(name, " │"); j >= 0 {
		name = name[:j]
	}
	return strings.TrimSpace(name)
}

func currentGroupName(m Model) string {
	g, ok := m.currentGroup()
	if !ok {
		return ""
	}
	return g.Name
}
