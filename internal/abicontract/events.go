package abicontract

// EventParameter is the bounded recursive ABI shape of a historical event input.
type EventParameter struct {
	Name       string           `json:"name"`
	Type       string           `json:"type"`
	Components []EventParameter `json:"components,omitempty"`
}
type EventField struct {
	EventParameter
	ParameterIndex int `json:"parameter_index"`
	TopicIndex     int `json:"topic_index"`
}
type EventDefinition struct {
	Anonymous     bool         `json:"anonymous"`
	IndexedFields []EventField `json:"indexed_fields"`
}
