package mail

import (
	"context"
	"fmt"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// Bulk message operations.
//
// IMAP commands address a *set* of UIDs, so deleting a thousand messages is one
// STORE and one EXPUNGE — not a thousand of each. Looping the single-message
// calls is what made "Delete all" on a busy sender hang past every timeout and
// look like it did nothing at all.
//
// Each operation is chunked, and each chunk falls back to per-message work only
// when the batch itself fails, so one unreachable message can't sink the rest
// while the common case still costs a constant number of round trips.

// uidChunk caps how many UIDs go into a single command. go-imap coalesces
// consecutive UIDs into ranges ("1:1348"), so a contiguous run stays short
// whatever this is; the cap only matters for scattered selections, where it
// keeps the command line well inside the 8 KB most servers accept.
const uidChunk = 500

// parseUIDs converts the API's string ids to UIDs, dropping any that don't
// parse and reporting them so the caller can mark them failed rather than
// silently losing them.
func parseUIDs(ids []string) (uids []imap.UID, byUID map[imap.UID]string, bad []string) {
	byUID = make(map[imap.UID]string, len(ids))
	for _, id := range ids {
		uid, err := parseUID(id)
		if err != nil {
			bad = append(bad, id)
			continue
		}
		// A repeated id would otherwise be reported as acted on twice.
		if _, seen := byUID[uid]; seen {
			continue
		}
		byUID[uid] = id
		uids = append(uids, uid)
	}
	return uids, byUID, bad
}

func chunkUIDs(uids []imap.UID) [][]imap.UID {
	var chunks [][]imap.UID
	for start := 0; start < len(uids); start += uidChunk {
		chunks = append(chunks, uids[start:min(start+uidChunk, len(uids))])
	}
	return chunks
}

// DeleteMessages deletes many messages by moving them to the server's trash.
//
// Moving rather than expunging is load-bearing on Gmail, which is the common
// case. Gmail's default IMAP setting maps "\Deleted + EXPUNGE" onto *archive*:
// it removes the INBOX label and leaves the message in All Mail, so the mail
// the user asked to delete is still sitting in their account. Only moving to
// [Gmail]/Trash actually deletes it. The same move is correct everywhere else
// too — it is what every mail client means by Delete, and it stays recoverable
// until the server purges the trash.
//
// Expunging is kept for the two cases where a move can't apply: a server with
// no trash folder at all, and messages already in the trash, where deleting
// means destroying them for good.
func (p *IMAPProvider) DeleteMessages(ctx context.Context, folder string, ids []string) ([]string, error) {
	uids, byUID, _ := parseUIDs(ids)
	if len(uids) == 0 {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	trash := p.resolveSpecialFolder(imap.MailboxAttrTrash, "", trashFallbackNames)
	// Deleting *from* the trash means emptying it, not moving it onto itself.
	if strings.EqualFold(trash, folder) {
		trash = ""
	}

	// removeChunk is whichever removal the server actually supports.
	removeChunk := p.expungeChunk
	if trash != "" {
		removeChunk = func(chunk []imap.UID) error { return p.moveChunk(chunk, trash) }
	}

	var done []string
	for _, chunk := range chunkUIDs(uids) {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if err := removeChunk(chunk); err != nil {
			// The batch failed as a unit, so which message caused it is
			// unknown. Retry the chunk one at a time to isolate the bad ones
			// instead of reporting the whole chunk as lost.
			for _, uid := range chunk {
				if err := removeChunk([]imap.UID{uid}); err == nil {
					done = append(done, byUID[uid])
				}
			}
			continue
		}
		for _, uid := range chunk {
			done = append(done, byUID[uid])
		}
	}
	return done, nil
}

// moveChunk relocates one UID set. go-imap falls back to COPY + \Deleted +
// EXPUNGE by itself on servers without the MOVE extension.
func (p *IMAPProvider) moveChunk(uids []imap.UID, dest string) error {
	if _, err := p.client.Move(imap.UIDSetNum(uids...), dest).Wait(); err != nil {
		return fmt.Errorf("imap: move to %q: %w", dest, err)
	}
	return nil
}

// expungeChunk marks one UID set \Deleted and expunges it — a permanent
// removal, used only when there is no trash to move to (or when already in it).
// Caller holds p.mu.
func (p *IMAPProvider) expungeChunk(uids []imap.UID) error {
	uidSet := imap.UIDSetNum(uids...)
	storeFlags := imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagDeleted},
		Silent: true,
	}
	if err := p.client.Store(uidSet, &storeFlags, nil).Close(); err != nil {
		return fmt.Errorf("imap: mark deleted: %w", err)
	}
	if err := p.client.UIDExpunge(uidSet).Close(); err != nil {
		// UID EXPUNGE needs UIDPLUS. Without it, a plain EXPUNGE is the only
		// option — note it removes *everything* currently flagged \Deleted in
		// the mailbox, including anything another client flagged and has not
		// expunged yet.
		if err2 := p.client.Expunge().Close(); err2 != nil {
			return fmt.Errorf("imap: expunge: %w", err2)
		}
	}
	return nil
}

// SetFlags adds or removes one flag across many messages in a single STORE.
func (p *IMAPProvider) SetFlags(ctx context.Context, folder string, ids []string, flag string, value bool) ([]string, error) {
	uids, byUID, _ := parseUIDs(ids)
	if len(uids) == 0 {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	op := imap.StoreFlagsAdd
	if !value {
		op = imap.StoreFlagsDel
	}
	store := func(chunk []imap.UID) error {
		storeFlags := imap.StoreFlags{Op: op, Flags: []imap.Flag{imap.Flag(flag)}, Silent: true}
		if err := p.client.Store(imap.UIDSetNum(chunk...), &storeFlags, nil).Close(); err != nil {
			return fmt.Errorf("imap: set flag %s: %w", flag, err)
		}
		return nil
	}

	var done []string
	for _, chunk := range chunkUIDs(uids) {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if err := store(chunk); err != nil {
			for _, uid := range chunk {
				if err := store([]imap.UID{uid}); err == nil {
					done = append(done, byUID[uid])
				}
			}
			continue
		}
		for _, uid := range chunk {
			done = append(done, byUID[uid])
		}
	}
	return done, nil
}

// MoveMessages relocates many messages into destFolder in a single MOVE per
// chunk, creating destFolder and retrying once if the server doesn't have it.
func (p *IMAPProvider) MoveMessages(ctx context.Context, folder string, ids []string, destFolder string) ([]string, error) {
	uids, byUID, _ := parseUIDs(ids)
	if len(uids) == 0 {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	created := false
	move := func(chunk []imap.UID) error {
		if _, err := p.client.Move(imap.UIDSetNum(chunk...), destFolder).Wait(); err != nil {
			if created {
				return fmt.Errorf("imap: move to %q: %w", destFolder, err)
			}
			if createErr := p.client.Create(destFolder, nil).Wait(); createErr != nil {
				return fmt.Errorf("imap: move to %q: %w", destFolder, err)
			}
			created = true
			// Some servers require the source mailbox re-selected after CREATE.
			p.mailbox = ""
			if err := p.selectMailbox(folder); err != nil {
				return err
			}
			if _, err := p.client.Move(imap.UIDSetNum(chunk...), destFolder).Wait(); err != nil {
				return fmt.Errorf("imap: move to %q after creating it: %w", destFolder, err)
			}
		}
		return nil
	}

	var done []string
	for _, chunk := range chunkUIDs(uids) {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if err := move(chunk); err != nil {
			for _, uid := range chunk {
				if err := move([]imap.UID{uid}); err == nil {
					done = append(done, byUID[uid])
				}
			}
			continue
		}
		for _, uid := range chunk {
			done = append(done, byUID[uid])
		}
	}
	return done, nil
}

// uidSetString renders a UID set the way it goes on the wire. Only used for
// logging, where seeing "1:1348" instead of a thousand numbers is the point.
func uidSetString(uids []imap.UID) string {
	s := imap.UIDSetNum(uids...).String()
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return strings.TrimSpace(s)
}
