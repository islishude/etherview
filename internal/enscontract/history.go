package enscontract

import "context"

// History fields are inert, bounded event evidence, not current resolver records.
type HistoryField struct {
	Key       string `json:"key"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	Truncated bool   `json:"truncated"`
}
type HistoryCoverage struct {
	State   string  `json:"state"`
	Start   string  `json:"start"`
	Scanned *string `json:"scanned,omitempty"`
	Target  *string `json:"target,omitempty"`
	Pending string  `json:"pending"`
}
type HistoryTransaction struct {
	TransactionHash string   `json:"transaction_hash"`
	BlockHash       string   `json:"block_hash"`
	BlockNumber     string   `json:"block_number"`
	Timestamp       string   `json:"timestamp"`
	Actions         []string `json:"actions"`
	EventCount      string   `json:"event_count"`
}
type HistoryEvent struct {
	LogIndex  string         `json:"log_index"`
	Emitter   string         `json:"emitter"`
	EventName string         `json:"event_name"`
	Action    string         `json:"action"`
	Fields    []HistoryField `json:"fields"`
}
type HistoryPage struct {
	Name     string               `json:"name"`
	State    string               `json:"state"`
	Coverage HistoryCoverage      `json:"coverage"`
	Snapshot string               `json:"snapshot"`
	Items    []HistoryTransaction `json:"items"`
}
type HistoryEventsPage struct {
	Items []HistoryEvent `json:"items"`
}
type HistoryRequest struct {
	Name, Cursor string
	Limit        int
}
type HistoryEventsRequest struct {
	Name, TransactionHash, BlockHash, Snapshot, Cursor string
	Limit                                              int
}
type HistoryReader interface {
	NameHistory(context.Context, HistoryRequest) (HistoryPage, string, error)
	NameHistoryEvents(context.Context, HistoryEventsRequest) (HistoryEventsPage, string, error)
}
