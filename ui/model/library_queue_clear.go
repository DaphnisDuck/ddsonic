package model

// ddsonic: X in the library's Queue view clears the whole queue, and Ctrl+Z
// undoes it once (#22). The queue's one undo, this one or x's or Up next's,
// holds only while the playlist is as that change left it: any later change
// to the playlist (an add, a removal, a move, playing a track or a list,
// shuffle, repeat, a track ending and the next starting) bumps its revision
// and drops the undo. A replacement (replacePlaylist: playing a list,
// loading one, a clear) drops it even when the new list equals the old and
// the revision does not move. Moving the cursor, seeking, pausing and the
// volume do not touch the playlist, so they keep it.

import tea "charm.land/bubbletea/v2"

// libClearQueue stops playback and empties the queue and Up next, as IPC's
// queue.clear does, and arms the undo. A loaded saved playlist is detached,
// not edited. An empty queue is left alone, and so is a pending undo.
func (m *Model) libClearQueue() tea.Cmd {
	n := m.playlist.Len()
	if n == 0 {
		m.status.Show("The queue is empty", statusTTLShort)
		return nil
	}
	snapshot, loaded := m.playlist.Snapshot(), m.loadedPlaylist
	m.sleepCommand() // it stops playback: a transport command (sleep_ddsonic.go)
	m.stopPlayback()
	m.retireTracksPaging()
	m.replacePlaylist(nil)
	m.loadedPlaylist = ""
	m.setHeaderStateFromTracks(nil)
	m.plCursor, m.plScroll = 0, 0
	m.armPlaylistUndo(playlistUndo{snapshot: snapshot, loaded: loaded, detached: true})
	if n == 1 {
		m.status.Show("Cleared 1 track from the queue (Ctrl+Z to undo)", statusTTLDefault)
	} else {
		m.status.Showf(statusTTLDefault, "Cleared %d tracks from the queue (Ctrl+Z to undo)", n)
	}
	m.notifyPlayback()
	return m.rearmPreload() // drops a gapless pipeline armed or loading
}

// armPlaylistUndo makes u the queue's undo for the change just made: it
// holds while the playlist stays at its revision now.
func (m *Model) armPlaylistUndo(u playlistUndo) {
	u.active = true
	u.revision = m.playlist.Revision()
	m.playlistUndo = u
}

// playlistUndoCurrent reports whether the undo is armed and the playlist has
// not changed since; a stale undo is dropped.
func (m *Model) playlistUndoCurrent() bool {
	if m.playlistUndo.active && m.playlist.Revision() == m.playlistUndo.revision {
		return true
	}
	m.playlistUndo = playlistUndo{}
	return false
}
