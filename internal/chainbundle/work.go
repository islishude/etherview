package chainbundle

import (
	"errors"
	"fmt"
	"math"
)

// WorkSize is the owned raw-memory and relational-row footprint of one
// already decoded bundle. RawBytes deliberately counts nested Raw slices even
// when their bytes also occur in a parent JSON document because each slice is
// independently owned by Bundle today.
type WorkSize struct {
	RawBytes uint64
	Rows     uint64
}

// MeasureWork performs only bounded alignment and overflow checks. It does not
// re-decode or cryptographically revalidate the bundle; callers use it after a
// hostile RPC or stored-data decoder has already established those invariants.
func MeasureWork(bundle Bundle) (WorkSize, error) {
	if bundle.Block == nil {
		return WorkSize{}, errors.New("measure bundle work: block is nil")
	}
	transactions := bundle.Block.Transactions()
	if len(bundle.RawTransactions) != len(transactions) ||
		len(bundle.Receipts) != len(transactions) ||
		len(bundle.RawReceipts) != len(transactions) ||
		len(bundle.RawLogs) != len(transactions) ||
		len(bundle.RawWithdrawals) != len(bundle.Block.Withdrawals()) {
		return WorkSize{}, errors.New("measure bundle work: raw and typed collections are misaligned")
	}

	work := WorkSize{Rows: 1}
	if err := work.addBytes(len(bundle.RawBlock)); err != nil {
		return WorkSize{}, err
	}
	for index := range transactions {
		if bundle.Receipts[index] == nil || len(bundle.RawLogs[index]) != len(bundle.Receipts[index].Logs) {
			return WorkSize{}, fmt.Errorf("measure bundle work: receipt %d is misaligned", index)
		}
		if err := work.addRows(3); err != nil {
			return WorkSize{}, err
		}
		if err := work.addBytes(len(bundle.RawTransactions[index])); err != nil {
			return WorkSize{}, err
		}
		if err := work.addBytes(len(bundle.RawReceipts[index])); err != nil {
			return WorkSize{}, err
		}
		for _, raw := range bundle.RawLogs[index] {
			if err := work.addRows(1); err != nil {
				return WorkSize{}, err
			}
			if err := work.addBytes(len(raw)); err != nil {
				return WorkSize{}, err
			}
		}
	}
	for _, raw := range bundle.RawUncles {
		if err := work.addBytes(len(raw)); err != nil {
			return WorkSize{}, err
		}
	}
	for _, raw := range bundle.RawWithdrawals {
		if err := work.addRows(1); err != nil {
			return WorkSize{}, err
		}
		if err := work.addBytes(len(raw)); err != nil {
			return WorkSize{}, err
		}
	}
	return work, nil
}

func (work *WorkSize) Add(other WorkSize) error {
	if work == nil {
		return errors.New("add bundle work: nil destination")
	}
	if math.MaxUint64-work.RawBytes < other.RawBytes || math.MaxUint64-work.Rows < other.Rows {
		return errors.New("add bundle work: size overflow")
	}
	work.RawBytes += other.RawBytes
	work.Rows += other.Rows
	return nil
}

func (work *WorkSize) addBytes(value int) error {
	if value < 0 || math.MaxUint64-work.RawBytes < uint64(value) {
		return errors.New("measure bundle work: raw byte size overflow")
	}
	work.RawBytes += uint64(value)
	return nil
}

func (work *WorkSize) addRows(value uint64) error {
	if math.MaxUint64-work.Rows < value {
		return errors.New("measure bundle work: row count overflow")
	}
	work.Rows += value
	return nil
}
