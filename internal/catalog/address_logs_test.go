package catalog

import (
	"strings"
	"testing"
)

func TestAddressLogFiltersAreBoundedAndExclusive(t *testing.T) {
	zero, three, four := 0, 3, 4
	topic := "0x" + string(make([]byte, 64))
	valid := "0x0000000000000000000000000000000000000000000000000000000000000001"
	for _, r := range []AddressLogRequest{
		{ChainID: "1", Limit: 101}, {ChainID: "1", Topic: valid}, {ChainID: "1", TopicIndex: &zero},
		{ChainID: "1", Topic: topic, TopicIndex: &zero}, {ChainID: "1", Topic: valid, TopicIndex: &four},
		{ChainID: "1", EventSignature: "Transfer(address)", Topic: valid, TopicIndex: &zero},
	} {
		if r.validate(false) == nil {
			t.Fatalf("accepted invalid filter: %+v", r)
		}
	}
	for _, r := range []AddressLogRequest{{ChainID: "1"}, {ChainID: "1", EventSignature: "Anonymous(uint256)"}, {ChainID: "1", Topic: valid, TopicIndex: &three}} {
		if err := r.validate(false); err != nil {
			t.Fatalf("valid filter: %v", err)
		}
	}
}

func TestAddressLogRanges(t *testing.T) {
	for _, directory := range []bool{false, true} {
		for _, bounds := range [][2]string{{"0", "0"}, {"", "10"}, {"10", ""}, {"9007199254740993", "9007199254740994"}} {
			r := AddressLogRequest{ChainID: "1", FromBlock: bounds[0], ToBlock: bounds[1]}
			if err := r.validate(directory); err != nil {
				t.Fatal(r, err)
			}
		}
		for _, bounds := range [][2]string{{"-1", ""}, {"01", ""}, {"1.5", ""}, {"2", "1"}, {strings.Repeat("9", 78), ""}, {"", " 1"}} {
			r := AddressLogRequest{ChainID: "1", FromBlock: bounds[0], ToBlock: bounds[1]}
			if r.validate(directory) == nil {
				t.Fatalf("accepted range: %+v", r)
			}
		}
	}
}
