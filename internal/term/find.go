package term

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Finding text in a pane goes the two ways scrolling and selecting do.
//
// A session in tmux is searched by tmux. Copy mode has looked backwards
// through a pane's history for as long as it has had one, and that history is
// the whole of what the session ever said — where the emulator here holds only
// what it was shown since this window opened, which for a session reattached
// after a restart is nothing at all. tmux highlights what it found, and
// #{search_present} is how it says whether it found anything, so nothing has
// to read what it printed.
//
// A pane with nothing behind it — no tmux installed, or a session that has
// ended — searches what it has itself: the scrollback the emulator kept, then
// the live screen, as select.go selects from what it is showing.
//
// Both look for a pattern, without regard to case, because that is what tmux's
// copy mode does with what it is given and a pane that has lost its tmux
// should not quietly start answering a different question.

// Find puts the view on the last line matching pattern at or before the live
// view, and reports whether there was one. A search that finds nothing leaves
// the view where it was.
func (p *Pane) Find(pattern string) bool {
	if pattern == "" {
		return false
	}
	if p.session != "" {
		return p.findTmux(pattern)
	}
	return p.findHere(pattern, false)
}

// FindAgain looks further back for what the last search looked for, and
// reports whether there was another one. False where nothing has been searched
// for yet.
func (p *Pane) FindAgain() bool {
	if p.session != "" {
		return p.findTmux("")
	}
	return p.findHere("", true)
}

// findTmux hands the search to tmux. An empty pattern means again, further
// back, which is tmux's own search-again.
func (p *Pane) findTmux(pattern string) bool {
	if err := tmuxSearchBack(p.session, pattern); err != nil {
		return false
	}
	found := tmuxSearchPresent(p.session)
	if !found && pattern != "" {
		// A fresh search that matched nothing leaves copy mode on, showing
		// the screen it was already showing. Nothing says so, and the next
		// key typed would go to copy mode rather than to the shell, so the
		// view is put back as it was instead.
		tmuxCopyCancel(p.session)
		p.mu.Lock()
		p.tmuxOffset, p.finding = 0, false
		p.mu.Unlock()
		return false
	}
	p.recordTmuxScroll()
	p.mu.Lock()
	p.finding = true
	p.mu.Unlock()
	return found
}

// findHere searches the pane's own lines, newest first. again carries on from
// the line the view sits on rather than from the live view, which is what
// makes repeating a search walk back through the matches instead of finding
// the same one.
func (p *Pane) findHere(pattern string, again bool) bool {
	re, ok := p.findPattern(pattern, again)
	if !ok {
		return false
	}

	p.emMu.RLock()
	sb := p.em.Scrollback()
	n := sb.Len()
	live := strings.Split(p.em.Render(), "\n")
	p.emMu.RUnlock()

	// The styling has to come off before the pattern goes on. A line is kept
	// as it was drawn, so a word split by a colour change is still one word to
	// read and two to match, and `m` would find the escape sequence setting a
	// mode rather than anything anyone wrote.
	text := func(i int) string {
		if i < n {
			return ansi.Strip(sb.Line(i).Render())
		}
		return ansi.Strip(live[i-n])
	}

	p.mu.Lock()
	from := n + len(live) - 1
	if again {
		from = n - p.scroll - 1
	}
	p.mu.Unlock()

	for i := min(from, n+len(live)-1); i >= 0; i-- {
		if !re.MatchString(text(i)) {
			continue
		}
		// The match goes to the top of the view: a line found by searching is
		// usually read downwards from, and putting it anywhere else means
		// deciding how much of what came before to show.
		p.forgetSelection()
		p.mu.Lock()
		p.scroll = min(max(n-i, 0), n)
		p.mu.Unlock()
		return true
	}
	return false
}

// findPattern is what to look for: what was just given, compiled, or what the
// last search was given.
//
// A pattern that will not compile is taken literally. Someone who searched for
// `a(b` meant those three characters, and an error about an unclosed group
// would be an error about a thing they did not do.
func (p *Pane) findPattern(pattern string, again bool) (*regexp.Regexp, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if again {
		return p.findRe, p.findRe != nil
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(pattern))
	}
	p.findRe = re
	return re, true
}
