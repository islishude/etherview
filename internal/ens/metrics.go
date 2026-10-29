package ens

import (
	"github.com/islishude/etherview/internal/enscontract"
	"time"
)

type MetricObservation struct {
	Source             Source
	Direction, Outcome string
	Duration           time.Duration
}
type Observer interface{ RecordENS(MetricObservation) }
type ForwardResolution = enscontract.ForwardResolution
type PrimaryResolution = enscontract.PrimaryResolution
