// Package stagecontract owns durable enrichment stage identities shared by
// schedulers, producers, workers, and maintenance commands.
package stagecontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

type ID struct {
	Name    string
	Version uint32
}

func (stage ID) Validate() error {
	if stage.Name == "" {
		return errors.New("stage name is empty")
	}
	for _, character := range stage.Name {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' {
			return fmt.Errorf("stage name %q contains an unsupported character", stage.Name)
		}
	}
	if stage.Version == 0 {
		return errors.New("stage version must be positive")
	}
	return nil
}

func (stage ID) String() string { return fmt.Sprintf("%s@%d", stage.Name, stage.Version) }

var (
	Proxy         = ID{Name: "proxy", Version: 2}
	ABI           = ID{Name: "abi", Version: 4}
	Token         = ID{Name: "token", Version: 1}
	Stats         = ID{Name: "stats", Version: 3}
	Trace         = ID{Name: "trace", Version: 3}
	StateDiff     = ID{Name: "state_diff", Version: 3}
	UserOperation = ID{Name: "userop", Version: 1}
	Holder        = ID{Name: "holder", Version: 1}
)

type ReplaySource struct {
	Kind string
	Key  string
}

func (source ReplaySource) Validate() error {
	if source == (ReplaySource{}) {
		return nil
	}
	if strings.TrimSpace(source.Kind) == "" || len(source.Kind) > 64 {
		return errors.New("enrichment replay source kind must contain between 1 and 64 bytes")
	}
	if strings.TrimSpace(source.Key) == "" || len(source.Key) > 256 {
		return errors.New("enrichment replay source key must contain between 1 and 256 bytes")
	}
	return nil
}

type EnqueueRequest struct {
	Kind        string
	Stage       ID
	ChainID     string
	BlockHash   common.Hash
	BlockNumber uint64
	Payload     json.RawMessage
	Priority    int
	MaxAttempts uint32
	Replay      ReplaySource
}
