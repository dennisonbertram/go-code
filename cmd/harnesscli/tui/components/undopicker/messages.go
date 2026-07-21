package undopicker

// UndoSelectedMsg is emitted when the user presses Enter on an enabled entry.
// Entry.Count is the number of prompts the server must drop.
type UndoSelectedMsg struct {
	Entry UndoEntry
}
