package editor

// Snapshot-based undo for whole-buffer rewrites.
//
// widget.Entry keeps a native undo stack for typing, but SetText clears it, and
// every structural command in Gaze — the line operations and find & replace —
// rewrites the buffer through SetText. Those commands used to be impossible to
// undo at all.
//
// Entry's own stack is always consulted first, so typing still undoes natively
// and the ordering stays correct: keystrokes are undone before the structural
// command that followed them. The trade-off is that once a structural command
// has run, the individual keystrokes from before it can no longer be undone one
// at a time — the whole pre-command buffer is restored in a single step.

const (
	// Whole-buffer copies are cheap at the file sizes Gaze targets, but the
	// limits stop a long session on a huge file from growing without bound.
	maxUndoSteps = 64
	maxUndoBytes = 32 << 20
)

// snapshot is one undoable buffer state.
type snapshot struct {
	text  string
	row   int
	col   int
	dirty bool
}

// current captures the buffer as it stands.
func (e *Editor) current() snapshot {
	return snapshot{
		text:  e.entry.Text,
		row:   e.entry.CursorRow,
		col:   e.entry.CursorColumn,
		dirty: e.dirty,
	}
}

// pushUndo records the pre-change state. Call it immediately before a SetText
// rewrite, never from inside a restore.
func (e *Editor) pushUndo() {
	if e.entry == nil || e.restoring {
		return
	}
	s := e.current()
	e.undo = append(e.undo, s)
	e.undoBytes += len(s.text)
	e.dropRedo()
	e.trimUndo()
}

// dropRedo forgets the redo trail; any new edit invalidates it. The slice is
// nil-ed rather than truncated so the old buffers can be collected.
func (e *Editor) dropRedo() {
	e.redo = nil
	e.redoBytes = 0
}

// trimUndo evicts the oldest snapshots once a limit is exceeded, always keeping
// at least one so that even a single huge buffer can still be undone.
func (e *Editor) trimUndo() {
	for len(e.undo) > 1 && (len(e.undo) > maxUndoSteps || e.undoBytes > maxUndoBytes) {
		e.undoBytes -= len(e.undo[0].text)
		e.undo = e.undo[1:]
	}
}

// popUndo restores the previous snapshot, moving the current state onto the
// redo trail.
func (e *Editor) popUndo() {
	n := len(e.undo)
	if n == 0 {
		e.setStatus("nothing to undo")
		return
	}
	cur := e.current()
	s := e.undo[n-1]
	e.undo = e.undo[:n-1]
	e.undoBytes -= len(s.text)

	e.redo = append(e.redo, cur)
	e.redoBytes += len(cur.text)

	e.restore(s)
	e.setStatus("undo")
}

// popRedo reverses a popUndo.
func (e *Editor) popRedo() {
	n := len(e.redo)
	if n == 0 {
		e.setStatus("nothing to redo")
		return
	}
	cur := e.current()
	s := e.redo[n-1]
	e.redo = e.redo[:n-1]
	e.redoBytes -= len(s.text)

	e.undo = append(e.undo, cur)
	e.undoBytes += len(cur.text)

	e.restore(s)
	e.setStatus("redo")
}

// restore installs a snapshot without treating it as a user edit: the dirty
// flag comes from the snapshot rather than being forced on by OnChanged.
func (e *Editor) restore(s snapshot) {
	e.restoring = true
	e.entry.SetText(s.text)
	e.entry.CursorRow, e.entry.CursorColumn = s.row, s.col
	e.restoring = false

	e.dirty = s.dirty
	e.entry.Refresh()
	e.refreshChrome()
}

// resetUndo drops all history, for when a different document is loaded.
func (e *Editor) resetUndo() {
	e.undo, e.redo = nil, nil
	e.undoBytes, e.redoBytes = 0, 0
}
