package multiagent

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Sticky tool visibility, scoped to a conversation and to this process.
//
// Background: Eino's toolsearch middleware unlocks a tool for the remainder of the *run*.
// Every user turn starts a new run, so without stickiness a tool discovered through
// tool_search disappears on the next turn and the model has to unlock it again — one extra
// model call round-trip plus a re-sent tool list, every turn, for every tool it needs.
//
// The registry is bounded on both axes (per-conversation cap and TTL) so a long-lived process
// cannot accumulate entries without limit.

const (
	maxStickyUnlockedTools = 64
	stickyUnlockTTL        = 6 * time.Hour
)

type stickyUnlockEntry struct {
	names map[string]struct{}
	seen  time.Time
}

var (
	stickyUnlockMu     sync.Mutex
	stickyUnlockByConv = map[string]*stickyUnlockEntry{}
)

// RecordToolUnlocks remembers tools unlocked by tool_search in this conversation. Blank and
// duplicate names are ignored; the per-conversation cap keeps the first-come set stable.
func RecordToolUnlocks(conversationID string, names []string) {
	conv := strings.TrimSpace(conversationID)
	if conv == "" || len(names) == 0 {
		return
	}
	now := time.Now()
	stickyUnlockMu.Lock()
	defer stickyUnlockMu.Unlock()
	entry := stickyUnlockByConv[conv]
	if entry == nil || now.Sub(entry.seen) > stickyUnlockTTL {
		entry = &stickyUnlockEntry{names: map[string]struct{}{}}
		stickyUnlockByConv[conv] = entry
	}
	entry.seen = now
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := entry.names[name]; ok {
			continue
		}
		if len(entry.names) >= maxStickyUnlockedTools {
			break
		}
		entry.names[name] = struct{}{}
	}
}

// StickyUnlockedTools returns the discovery-resident tool names for this conversation, or nil
// when there are none (or the entry has expired).
func StickyUnlockedTools(conversationID string) []string {
	conv := strings.TrimSpace(conversationID)
	if conv == "" {
		return nil
	}
	stickyUnlockMu.Lock()
	defer stickyUnlockMu.Unlock()
	entry := stickyUnlockByConv[conv]
	if entry == nil {
		return nil
	}
	if time.Since(entry.seen) > stickyUnlockTTL {
		delete(stickyUnlockByConv, conv)
		return nil
	}
	if len(entry.names) == 0 {
		return nil
	}
	out := make([]string, 0, len(entry.names))
	for name := range entry.names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// resetStickyUnlocksForTest clears the registry (tests only).
func resetStickyUnlocksForTest() {
	stickyUnlockMu.Lock()
	defer stickyUnlockMu.Unlock()
	stickyUnlockByConv = map[string]*stickyUnlockEntry{}
}
