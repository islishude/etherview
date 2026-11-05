package catalog

import (
	"strings"
	"testing"
)

func TestAddressLogFiltersAreBoundedAndCombined(t *testing.T) {
	valid := "0x" + strings.Repeat("01", 32)
	for _, r := range []AddressLogRequest{
		{ChainID: "1", Limit: 101}, {ChainID: "1", Topics: [4]string{"0x12"}},
		{ChainID: "1", EventID: "x"}, {ChainID: "1", EventID: valid, EventSignature: "Changed()"},
	} {
		if r.validate(false) == nil {
			t.Fatalf("accepted invalid filter: %+v", r)
		}
	}
	for _, r := range []AddressLogRequest{
		{ChainID: "1"}, {ChainID: "1", EventSignature: "Changed()", Topics: [4]string{valid, "", valid}},
		{ChainID: "1", EventID: valid, Topics: [4]string{"", valid, valid}},
	} {
		if err := r.validate(false); err != nil {
			t.Fatal(err)
		}
	}
	if (AddressLogRequest{ChainID: "1", Topics: [4]string{valid}}).validate(true) == nil {
		t.Fatal("directory accepted topics")
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
