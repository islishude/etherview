package enrich

import (
	"errors"
	"fmt"
	"math/big"
)

type TokenStandard string

const (
	TokenERC20           TokenStandard = "erc20"
	TokenERC721          TokenStandard = "erc721"
	TokenERC1155         TokenStandard = "erc1155"
	TokenERC721Or1155    TokenStandard = "erc721_or_erc1155"
	TokenStandardUnknown TokenStandard = "unknown"
)

type TokenEventKind string

const (
	TokenTransfer       TokenEventKind = "transfer"
	TokenMint           TokenEventKind = "mint"
	TokenBurn           TokenEventKind = "burn"
	TokenApproval       TokenEventKind = "approval"
	TokenApprovalForAll TokenEventKind = "approval_for_all"
)

type TokenParseStatus string

const (
	TokenParsed    TokenParseStatus = "parsed"
	TokenUnknown   TokenParseStatus = "unknown"
	TokenMalformed TokenParseStatus = "malformed"
)

type TokenLog struct {
	Contract Address
	Topics   []Word
	Data     []byte
	LogIndex uint64
}

// TokenEvent uses decimal strings for values that may exceed JavaScript's
// integer precision. SubIndex is stable for ERC-1155 batches.
type TokenEvent struct {
	Standard   TokenStandard
	Kind       TokenEventKind
	Contract   Address
	Operator   *Address
	From       *Address
	To         *Address
	Owner      *Address
	Spender    *Address
	TokenID    string
	Amount     string
	Approved   *bool
	LogIndex   uint64
	SubIndex   uint32
	Confidence Confidence
}

type TokenParseResult struct {
	Status  TokenParseStatus
	Events  []TokenEvent
	Warning string
}

var (
	topicTransfer       = SignatureHash("Transfer(address,address,uint256)")
	topicApproval       = SignatureHash("Approval(address,address,uint256)")
	topicApprovalForAll = SignatureHash("ApprovalForAll(address,address,bool)")
	topicTransferSingle = SignatureHash("TransferSingle(address,address,address,uint256,uint256)")
	topicTransferBatch  = SignatureHash("TransferBatch(address,address,address,uint256[],uint256[])")
)

// ParseTokenLog recognizes only exact standard layouts. Recognized topics with
// malformed values return TokenMalformed and no partial state changes.
func ParseTokenLog(log TokenLog) TokenParseResult {
	if len(log.Topics) == 0 {
		return TokenParseResult{Status: TokenUnknown}
	}
	switch log.Topics[0] {
	case topicTransfer:
		return parseTransfer(log)
	case topicApproval:
		return parseApproval(log)
	case topicApprovalForAll:
		return parseApprovalForAll(log)
	case topicTransferSingle:
		return parseTransferSingle(log)
	case topicTransferBatch:
		return parseTransferBatch(log)
	default:
		return TokenParseResult{Status: TokenUnknown}
	}
}

func parseTransfer(log TokenLog) TokenParseResult {
	if len(log.Topics) != 3 && len(log.Topics) != 4 {
		return malformedToken("Transfer has %d topics; want 3 for ERC-20 or 4 for ERC-721", len(log.Topics))
	}
	from, err := AddressFromWord(log.Topics[1])
	if err != nil {
		return malformedToken("Transfer from: %v", err)
	}
	to, err := AddressFromWord(log.Topics[2])
	if err != nil {
		return malformedToken("Transfer to: %v", err)
	}
	if len(log.Topics) == 3 {
		amount, err := staticUint256(log.Data)
		if err != nil {
			return malformedToken("ERC-20 Transfer amount: %v", err)
		}
		event := TokenEvent{
			Standard:   TokenERC20,
			Kind:       transferAction(from, to),
			Contract:   log.Contract,
			From:       addressPointer(from),
			To:         addressPointer(to),
			Amount:     amount,
			LogIndex:   log.LogIndex,
			Confidence: ConfidenceHigh,
		}
		return parsedToken(event)
	}
	if len(log.Data) != 0 {
		return malformedToken("ERC-721 Transfer must have empty data, got %d bytes", len(log.Data))
	}
	event := TokenEvent{
		Standard:   TokenERC721,
		Kind:       transferAction(from, to),
		Contract:   log.Contract,
		From:       addressPointer(from),
		To:         addressPointer(to),
		TokenID:    wordDecimal(log.Topics[3]),
		Amount:     "1",
		LogIndex:   log.LogIndex,
		Confidence: ConfidenceHigh,
	}
	return parsedToken(event)
}

func parseApproval(log TokenLog) TokenParseResult {
	if len(log.Topics) != 3 && len(log.Topics) != 4 {
		return malformedToken("Approval has %d topics; want 3 for ERC-20 or 4 for ERC-721", len(log.Topics))
	}
	owner, err := AddressFromWord(log.Topics[1])
	if err != nil {
		return malformedToken("Approval owner: %v", err)
	}
	spender, err := AddressFromWord(log.Topics[2])
	if err != nil {
		return malformedToken("Approval spender: %v", err)
	}
	event := TokenEvent{
		Kind:       TokenApproval,
		Contract:   log.Contract,
		Owner:      addressPointer(owner),
		Spender:    addressPointer(spender),
		LogIndex:   log.LogIndex,
		Confidence: ConfidenceHigh,
	}
	if len(log.Topics) == 3 {
		amount, err := staticUint256(log.Data)
		if err != nil {
			return malformedToken("ERC-20 Approval amount: %v", err)
		}
		event.Standard = TokenERC20
		event.Amount = amount
		return parsedToken(event)
	}
	if len(log.Data) != 0 {
		return malformedToken("ERC-721 Approval must have empty data, got %d bytes", len(log.Data))
	}
	event.Standard = TokenERC721
	event.TokenID = wordDecimal(log.Topics[3])
	return parsedToken(event)
}

func parseApprovalForAll(log TokenLog) TokenParseResult {
	if len(log.Topics) != 3 {
		return malformedToken("ApprovalForAll has %d topics; want 3", len(log.Topics))
	}
	owner, err := AddressFromWord(log.Topics[1])
	if err != nil {
		return malformedToken("ApprovalForAll owner: %v", err)
	}
	operator, err := AddressFromWord(log.Topics[2])
	if err != nil {
		return malformedToken("ApprovalForAll operator: %v", err)
	}
	approved, err := staticBool(log.Data)
	if err != nil {
		return malformedToken("ApprovalForAll approved: %v", err)
	}
	return parsedToken(TokenEvent{
		Standard:   TokenERC721Or1155,
		Kind:       TokenApprovalForAll,
		Contract:   log.Contract,
		Owner:      addressPointer(owner),
		Operator:   addressPointer(operator),
		Approved:   &approved,
		LogIndex:   log.LogIndex,
		Confidence: ConfidenceInferred,
	})
}

func parseTransferSingle(log TokenLog) TokenParseResult {
	if len(log.Topics) != 4 {
		return malformedToken("TransferSingle has %d topics; want 4", len(log.Topics))
	}
	operator, from, to, result := parseERC1155Addresses(log.Topics)
	if result.Status != "" {
		return result
	}
	if len(log.Data) != 64 {
		return malformedToken("TransferSingle data is %d bytes; want 64", len(log.Data))
	}
	idWord, _ := WordFromBytes(log.Data[:32])
	amountWord, _ := WordFromBytes(log.Data[32:])
	return parsedToken(TokenEvent{
		Standard:   TokenERC1155,
		Kind:       transferAction(from, to),
		Contract:   log.Contract,
		Operator:   addressPointer(operator),
		From:       addressPointer(from),
		To:         addressPointer(to),
		TokenID:    wordDecimal(idWord),
		Amount:     wordDecimal(amountWord),
		LogIndex:   log.LogIndex,
		Confidence: ConfidenceHigh,
	})
}

func parseTransferBatch(log TokenLog) TokenParseResult {
	if len(log.Topics) != 4 {
		return malformedToken("TransferBatch has %d topics; want 4", len(log.Topics))
	}
	operator, from, to, result := parseERC1155Addresses(log.Topics)
	if result.Status != "" {
		return result
	}
	arrayType, err := parseABIType(abiParameter{Type: "uint256[]"}, 1, DefaultDecodeLimits().MaxDepth)
	if err != nil {
		return malformedToken("TransferBatch decoder initialization: %v", err)
	}
	values, err := decodeABIValues([]*abiType{arrayType, arrayType}, log.Data, DefaultDecodeLimits())
	if err != nil {
		return malformedToken("TransferBatch data: %v", err)
	}
	ids, idsOK := values[0].([]any)
	amounts, amountsOK := values[1].([]any)
	if !idsOK || !amountsOK {
		return malformedToken("TransferBatch arrays have unexpected decoded types")
	}
	if len(ids) != len(amounts) {
		return malformedToken("TransferBatch has %d ids but %d amounts", len(ids), len(amounts))
	}
	events := make([]TokenEvent, len(ids))
	for index := range ids {
		id, idOK := ids[index].(string)
		amount, amountOK := amounts[index].(string)
		if !idOK || !amountOK {
			return malformedToken("TransferBatch element %d is not uint256", index)
		}
		events[index] = TokenEvent{
			Standard:   TokenERC1155,
			Kind:       transferAction(from, to),
			Contract:   log.Contract,
			Operator:   addressPointer(operator),
			From:       addressPointer(from),
			To:         addressPointer(to),
			TokenID:    id,
			Amount:     amount,
			LogIndex:   log.LogIndex,
			SubIndex:   uint32(index),
			Confidence: ConfidenceHigh,
		}
	}
	return TokenParseResult{Status: TokenParsed, Events: events}
}

func parseERC1155Addresses(topics []Word) (Address, Address, Address, TokenParseResult) {
	operator, err := AddressFromWord(topics[1])
	if err != nil {
		return Address{}, Address{}, Address{}, malformedToken("ERC-1155 operator: %v", err)
	}
	from, err := AddressFromWord(topics[2])
	if err != nil {
		return Address{}, Address{}, Address{}, malformedToken("ERC-1155 from: %v", err)
	}
	to, err := AddressFromWord(topics[3])
	if err != nil {
		return Address{}, Address{}, Address{}, malformedToken("ERC-1155 to: %v", err)
	}
	return operator, from, to, TokenParseResult{}
}

func staticUint256(data []byte) (string, error) {
	if len(data) != 32 {
		return "", fmt.Errorf("value is %d bytes; want 32", len(data))
	}
	word, err := WordFromBytes(data)
	if err != nil {
		return "", err
	}
	return wordDecimal(word), nil
}

func staticBool(data []byte) (bool, error) {
	if len(data) != 32 {
		return false, fmt.Errorf("value is %d bytes; want 32", len(data))
	}
	word, err := WordFromBytes(data)
	if err != nil {
		return false, err
	}
	if !allByte(word[:31], 0) || word[31] > 1 {
		return false, errors.New("boolean word is neither zero nor one")
	}
	return word[31] == 1, nil
}

func wordDecimal(word Word) string { return new(big.Int).SetBytes(word[:]).String() }

func addressPointer(value Address) *Address {
	copy := value
	return &copy
}

func transferAction(from, to Address) TokenEventKind {
	if from == (Address{}) {
		return TokenMint
	}
	if to == (Address{}) {
		return TokenBurn
	}
	return TokenTransfer
}

func parsedToken(event TokenEvent) TokenParseResult {
	return TokenParseResult{Status: TokenParsed, Events: []TokenEvent{event}}
}

func malformedToken(format string, arguments ...any) TokenParseResult {
	return TokenParseResult{Status: TokenMalformed, Warning: fmt.Sprintf(format, arguments...)}
}
