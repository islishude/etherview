package erc4337

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"strings"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

var ErrNotBundleMethod = errors.New("EntryPoint calldata is not a supported bundle method")

const userOperationV06Components = `
  {"name":"sender","type":"address"},
  {"name":"nonce","type":"uint256"},
  {"name":"initCode","type":"bytes"},
  {"name":"callData","type":"bytes"},
  {"name":"callGasLimit","type":"uint256"},
  {"name":"verificationGasLimit","type":"uint256"},
  {"name":"preVerificationGas","type":"uint256"},
  {"name":"maxFeePerGas","type":"uint256"},
  {"name":"maxPriorityFeePerGas","type":"uint256"},
  {"name":"paymasterAndData","type":"bytes"},
  {"name":"signature","type":"bytes"}`

const packedUserOperationComponents = `
  {"name":"sender","type":"address"},
  {"name":"nonce","type":"uint256"},
  {"name":"initCode","type":"bytes"},
  {"name":"callData","type":"bytes"},
  {"name":"accountGasLimits","type":"bytes32"},
  {"name":"preVerificationGas","type":"uint256"},
  {"name":"gasFees","type":"bytes32"},
  {"name":"paymasterAndData","type":"bytes"},
  {"name":"signature","type":"bytes"}`

func entryPointABI(components string) string {
	return `[
    {"type":"function","name":"handleOps","inputs":[
      {"name":"ops","type":"tuple[]","components":[` + components + `]},
      {"name":"beneficiary","type":"address"}
    ],"outputs":[]},
    {"type":"function","name":"handleAggregatedOps","inputs":[
      {"name":"opsPerAggregator","type":"tuple[]","components":[
        {"name":"userOps","type":"tuple[]","components":[` + components + `]},
        {"name":"aggregator","type":"address"},
        {"name":"signature","type":"bytes"}
      ]},
      {"name":"beneficiary","type":"address"}
    ],"outputs":[]}
  ]`
}

var (
	entryPointV06ABI    = mustABI(entryPointABI(userOperationV06Components))
	entryPointPackedABI = mustABI(entryPointABI(packedUserOperationComponents))
)

func mustABI(definition string) gethabi.ABI {
	parsed, err := gethabi.JSON(strings.NewReader(definition))
	if err != nil {
		panic(err)
	}
	return parsed
}

type userOperationV06Wire struct {
	Sender               common.Address
	Nonce                *big.Int
	InitCode             []byte
	CallData             []byte
	CallGasLimit         *big.Int
	VerificationGasLimit *big.Int
	PreVerificationGas   *big.Int
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
	PaymasterAndData     []byte
	Signature            []byte
}

type userOperationsPerAggregatorV06Wire struct {
	UserOps    []userOperationV06Wire
	Aggregator common.Address
	Signature  []byte
}

type packedUserOperationWire struct {
	Sender             common.Address
	Nonce              *big.Int
	InitCode           []byte
	CallData           []byte
	AccountGasLimits   [32]byte
	PreVerificationGas *big.Int
	GasFees            [32]byte
	PaymasterAndData   []byte
	Signature          []byte
}

type userOperationsPerAggregatorPackedWire struct {
	UserOps    []packedUserOperationWire
	Aggregator common.Address
	Signature  []byte
}

func decodeHandleCalldata(version Version, calldata []byte) ([]Request, common.Address, error) {
	if len(calldata) < 4 {
		return nil, common.Address{}, errors.New("EntryPoint calldata is shorter than a selector")
	}
	definition := entryPointPackedABI
	if version == Version06 {
		definition = entryPointV06ABI
	}
	method, err := definition.MethodById(calldata[:4])
	if err != nil || (method.Name != "handleOps" && method.Name != "handleAggregatedOps") {
		return nil, common.Address{}, ErrNotBundleMethod
	}
	values, err := method.Inputs.Unpack(calldata[4:])
	if err != nil || len(values) != 2 {
		return nil, common.Address{}, fmt.Errorf("decode EntryPoint %s calldata: %w", method.Name, err)
	}
	repacked, err := method.Inputs.Pack(values...)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("re-encode EntryPoint %s calldata: %w", method.Name, err)
	}
	if !bytes.Equal(repacked, calldata[4:]) {
		return nil, common.Address{}, errors.New("EntryPoint calldata is not canonical ABI encoding")
	}
	beneficiary, ok := values[1].(common.Address)
	if !ok {
		return nil, common.Address{}, errors.New("EntryPoint beneficiary has an invalid ABI type")
	}
	if version == Version06 {
		requests, decodeErr := decodeV06Requests(method.Name, values[0])
		return requests, beneficiary, decodeErr
	}
	requests, err := decodePackedRequests(version, method.Name, values[0])
	return requests, beneficiary, err
}

func decodeV06Requests(method string, value any) ([]Request, error) {
	var requests []Request
	switch method {
	case "handleOps":
		converted, ok := gethabi.ConvertType(value, new([]userOperationV06Wire)).(*[]userOperationV06Wire)
		if !ok {
			return nil, errors.New("convert v0.6 UserOperation array")
		}
		requests = make([]Request, 0, len(*converted))
		for _, operation := range *converted {
			request, err := normalizeV06(operation)
			if err != nil {
				return nil, err
			}
			requests = append(requests, request)
		}
	case "handleAggregatedOps":
		groups, ok := gethabi.ConvertType(value, new([]userOperationsPerAggregatorV06Wire)).(*[]userOperationsPerAggregatorV06Wire)
		if !ok {
			return nil, errors.New("convert v0.6 aggregated UserOperation array")
		}
		for _, group := range *groups {
			for _, operation := range group.UserOps {
				request, err := normalizeV06(operation)
				if err != nil {
					return nil, err
				}
				request.Aggregator = addressPointer(group.Aggregator)
				request.AggregatedSignature = bytes.Clone(group.Signature)
				requests = append(requests, request)
			}
		}
	}
	return requests, nil
}

func decodePackedRequests(version Version, method string, value any) ([]Request, error) {
	var requests []Request
	switch method {
	case "handleOps":
		converted, ok := gethabi.ConvertType(value, new([]packedUserOperationWire)).(*[]packedUserOperationWire)
		if !ok {
			return nil, errors.New("convert packed UserOperation array")
		}
		requests = make([]Request, 0, len(*converted))
		for _, operation := range *converted {
			request, err := normalizePacked(version, operation)
			if err != nil {
				return nil, err
			}
			requests = append(requests, request)
		}
	case "handleAggregatedOps":
		groups, ok := gethabi.ConvertType(value, new([]userOperationsPerAggregatorPackedWire)).(*[]userOperationsPerAggregatorPackedWire)
		if !ok {
			return nil, errors.New("convert aggregated packed UserOperation array")
		}
		for _, group := range *groups {
			for _, operation := range group.UserOps {
				request, err := normalizePacked(version, operation)
				if err != nil {
					return nil, err
				}
				request.Aggregator = addressPointer(group.Aggregator)
				request.AggregatedSignature = bytes.Clone(group.Signature)
				requests = append(requests, request)
			}
		}
	}
	return requests, nil
}

func normalizeV06(operation userOperationV06Wire) (Request, error) {
	request := Request{
		Sender: operation.Sender, Nonce: cloneBig(operation.Nonce),
		InitCode: bytes.Clone(operation.InitCode), CallData: bytes.Clone(operation.CallData),
		CallGasLimit: cloneBig(operation.CallGasLimit), VerificationGasLimit: cloneBig(operation.VerificationGasLimit),
		PreVerificationGas: cloneBig(operation.PreVerificationGas), MaxFeePerGas: cloneBig(operation.MaxFeePerGas),
		MaxPriorityFeePerGas: cloneBig(operation.MaxPriorityFeePerGas),
		PaymasterAndData:     bytes.Clone(operation.PaymasterAndData), Signature: bytes.Clone(operation.Signature),
	}
	if err := normalizeInitCode(Version06, &request); err != nil {
		return Request{}, err
	}
	if err := normalizePaymaster(Version06, &request); err != nil {
		return Request{}, err
	}
	return request, validateRequest(request)
}

func normalizePacked(version Version, operation packedUserOperationWire) (Request, error) {
	request := Request{
		Sender: operation.Sender, Nonce: cloneBig(operation.Nonce),
		InitCode: bytes.Clone(operation.InitCode), CallData: bytes.Clone(operation.CallData),
		VerificationGasLimit: new(big.Int).SetBytes(operation.AccountGasLimits[:16]),
		CallGasLimit:         new(big.Int).SetBytes(operation.AccountGasLimits[16:]),
		PreVerificationGas:   cloneBig(operation.PreVerificationGas),
		MaxPriorityFeePerGas: new(big.Int).SetBytes(operation.GasFees[:16]),
		MaxFeePerGas:         new(big.Int).SetBytes(operation.GasFees[16:]),
		PaymasterAndData:     bytes.Clone(operation.PaymasterAndData), Signature: bytes.Clone(operation.Signature),
		AccountGasLimits: bytes.Clone(operation.AccountGasLimits[:]), GasFees: bytes.Clone(operation.GasFees[:]),
	}
	if err := normalizeInitCode(version, &request); err != nil {
		return Request{}, err
	}
	if err := normalizePaymaster(version, &request); err != nil {
		return Request{}, err
	}
	return request, validateRequest(request)
}

func normalizeInitCode(version Version, request *Request) error {
	if len(request.InitCode) == 0 {
		request.InitKind = InitNone
		return nil
	}
	if (version == Version08 || version == Version09) && eip7702InitCode(request.InitCode) {
		request.InitKind = InitEIP7702
		if len(request.InitCode) > common.AddressLength {
			request.FactoryData = bytes.Clone(request.InitCode[common.AddressLength:])
		}
		return nil
	}
	if len(request.InitCode) < common.AddressLength {
		return errors.New("EntryPoint initCode is shorter than a factory address")
	}
	request.InitKind = InitFactory
	factory := common.BytesToAddress(request.InitCode[:common.AddressLength])
	request.Factory = &factory
	request.FactoryData = bytes.Clone(request.InitCode[common.AddressLength:])
	return nil
}

func eip7702InitCode(value []byte) bool {
	if len(value) < 2 || value[0] != 0x77 || value[1] != 0x02 {
		return false
	}
	for index := 2; index < min(len(value), common.AddressLength); index++ {
		if value[index] != 0 {
			return false
		}
	}
	return true
}

var paymasterSignatureMagic = []byte{0x22, 0xe3, 0x25, 0xa2, 0x97, 0x43, 0x96, 0x56}

func normalizePaymaster(version Version, request *Request) error {
	data := request.PaymasterAndData
	if len(data) == 0 {
		return nil
	}
	minimum := common.AddressLength
	if version != Version06 {
		minimum = 52
	}
	if len(data) < minimum {
		return errors.New("EntryPoint paymasterAndData is shorter than its fixed fields")
	}
	paymaster := common.BytesToAddress(data[:common.AddressLength])
	if paymaster != (common.Address{}) {
		request.Paymaster = &paymaster
	}
	if version == Version06 {
		request.PaymasterData = bytes.Clone(data[common.AddressLength:])
		return nil
	}
	request.PaymasterVerificationGasLimit = new(big.Int).SetBytes(data[20:36])
	request.PaymasterPostOpGasLimit = new(big.Int).SetBytes(data[36:52])
	dataEnd := len(data)
	if version == Version09 && len(data) >= 62 && bytes.Equal(data[len(data)-8:], paymasterSignatureMagic) {
		signatureLength := int(data[len(data)-10])<<8 | int(data[len(data)-9])
		if signatureLength > len(data)-62 {
			return errors.New("EntryPoint v0.9 paymaster signature length is invalid")
		}
		if signatureLength > 0 {
			start := len(data) - 10 - signatureLength
			request.PaymasterSignature = bytes.Clone(data[start : len(data)-10])
			dataEnd = start
		}
	}
	request.PaymasterData = bytes.Clone(data[52:dataEnd])
	return nil
}

func validateRequest(request Request) error {
	for _, field := range []struct {
		name  string
		value *big.Int
	}{
		{name: "nonce", value: request.Nonce},
		{name: "call gas limit", value: request.CallGasLimit},
		{name: "verification gas limit", value: request.VerificationGasLimit},
		{name: "pre-verification gas", value: request.PreVerificationGas},
		{name: "maximum fee", value: request.MaxFeePerGas},
		{name: "maximum priority fee", value: request.MaxPriorityFeePerGas},
	} {
		if field.value == nil || field.value.Sign() < 0 || field.value.BitLen() > 256 {
			return fmt.Errorf("EntryPoint %s is outside uint256", field.name)
		}
	}
	return nil
}

func cloneBig(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}

func addressPointer(value common.Address) *common.Address {
	if value == (common.Address{}) {
		return nil
	}
	copy := value
	return &copy
}
