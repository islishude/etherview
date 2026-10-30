package enscontract

import "context"

// Field never encodes missing evidence as an empty value. Expiry values are
// unsigned Unix seconds, so even uint64 protocol sentinels remain lossless.
type Field struct {
	State string `json:"state"`
	Value string `json:"value,omitempty"`
	Model string `json:"model,omitempty"`
}
type RegistrationProfile struct {
	RegistrationState string `json:"registration_state"`
	Registrant        Field  `json:"registrant"`
	Controller        Field  `json:"controller"`
	ExpirationTime    Field  `json:"expiration_time"`
}
type Observation struct {
	Publication string `json:"publication"`
	BlockNumber string `json:"observed_block_number"`
	BlockHash   string `json:"observed_block_hash"`
	ValidUntil  string `json:"valid_until"`
}
type NameDetail struct {
	Name  string `json:"name"`
	State string `json:"state"`
	RegistrationProfile
	ResolvedAddress       Field        `json:"resolved_address"`
	ProfileObservation    *Observation `json:"profile_observation,omitempty"`
	ResolutionObservation *Observation `json:"resolution_observation,omitempty"`
}
type DetailReader interface {
	NameDetail(context.Context, string) (NameDetail, error)
}

func PendingProfile() RegistrationProfile {
	return RegistrationProfile{RegistrationState: "pending", Registrant: Field{State: "pending"}, Controller: Field{State: "pending"}, ExpirationTime: Field{State: "pending"}}
}
