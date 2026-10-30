package ens

import (
	"github.com/islishude/etherview/internal/enscontract"
	"time"
)

type MetricObservation struct {
	Direction, Outcome string
	Duration           time.Duration
}
type Observer interface{ RecordENS(MetricObservation) }
type PrimaryResolution = enscontract.PrimaryResolution
