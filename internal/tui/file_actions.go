package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// fileChange names one of the file menu's changes.
type fileChange int

const (
	fileHigh fileChange = iota
	fileNormal
	fileLow
	fileWanted
	fileUnwanted
)

// fileChangeVerb is how a file change is reported and what it writes. The CLI's
// `files update` prints the changed rows rather than a sentence, so the wording
// is the TUI's own, in the shape of its torrent verbs.
type fileChangeVerb struct {
	// done is the report, formatted with the counted files.
	done string

	// apply is the FileChanges the change writes for a set of indexes.
	apply func(indexes []int) transmission.FileChanges
}

// fileChangeVerbs are the file menu's five changes.
var fileChangeVerbs = map[fileChange]fileChangeVerb{
	fileHigh: {done: "Set %s to high priority", apply: func(ix []int) transmission.FileChanges {
		return transmission.FileChanges{PriorityHigh: ix}
	}},
	fileNormal: {done: "Set %s to normal priority", apply: func(ix []int) transmission.FileChanges {
		return transmission.FileChanges{PriorityNormal: ix}
	}},
	fileLow: {done: "Set %s to low priority", apply: func(ix []int) transmission.FileChanges {
		return transmission.FileChanges{PriorityLow: ix}
	}},
	fileWanted: {done: "Marked %s wanted", apply: func(ix []int) transmission.FileChanges {
		return transmission.FileChanges{Wanted: ix}
	}},
	fileUnwanted: {done: "Marked %s unwanted", apply: func(ix []int) transmission.FileChanges {
		return transmission.FileChanges{Unwanted: ix}
	}},
}

// fileMutationMsg is the answer to a file change.
type fileMutationMsg struct {
	gen    int
	change fileChange
	count  int
	err    error
}

// fileRenamedMsg is the answer to a file rename: the torrent-relative path that
// was renamed, its new last component, and the torrent it lives in.
type fileRenamedMsg struct {
	gen                 int
	path, name, torrent string
	err                 error
}

// pluralFiles is a file count with its noun, the CLI's pluralizeFiles.
func pluralFiles(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "file"))
}

// fileMenu is m on the files tab: the changes `torrent files update` makes, and
// rename, over the marked files or the cursor file.
func fileMenu(n int) menuOverlay {
	return menuOverlay{
		title: "Files (" + pluralFiles(n) + ")",
		items: []menuItem{
			{label: "high priority", action: actionFilePriorityHigh},
			{label: "normal priority", action: actionFilePriorityNormal},
			{label: "low priority", action: actionFilePriorityLow},
			{label: "wanted", action: actionFileWanted},
			{label: "unwanted", action: actionFileUnwanted},
			{label: "rename", action: actionFileRename},
		},
	}
}

// fileSelection is what a file action acts on: the torrent the detail describes
// and the file indexes — the marks, or the cursor file — or why there is
// nothing to act on.
func (m Model) fileSelection() (int64, []int, string, bool) {
	p := m.torrents.detail

	switch {
	case m.client == nil:
		return 0, nil, "no server", false
	case m.live.switching:
		return 0, nil, "switching servers…", false
	case p.torrent == nil:
		return 0, nil, "nothing selected", false
	case !p.hasDetail || len(p.detail.Files) == 0:
		return 0, nil, "no files", false
	}

	indexes := p.fileSel.keys(p.detail.Files)
	if len(indexes) == 0 {
		return 0, nil, "nothing selected", false
	}

	return p.detail.ID, indexes, "", true
}

// openFileMenu is m on the files tab. It refuses for the same reasons its
// leaves would, so the menu never opens over nothing.
func (m Model) openFileMenu() Model {
	if top, ok := m.topOverlay(); ok && top.kind() == menuOverlayKind {
		return m.popMenus()
	}

	_, indexes, why, ok := m.fileSelection()
	if !ok {
		m.toast = why

		return m
	}

	return m.push(fileMenu(len(indexes)))
}

// toggleFileMark is space on the files tab.
func (m Model) toggleFileMark() Model {
	m.torrents.detail = m.torrents.detail.toggleFileMark()

	return m
}

// changeFiles writes a priority or wanted change to the selected files. It does
// not confirm: it touches one torrent, the same menu undoes it, and the CLI's
// `files update` does not ask either.
func (m Model) changeFiles(change fileChange) (Model, tea.Cmd) {
	id, indexes, why, ok := m.fileSelection()
	if !ok {
		m.toast = why

		return m, nil
	}

	changes := fileChangeVerbs[change].apply(indexes)
	n := len(indexes)
	ctx, client, gen := m.ctx, m.client, m.live.gen

	m.toast = "Updating " + pluralFiles(n) + "…"

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		err := client.SetFiles(ctx, transmission.Selector{IDs: []int64{id}}, changes)

		return fileMutationMsg{gen: gen, change: change, count: n, err: err}
	}
}

// filesChanged settles a file change: report it, then refetch rather than edit
// the rows, as every other mutation does.
func (m Model) filesChanged(msg fileMutationMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	if msg.err != nil {
		m.toast = "files: " + errorLine(msg.err)

		return m, nil
	}

	m.toast = fmt.Sprintf(fileChangeVerbs[msg.change].done, pluralFiles(msg.count))

	return m.refetchDetail()
}

// refetchDetail follows a change the detail shows: fetch it now, rather than at
// the next tick, and poll so the list agrees.
func (m Model) refetchDetail() (Model, tea.Cmd) {
	var detail tea.Cmd

	if id, ok := m.wantedDetailID(); ok && m.live.detailInFlight == 0 {
		m, detail = m.startDetailFetch(id)
	}

	m, poll := m.requestPoll()

	return m, tea.Batch(detail, poll)
}

// lastComponent is a torrent-relative path's last component, the part a rename
// replaces.
func lastComponent(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

// renameFile renames one file's last path component. Transmission renames one
// path at a time, so more than one selected file is refused rather than narrowed
// to one of them.
func (m Model) renameFile() (Model, tea.Cmd) {
	id, indexes, why, ok := m.fileSelection()

	switch {
	case !ok:
		m.toast = why

		return m, nil
	case len(indexes) != 1:
		m.toast = "rename: one file at a time"

		return m, nil
	}

	file := m.torrents.detail.detail.Files[indexes[0]]
	torrent := m.torrents.detail.detail.Name
	current := output.SanitizeTerminal(lastComponent(file.Name))

	return m.openPrompt(newPrompt(m.styles, "Rename file", "renames the last path component", current,
		func(m Model, value string) (Model, tea.Cmd) {
			name := strings.TrimSpace(value)

			switch {
			case name == "":
				return m.rejectPrompt(errors.New("a name is required")), nil
			case name == current:
				return m.rejectPrompt(errors.New("the name is unchanged")), nil
			case strings.Contains(name, "/"):
				return m.rejectPrompt(errors.New("a name cannot contain /")), nil
			case m.client == nil || m.live.switching:
				return m.rejectPrompt(errors.New("no server")), nil
			}

			ctx, client, gen := m.ctx, m.client, m.live.gen

			m = m.pop()
			m.toast = "Renaming " + output.SanitizeTerminal(file.Name) + "…"

			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
				defer cancel()

				_, err := client.RenamePath(ctx, transmission.Selector{IDs: []int64{id}}, file.Name, name)

				return fileRenamedMsg{gen: gen, path: file.Name, name: name, torrent: torrent, err: err}
			}
		}))
}

// fileRenamed settles a rename with the CLI's own line.
func (m Model) fileRenamed(msg fileRenamedMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	if msg.err != nil {
		m.toast = "rename: " + errorLine(msg.err)

		return m, nil
	}

	m.toast = fmt.Sprintf("Renamed %s to %s in torrent %s",
		output.SanitizeTerminal(msg.path), output.SanitizeTerminal(msg.name), output.SanitizeTerminal(msg.torrent))

	return m.refetchDetail()
}

// fileHandlers are the files tab's space and m, and the file menu's leaves.
func fileHandlers() map[Action]handler {
	change := func(c fileChange) handler {
		return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.changeFiles(c) }
	}

	return map[Action]handler{
		actionFileMark:           plain(Model.toggleFileMark),
		actionFileMenu:           plain(Model.openFileMenu),
		actionFilePriorityHigh:   change(fileHigh),
		actionFilePriorityNormal: change(fileNormal),
		actionFilePriorityLow:    change(fileLow),
		actionFileWanted:         change(fileWanted),
		actionFileUnwanted:       change(fileUnwanted),
		actionFileRename:         func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.renameFile() },
	}
}
