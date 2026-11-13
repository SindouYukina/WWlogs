package history

import "wwlogs/internal/capture"

// Losses in journal order.
//
// A journal entry can fail to reach storage in three places: the engine journal refuses it at its own bound, the recorder's
// queue refuses it for lack of room (live capture only), or the transaction that held it fails. Wherever that happens, what
// was lost becomes a lossNote that takes the entries' place in the journal: it travels through the queue between the entries
// before and after them, so the writer applies it in the span the entries belonged to. Applying a note counts the entries on
// that span and marks the encounters they belonged to incomplete; an entry that belonged to no single encounter, such as an
// effect change or a transport loss, marks every encounter of the span. Structural entries, the span markers and protocol
// sessions (structural), are never turned into notes while the writer runs: they are held in order with the notes, so the
// entries after them always land in the right span and protocol session. "Markers" below means these entries.

// lossNote stands for journal entries that were not stored.
type lossNote struct {
	journal, queue, write uint64
	// keys are the encounters the entries named; unattributed is set when an entry belonged to no single encounter, or when
	// there were too many encounters to name.
	keys         map[string]struct{}
	unattributed bool
}

// Where an entry was lost.
type lossPlace uint8

const (
	lostInJournal lossPlace = iota
	lostInQueue
	lostInTransaction
)

func (n *lossNote) count(place lossPlace, entries uint64) {
	switch place {
	case lostInJournal:
		n.journal += entries
	case lostInQueue:
		n.queue += entries
	default:
		n.write += entries
	}
}

// add records one lost entry. An accepted combat result names no encounter, but its assignment does, and the writer marks an
// encounter whose assigned result is missing (HistoryEventAssigned); it therefore marks nothing by itself.
func (n *lossNote) add(place lossPlace, entry capture.HistoryEntry) {
	n.count(place, 1)
	switch {
	case entry.Encounter != "":
		n.name(entry.Encounter)
	case entry.Kind != capture.HistoryEventAccepted:
		n.unattributed = true
	}
}

func (n *lossNote) name(key string) {
	if n.unattributed {
		return
	}
	if len(n.keys) >= maxTaintedEncounters {
		n.keys, n.unattributed = nil, true
		return
	}
	if n.keys == nil {
		n.keys = map[string]struct{}{}
	}
	n.keys[key] = struct{}{}
}

func (n *lossNote) merge(other *lossNote) {
	n.journal += other.journal
	n.queue += other.queue
	n.write += other.write
	if other.unattributed {
		n.keys, n.unattributed = nil, true
	}
	for key := range other.keys {
		n.name(key)
	}
}

func (n *lossNote) entries() uint64 { return n.journal + n.queue + n.write }

// heldItem is a span marker or a loss note waiting in journal order.
type heldItem struct {
	marker capture.HistoryEntry
	note   *lossNote
}

// held is a run of held items in journal order.
type held []heldItem

func (n *lossNote) empty() bool {
	return n == nil || n.entries() == 0 && !n.unattributed && len(n.keys) == 0
}

// appendNote adds a copy of note, merged into the last item when that is a note too.
func (h held) appendNote(note *lossNote) held {
	if note.empty() {
		return h
	}
	if last := len(h) - 1; last >= 0 && h[last].note != nil {
		h[last].note.merge(note)
		return h
	}
	copied := &lossNote{}
	copied.merge(note)
	return append(h, heldItem{note: copied})
}

// appendLost turns entries that were lost at place into notes, keeping their span markers in order between them. It returns
// how many entries other than span markers were lost.
func (h held) appendLost(place lossPlace, entries []capture.HistoryEntry) (held, uint64) {
	var note *lossNote
	var lost uint64
	for _, entry := range entries {
		if structural(entry.Kind) {
			h = h.appendNote(note)
			note = nil
			h = append(h, heldItem{marker: entry})
			continue
		}
		if note == nil {
			note = &lossNote{}
		}
		note.add(place, entry)
		lost++
	}
	return h.appendNote(note), lost
}

// markers counts the span markers held.
func (h held) markers() int {
	count := 0
	for _, item := range h {
		if item.note == nil {
			count++
		}
	}
	return count
}

// submission is one queue element: what waited in journal order before a drain, then the drain itself.
type submission struct {
	held    held
	entries []capture.HistoryEntry
}

// size is the number of queue places the submission takes.
func (s submission) size() int { return len(s.held) + len(s.entries) }

// lostSubmissions turns everything of a failed or skipped transaction into held items in journal order: markers and notes
// stay as they are, and every other entry becomes part of a note for place. A marker the transaction refused (refused, when
// not nil) becomes a note too: it belonged to no single encounter. It returns how many entries it turned into notes.
func lostSubmissions(place lossPlace, subs []submission, refused *refusedEntry) (held, uint64) {
	var h held
	var lost uint64
	isRefused := func(sub int, inHeld bool, index int) bool {
		return refused != nil && refused.sub == sub && refused.held == inHeld && refused.index == index
	}
	refuse := func(entry capture.HistoryEntry) {
		note := &lossNote{unattributed: true}
		note.count(place, 1)
		h = h.appendNote(note)
		lost++
	}
	for i, sub := range subs {
		for index, item := range sub.held {
			switch {
			case item.note != nil:
				h = h.appendNote(item.note)
			case isRefused(i, true, index):
				refuse(item.marker)
			default:
				h = append(h, item)
			}
		}
		entries := sub.entries
		if refused != nil && refused.sub == i && !refused.held {
			var n uint64
			h, n = h.appendLost(place, entries[:refused.index])
			lost += n
			refuse(entries[refused.index])
			entries = entries[refused.index+1:]
		}
		var n uint64
		h, n = h.appendLost(place, entries)
		lost += n
	}
	return h, lost
}

// structural reports an entry that says where the entries after it belong: a span marker, or the protocol session (and game
// version) of what follows. It is never refused for lack of room or lost with a failed transaction while the writer runs.
func structural(kind capture.HistoryEntryKind) bool {
	switch kind {
	case capture.HistoryRecordingStarted, capture.HistoryRecordingStopped, capture.HistoryProtocolConfirmed:
		return true
	}
	return false
}
