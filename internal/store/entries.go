package store

import (
	"encoding/json"
	"time"
)

// LogEntry is one decoded ledger record, for audit/display.
// Kind values mirror the internal record kinds: "acct", "acct-del", "asset",
// "asset-del", "config", "tx", "tx-edit", "tx-del", "label", "label-del".
type LogEntry struct {
	Seq  int
	Ts   time.Time
	Kind string
	Data json.RawMessage
}

// LogEntries returns all log entries in file order (oldest first).
func (f *File) LogEntries() []LogEntry {
	out := make([]LogEntry, len(f.entries))
	for i, e := range f.entries {
		ts, _ := time.Parse(time.RFC3339Nano, e.rec.Ts)
		out[i] = LogEntry{
			Seq:  i + 1,
			Ts:   ts,
			Kind: string(e.rec.K),
			Data: e.rec.D,
		}
	}
	return out
}
