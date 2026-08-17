package ethrpc

import "time"

type Observation struct {
	Endpoint     string
	Purpose      Purpose
	Method       string
	BatchSize    int
	SuccessCount int
	ErrorCount   int
	Duration     time.Duration
}

type EndpointState struct {
	Endpoint            string
	State               string
	ConsecutiveFailures uint32
	Cooldown            time.Duration
}

// Observer receives bounded endpoint names, architectural purposes, methods,
// counts, and timings. It never receives URLs, parameters, results, or errors.
type Observer interface {
	RecordRPC(Observation)
}

type EndpointStateObserver interface {
	RecordRPCEndpointState(EndpointState)
}
