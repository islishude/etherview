package enrich

import (
	"strings"
	"testing"
)

func TestParseERC20AndERC721Transfers(t *testing.T) {
	t.Parallel()
	contract := testAddress(0xaa)
	from, to := testAddress(1), testAddress(2)
	erc20 := ParseTokenLog(TokenLog{
		Contract: contract,
		Topics:   []Word{topicTransfer, addressWord(from), addressWord(to)},
		Data:     wordBytes(uintWord(500)),
		LogIndex: 7,
	})
	if erc20.Status != TokenParsed || len(erc20.Events) != 1 || erc20.Events[0].Standard != TokenERC20 || erc20.Events[0].Amount != "500" || erc20.Events[0].SubIndex != 0 {
		t.Fatalf("erc20=%+v", erc20)
	}
	erc721 := ParseTokenLog(TokenLog{
		Contract: contract,
		Topics:   []Word{topicTransfer, addressWord(Address{}), addressWord(to), uintWord(42)},
		LogIndex: 8,
	})
	if erc721.Status != TokenParsed || erc721.Events[0].Standard != TokenERC721 || erc721.Events[0].Kind != TokenMint || erc721.Events[0].TokenID != "42" || erc721.Events[0].Amount != "1" {
		t.Fatalf("erc721=%+v", erc721)
	}
}

func TestParseERC1155SingleAndBatch(t *testing.T) {
	t.Parallel()
	operator, from, to := testAddress(3), testAddress(4), testAddress(5)
	topics := []Word{topicTransferSingle, addressWord(operator), addressWord(from), addressWord(to)}
	singleData := append(wordBytes(uintWord(10)), wordBytes(uintWord(3))...)
	single := ParseTokenLog(TokenLog{Contract: testAddress(9), Topics: topics, Data: singleData, LogIndex: 11})
	if single.Status != TokenParsed || single.Events[0].Standard != TokenERC1155 || single.Events[0].TokenID != "10" || single.Events[0].Amount != "3" {
		t.Fatalf("single=%+v", single)
	}

	batchTopics := append([]Word(nil), topics...)
	batchTopics[0] = topicTransferBatch
	batchData := encodeUintArrayPair([]uint64{10, 11}, []uint64{3, 4})
	batch := ParseTokenLog(TokenLog{Contract: testAddress(9), Topics: batchTopics, Data: batchData, LogIndex: 12})
	if batch.Status != TokenParsed || len(batch.Events) != 2 || batch.Events[0].SubIndex != 0 || batch.Events[1].SubIndex != 1 || batch.Events[1].TokenID != "11" || batch.Events[1].Amount != "4" {
		t.Fatalf("batch=%+v", batch)
	}

	malformed := ParseTokenLog(TokenLog{Contract: testAddress(9), Topics: batchTopics, Data: encodeUintArrayPair([]uint64{1, 2}, []uint64{3})})
	if malformed.Status != TokenMalformed || len(malformed.Events) != 0 || malformed.Warning == "" {
		t.Fatalf("malformed=%+v", malformed)
	}
}

func TestParseERC1155BatchUsesArrayLimitNotArgumentLimit(t *testing.T) {
	t.Parallel()
	operator, from, to := testAddress(3), testAddress(4), testAddress(5)
	topics := []Word{topicTransferBatch, addressWord(operator), addressWord(from), addressWord(to)}
	values := make([]uint64, 257)
	for index := range values {
		values[index] = uint64(index + 1)
	}
	valid := ParseTokenLog(TokenLog{Contract: testAddress(9), Topics: topics, Data: encodeUintArrayPair(values, values)})
	if valid.Status != TokenParsed || len(valid.Events) != 257 || valid.Events[256].SubIndex != 256 {
		t.Fatalf("valid batch status=%s events=%d warning=%q", valid.Status, len(valid.Events), valid.Warning)
	}

	oversized := make([]uint64, DefaultDecodeLimits().MaxArrayElements+1)
	invalid := ParseTokenLog(TokenLog{Contract: testAddress(9), Topics: topics, Data: encodeUintArrayPair(oversized, oversized)})
	if invalid.Status != TokenMalformed || !strings.Contains(invalid.Warning, "exceeds limit 4096") || strings.Contains(invalid.Warning, "too many values") {
		t.Fatalf("oversized batch=%+v", invalid)
	}
}

func TestParseERC1155BatchMintAndBurn(t *testing.T) {
	t.Parallel()
	operator, holder := testAddress(3), testAddress(5)
	data := encodeUintArrayPair([]uint64{10, 11}, []uint64{3, 4})
	tests := []struct {
		name string
		from Address
		to   Address
		kind TokenEventKind
	}{
		{name: "batch mint", from: Address{}, to: holder, kind: TokenMint},
		{name: "batch burn", from: holder, to: Address{}, kind: TokenBurn},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := ParseTokenLog(TokenLog{
				Contract: testAddress(9),
				Topics: []Word{
					topicTransferBatch, addressWord(operator), addressWord(test.from), addressWord(test.to),
				},
				Data: data, LogIndex: 12,
			})
			if result.Status != TokenParsed || len(result.Events) != 2 {
				t.Fatalf("result=%+v", result)
			}
			for index, event := range result.Events {
				if event.Kind != test.kind || event.Standard != TokenERC1155 || event.SubIndex != uint32(index) {
					t.Fatalf("event[%d]=%+v", index, event)
				}
			}
		})
	}
}

func TestTokenMalformedKnownEventIsNotUnknownOrPartial(t *testing.T) {
	t.Parallel()
	result := ParseTokenLog(TokenLog{
		Topics: []Word{topicTransfer, addressWord(testAddress(1)), addressWord(testAddress(2))},
		Data:   []byte{1},
	})
	if result.Status != TokenMalformed || len(result.Events) != 0 {
		t.Fatalf("result=%+v", result)
	}
	invalidBool := uintWord(2)
	result = ParseTokenLog(TokenLog{
		Topics: []Word{topicApprovalForAll, addressWord(testAddress(1)), addressWord(testAddress(2))},
		Data:   wordBytes(invalidBool),
	})
	if result.Status != TokenMalformed {
		t.Fatalf("result=%+v", result)
	}
}

func FuzzParseTokenLogDoesNotPanic(f *testing.F) {
	f.Add([]byte{1, 2, 3}, []byte{4})
	f.Add(wordBytes(topicTransfer), wordBytes(uintWord(1)))
	f.Fuzz(func(t *testing.T, topicBytes, data []byte) {
		if len(topicBytes) > 128 || len(data) > 2048 {
			t.Skip()
		}
		var topic Word
		copy(topic[:], topicBytes)
		_ = ParseTokenLog(TokenLog{Topics: []Word{topic}, Data: data})
	})
}

func encodeUintArrayPair(left, right []uint64) []byte {
	leftSize := 32 + 32*len(left)
	result := append(wordBytes(uintWord(64)), wordBytes(uintWord(uint64(64+leftSize)))...)
	result = append(result, wordBytes(uintWord(uint64(len(left))))...)
	for _, value := range left {
		result = append(result, wordBytes(uintWord(value))...)
	}
	result = append(result, wordBytes(uintWord(uint64(len(right))))...)
	for _, value := range right {
		result = append(result, wordBytes(uintWord(value))...)
	}
	return result
}

func wordBytes(word Word) []byte { return append([]byte(nil), word[:]...) }

func selectorBytes(signature string) []byte {
	selector := SignatureSelector(signature)
	return append([]byte(nil), selector[:]...)
}
