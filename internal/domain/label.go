package domain

type LabelID string

// Label tags a position — a specific (account, asset) couple — with a free-form
// name. Each assignment is its own random-id entity, so a pair can carry any
// number of labels and two machines tagging the same pair union without
// conflict. Account and Asset are both required today; an empty id is reserved
// for a future "wildcard" (all accounts / all assets).
type Label struct {
	ID      LabelID   `json:"id"`
	Account AccountID `json:"account"`
	Asset   AssetID   `json:"asset"`
	Name    string    `json:"name"`
}
