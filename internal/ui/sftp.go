package ui

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/sftpx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// filePane is one side of the browser.
type filePane struct {
	fs      sftpx.FS
	path    string
	entries []sftpx.Entry
	idx     int
	err     error
}

func (p *filePane) reload() {
	es, err := p.fs.List(p.path)
	p.entries, p.err = es, err
	p.idx = clamp(p.idx, 0, len(p.entries)-1)
}

func (p *filePane) selected() (sftpx.Entry, bool) {
	if len(p.entries) == 0 {
		return sftpx.Entry{}, false
	}
	return p.entries[clamp(p.idx, 0, len(p.entries)-1)], true
}

func (p *filePane) selectedPath() (string, bool) {
	e, ok := p.selected()
	if !ok {
		return "", false
	}
	return p.fs.Join(p.path, e.Name), true
}

// --- messages ----------------------------------------------------------

type sftpConnectedMsg struct {
	sess *sftpx.Session
	host string
	err  error
}

// transferMsg reports progress or completion of one file transfer.
type transferMsg struct {
	name        string
	done, total int64
	err         error
	finished    bool
	// dst is the pane the file was going to, remembered from when the copy
	// started. Working it out from the focus when the message arrives got it
	// wrong for anyone who switched panes while a transfer was running.
	dst int
}

func connectSFTP(h store.Host) tea.Cmd {
	return func() tea.Msg {
		sess, err := sftpx.Connect(h)
		return sftpConnectedMsg{sess: sess, host: h.Name, err: err}
	}
}

func waitTransfer(ch <-chan transferMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// --- lifecycle ---------------------------------------------------------

func (m Model) openSFTP() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	m.setStatus("opening sftp on " + h.Name + "…")
	return m, connectSFTP(m.d.resolver.Resolve(h).Host)
}

func (m Model) sftpConnected(msg sftpConnectedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// The host as the subject, so a narrow bar keeps ssh's reason and the
		// remedy and lets the name go — it is on screen either way.
		m.setErrOf(msg.host, msg.err)
		return m, nil
	}
	local := sftpx.Local{}
	m.panes = [2]filePane{
		{fs: local, path: localStart(local)},
		{fs: msg.sess, path: msg.sess.Home()},
	}
	m.panes[0].reload()
	m.panes[1].reload()

	m.sftpSess = msg.sess
	m.paneFocus = 1
	m.mode = modeSFTP
	m.setStatus("sftp on " + msg.host)
	return m, waitTransfer(m.transfers)
}

func (m Model) closeSFTP() (tea.Model, tea.Cmd) {
	if m.sftpSess != nil {
		m.sftpSess.Close()
		m.sftpSess = nil
	}
	m.mode = modeBrowse
	m.setStatus("sftp closed")
	return m, nil
}

// localStart opens the local pane where Omassh was launched, which is more
// often what you want to transfer than the home directory.
func localStart(l sftpx.Local) string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return l.Home()
}

// Close releases the child processes the interface owns — an SFTP session and
// an embedded pane both hold an ssh of their own. Exported so main can tear
// them down after the program loop ends.
func (m Model) Close() {
	if m.sftpSess != nil {
		m.sftpSess.Close()
	}
	m.closePanesForExit()
}

// --- keys --------------------------------------------------------------

func (m Model) handleSFTPKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.panes[m.paneFocus]

	switch msg.String() {
	case "ctrl+c":
		m.sftpSess.Close()
		return m, tea.Quit
	case "esc", "q":
		return m.closeSFTP()
	case "tab", "shift+tab", "h", "l", "left", "right":
		m.paneFocus = 1 - m.paneFocus
	case "j", "down":
		p.idx = clamp(p.idx+1, 0, len(p.entries)-1)
	case "k", "up":
		p.idx = clamp(p.idx-1, 0, len(p.entries)-1)
	case "g":
		p.reload()
		m.setStatus("refreshed")

	case "enter":
		if _, why := p.enterSelected(); why != "" {
			m.setStatus(why)
		}
	case "backspace", "-":
		p.path = p.fs.Parent(p.path)
		p.idx = 0
		p.reload()

	case "c":
		return m.copySelected()
	case "m":
		// The title is fitted here rather than left to the box, so what
		// survives is the directory you are in and not the root it is under.
		const lead = "New directory in "
		m.form = singleFieldForm(formMkdir,
			lead+pathTail(fromRemote(p.path), m.dialogWidth()-5-ansi.StringWidth(lead)), "Name", "docs", "")
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	case "r":
		e, ok := p.selected()
		if !ok {
			return m, nil
		}
		m.form = singleFieldForm(formRename, "Rename "+fromRemote(e.Name), "Name", "the new name", e.Name)
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	case "M":
		e, ok := p.selected()
		if !ok {
			return m, nil
		}
		m.form = singleFieldForm(formChmod, "Permissions for "+fromRemote(e.Name), "Mode", "octal, like 644",
			fmt.Sprintf("%o", e.Mode.Perm()))
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	case "d":
		return m.askDeleteFile()
	}
	return m, nil
}

// --- actions -----------------------------------------------------------

// copySelected transfers the highlighted file to the other pane's directory.
func (m Model) copySelected() (tea.Model, tea.Cmd) {
	src := &m.panes[m.paneFocus]
	dst := &m.panes[1-m.paneFocus]

	e, ok := src.selected()
	if !ok {
		return m, nil
	}
	if e.IsDir {
		// Recursive transfer is a queue of its own; refusing is clearer than
		// silently copying only the directory entry.
		m.setStatus("directories cannot be copied yet — select a file")
		return m, nil
	}

	srcPath := src.fs.Join(src.path, e.Name)
	dstPath := dst.fs.Join(dst.path, e.Name)
	srcFS, dstFS := src.fs, dst.fs
	dstPane := 1 - m.paneFocus
	ch := m.transfers

	go func() {
		last := time.Now()
		err := sftpx.Copy(dstFS, dstPath, srcFS, srcPath, func(done, total int64) {
			// Throttle: a fast local copy would otherwise flood the UI with
			// more messages than it can render.
			if time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			select {
			case ch <- transferMsg{name: fromRemote(e.Name), done: done, total: total, dst: dstPane}:
			default:
			}
		})
		ch <- transferMsg{name: fromRemote(e.Name), err: err, finished: true, total: e.Size, done: e.Size, dst: dstPane}
	}()

	m.setStatus("copying " + fromRemote(e.Name) + " → " + dstFS.Label())
	return m, nil
}

func (m Model) askDeleteFile() (tea.Model, tea.Cmd) {
	p := &m.panes[m.paneFocus]
	e, ok := p.selected()
	if !ok {
		return m, nil
	}
	full, _ := p.selectedPath()
	fs := p.fs
	detail := "this cannot be undone"
	if e.IsDir {
		detail = "the directory and everything in it — this cannot be undone"
	}
	m.confirm = &confirmation{
		prompt: "Delete " + fromRemote(e.Name) + " on " + fs.Label() + "?",
		detail: detail,
		run:    func() (string, error) { return "deleted " + fromRemote(e.Name), fs.Remove(full) },
	}
	m.returnTo, m.mode = backFor(m.mode), modeConfirm
	return m, nil
}

func (m Model) saveFileForm() (tea.Model, tea.Cmd) {
	f := m.form
	p := &m.panes[m.paneFocus]
	name := f.value(f.fields[0].label)
	if name == "" {
		f.problem = "a name is required"
		return m, nil
	}

	var err error
	switch f.kind {
	case formMkdir:
		dest := p.fs.Join(p.path, name)
		err = sftpx.Problem(p.fs, dest, name, p.fs.Mkdir(dest))
	case formRename:
		old, ok := p.selectedPath()
		if !ok {
			err = fmt.Errorf("nothing selected")
			break
		}
		dest := p.fs.Join(p.path, name)
		err = sftpx.Problem(p.fs, dest, name, p.fs.Rename(old, dest))
	case formChmod:
		mode, perr := strconv.ParseUint(name, 8, 32)
		if perr != nil {
			f.problem = "mode must be octal, like 644"
			return m, nil
		}
		target, ok := p.selectedPath()
		if !ok {
			err = fmt.Errorf("nothing selected")
			break
		}
		err = p.fs.Chmod(target, os.FileMode(mode))
	}
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}

	m.form, m.mode = nil, modeSFTP
	p.reload()
	m.setStatus("done")
	return m, nil
}

// singleFieldForm builds the one-field dialogs the file browser asks with.
//
// A value filled in from what is already there is a suggestion, so typing
// replaces it. Renaming means typing the name you want, and appending to what
// was there turned "notes.txt" into "notes.txtreport.txt" — the same way an
// unreplaced group suggestion once made a group called "FleetFleet".
//
// The hint must never be able to read as the value, which is why neither of
// them is the current name. A placeholder shows through as soon as the field
// is emptied, and one repeating the value made a cleared field look full: the
// text was still there, backspace looked dead, and saving asked for a name
// that was apparently already given.
func singleFieldForm(kind formKind, title, label, hint, value string) *form {
	return &form{kind: kind, title: title, fields: []field{asSuggestion(newField(label, hint, value))}}
}

// --- rendering ---------------------------------------------------------

func (m Model) sftpView(content int) string {
	leftW := m.w / 2
	rightW := m.w - leftW
	body := content - 1 // one row for the transfer strip

	rows := max(body-2, 1) // the box's own borders
	left := box(m.paneTitle(0, rows, leftW), m.paneFocus == 0, leftW, body, m.paneBody(0, leftW-4, rows))
	right := box(m.paneTitle(1, rows, rightW), m.paneFocus == 1, rightW, body, m.paneBody(1, rightW-4, rows))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n" + m.transferStrip()
}

func (m Model) paneTitle(i, rows, w int) string {
	p := m.panes[i]
	// A directory that does not fit says where in it you are, since the list
	// scrolls and the borders alone no longer show that there is more. It goes
	// before the path, which is the part that gives way — putting the position
	// after it loses the position exactly when there is enough to need one.
	//
	// Measured plainly and drawn styled, since the position is dimmed and a
	// title has to be sized before it is coloured.
	head, styled := p.fs.Label(), p.fs.Label()
	if len(p.entries) > rows {
		pos := fmt.Sprintf("  %d/%d", p.idx+1, len(p.entries))
		head, styled = head+pos, styled+theme.Dim.Render(pos)
	}
	// box gives a title w-5 cells, and two spaces separate it from the path.
	avail := w - 5 - ansi.StringWidth(head) - 2
	if avail < 4 {
		return styled
	}
	return styled + "  " + pathTail(fromRemote(p.path), avail)
}

func (m Model) paneBody(i, w, rows int) string {
	p := m.panes[i]
	if p.err != nil {
		return theme.Fg(theme.Red).Render(p.err.Error())
	}
	if len(p.entries) == 0 {
		return theme.Dim.Render("empty")
	}

	// Render only the window the pane can show. Drawing every entry and
	// letting the box cut it off meant the cursor walked out of sight in any
	// directory bigger than the pane, with nothing on screen saying what was
	// about to be copied or deleted.
	start, end := listWindow(p.idx, len(p.entries), rows)

	lines := make([]string, 0, end-start)
	for j, e := range p.entries[start:end] {
		j += start
		name := fromRemote(e.Name)
		if e.IsDir {
			name += "/"
		}
		size := humanSize(e.Size)
		if e.IsDir {
			size = ""
		}
		text := fmt.Sprintf("%s %s", pad(ansi.Truncate(name, max(w-12, 4), "…"), max(w-12, 4)), lpad(size, 8))

		selected := j == p.idx && m.paneFocus == i
		if selected {
			lines = append(lines, row(text, true, w))
			continue
		}
		style := theme.Normal
		if e.IsDir {
			style = theme.Fg(theme.Accent)
		}
		lines = append(lines, style.Render(text))
	}
	return strings.Join(lines, "\n")
}

func (m Model) transferStrip() string {
	// Idle, this row is the file browser's key list. It is the only hint line
	// in this view: the status bar shows just the status here, because the two
	// were saying nearly the same thing in different words, and neither said
	// all of it.
	if m.transfer.name == "" {
		return theme.Dim.Render(" " + ansi.Truncate(
			"tab/⇧tab pane · ↵ open · - up · c copy · m mkdir · r rename · M chmod · d delete · q close",
			m.w-1, "…"))
	}
	t := m.transfer
	switch {
	case t.err != nil:
		// The name only if it fits beside the reason. It is in the listing
		// just above either way, and cut the other way round this row said
		// which file and not what went wrong.
		msg := t.err.Error()
		if with := t.name + ": " + msg; ansi.StringWidth(with)+1 <= m.w {
			msg = with
		}
		return theme.Fg(theme.Red).Render(" " + ansi.Truncate(msg, m.w-1, "…"))
	case t.finished:
		return theme.Fg(theme.Green).Render(fmt.Sprintf(" %s — copied %s", t.name, humanSize(t.total)))
	default:
		pct := 0
		if t.total > 0 {
			pct = int(t.done * 100 / t.total)
		}
		return theme.Fg(theme.Yellow).Render(fmt.Sprintf(" %s — %d%% (%s of %s)",
			t.name, pct, humanSize(t.done), humanSize(t.total)))
	}
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// lpad right-aligns s in n columns, counted in cells for the same reason pad
// counts them that way.
func lpad(s string, n int) string {
	w := ansi.StringWidth(s)
	if w >= n {
		return s
	}
	return strings.Repeat(" ", n-w) + s
}

// listWindow is the slice of a list to draw so that idx stays visible.
//
// The window moves only when the cursor would leave it, which keeps the list
// still while you move within the visible part — a window centred on the
// cursor would scroll the whole pane on every keystroke.
func listWindow(idx, n, rows int) (start, end int) {
	if rows >= n {
		return 0, n
	}
	start = clamp(idx-rows+1, 0, max(n-rows, 0))
	if idx < start {
		start = idx
	}
	return start, min(start+rows, n)
}

// enterSelected descends into the highlighted entry when it is a directory.
// Shared by ↵ and by a double click, so the two cannot drift apart. why is
// what to say when it could not, and is empty when there is nothing to say.
//
// A symlink is not a directory as far as a listing is concerned. Both
// os.ReadDir and the sftp server report what lstat says, so /tmp, /etc, /var
// and /home — every one of them a symlink on macOS — sat among the files with
// a size beside them, and pressing ↵ on one did nothing whatsoever: no
// descent, no message, nothing to suggest it was not simply broken. Navigating
// from / to anywhere they lead was impossible.
//
// Following one costs a stat, so it happens here, on the single entry someone
// asked about, rather than on every row of every listing — which for a remote
// directory of any size is a round trip each.
func (p *filePane) enterSelected() (ok bool, why string) {
	e, sel := p.selected()
	if !sel {
		return false, ""
	}
	if !e.IsDir {
		if e.Mode&os.ModeSymlink == 0 {
			return false, "" // an ordinary file; ↵ is not what opens it
		}
		target, err := p.fs.Stat(p.fs.Join(p.path, e.Name))
		switch {
		case errors.Is(err, os.ErrNotExist):
			// Naming the link rather than repeating "no such file", which
			// would be said about a name that is plainly there on the screen.
			return false, fromRemote(e.Name) + " points at something that is not there"
		case err != nil:
			return false, "cannot follow " + fromRemote(e.Name) + ": " + bareError(err)
		case !target.IsDir:
			return false, "" // a link to a file is a file
		}
	}
	p.path = p.fs.Join(p.path, e.Name)
	p.idx = 0
	p.reload()
	return true, ""
}

// bareError is what went wrong without the path Go wraps around it. The name
// is already in the sentence, and saying it twice pushes the half that matters
// off the end of the bar.
func bareError(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// fromRemote is text omassh did not write, made safe to draw.
//
// A filename comes from the far side, and a name is not a message. One holding
// an escape sequence went straight into the interface: the row took its colour
// from the file rather than from the theme, and a name carrying a cursor move
// or an erase had the terminal act on it in the middle of a redraw. Widths were
// never the problem — ansi.StringWidth ignores escapes, so the frame held while
// the terminal did as the remote asked.
//
// What escape-stripping leaves behind is the bare control characters, and each
// of those breaks the row its own way: a newline splits it in two, a tab shifts
// the size column off its alignment, a carriage return draws the end of the row
// over the start of it, and a bell simply rings. Every one becomes a question
// mark — what ls does with a byte it cannot print — which keeps the name
// recognisable and its width honest.
//
// The true name is what every operation uses, so a file can still be opened,
// copied or deleted by a name nothing can safely print. This is for the passive
// surfaces, where merely listing a directory is enough to be drawn into.
func fromRemote(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, ansi.Strip(s))
}
