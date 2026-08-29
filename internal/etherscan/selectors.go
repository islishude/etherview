package etherscan

import (
	"errors"
	"net/url"
	"strings"
)

type selectorMode uint8

const (
	selectorLegacyAddress selectorMode = iota + 1
	selectorTransactionHash
	selectorRange
	selectorDirectional
	selectorContract
)

type directionalSelector struct {
	mode selectorMode
	from string
	to   string
	op   string
}

func actionParameterNames(spec actionSpec) map[string]bool {
	allowed := map[string]bool{"chainid": true, "module": true, "action": true}
	for _, names := range [][]string{
		spec.required, spec.addresses, spec.optionalAddresses, spec.hashes, spec.extra,
	} {
		for _, name := range names {
			allowed[name] = true
		}
	}
	if spec.hashOptional != "" {
		allowed[spec.hashOptional] = true
	}
	if spec.state {
		allowed["tag"] = true
	}
	if spec.list || spec.pageOnly {
		allowed["page"], allowed["offset"] = true, true
	}
	if spec.list {
		allowed["sort"] = true
	}
	return allowed
}

func validateNormalTransactionMode(values url.Values) error {
	_, err := normalTransactionSelector(values)
	return err
}

func normalTransactionSelector(values url.Values) (directionalSelector, error) {
	address := strings.TrimSpace(values.Get("address"))
	from := strings.TrimSpace(values.Get("from"))
	to := strings.TrimSpace(values.Get("to"))
	op := strings.ToLower(strings.TrimSpace(values.Get("fromto_opr")))
	if address != "" {
		if from != "" || to != "" || op != "" {
			return directionalSelector{}, errors.New("txlist address mode does not accept from/to filters")
		}
		return directionalSelector{mode: selectorLegacyAddress}, nil
	}
	if from == "" && to == "" {
		return directionalSelector{}, errors.New("txlist requires address, from, or to")
	}
	if err := validateFromToOperator(from, to, op); err != nil {
		return directionalSelector{}, err
	}
	return directionalSelector{mode: selectorDirectional, from: from, to: to, op: op}, nil
}

func validateTokenTransferMode(values url.Values) error {
	_, err := tokenTransferSelector(values)
	return err
}

func tokenTransferSelector(values url.Values) (directionalSelector, error) {
	address := strings.TrimSpace(values.Get("address"))
	contract := strings.TrimSpace(values.Get("contractaddress"))
	from := strings.TrimSpace(values.Get("from"))
	to := strings.TrimSpace(values.Get("to"))
	op := strings.ToLower(strings.TrimSpace(values.Get("fromto_opr")))
	if address != "" {
		if from != "" || to != "" || op != "" {
			return directionalSelector{}, errors.New("token transfer address mode does not accept from/to filters")
		}
		return directionalSelector{mode: selectorLegacyAddress}, nil
	}
	if contract == "" && from == "" && to == "" {
		return directionalSelector{}, errors.New("token transfer requires address, contractaddress, from, or to")
	}
	if from == "" && to == "" {
		if op != "" {
			return directionalSelector{}, errors.New("fromto_opr requires from or to")
		}
		return directionalSelector{mode: selectorContract}, nil
	}
	if err := validateFromToOperator(from, to, op); err != nil {
		return directionalSelector{}, err
	}
	return directionalSelector{mode: selectorDirectional, from: from, to: to, op: op}, nil
}

func validateInternalTransactionMode(values url.Values) error {
	_, err := internalTransactionSelector(values)
	return err
}

func internalTransactionSelector(values url.Values) (directionalSelector, error) {
	address := strings.TrimSpace(values.Get("address"))
	hash := strings.TrimSpace(values.Get("txhash"))
	from := strings.TrimSpace(values.Get("from"))
	to := strings.TrimSpace(values.Get("to"))
	op := strings.ToLower(strings.TrimSpace(values.Get("fromto_opr")))
	start := strings.TrimSpace(values.Get("startblock"))
	end := strings.TrimSpace(values.Get("endblock"))

	modes := 0
	if address != "" {
		modes++
	}
	if hash != "" {
		modes++
	}
	if from != "" || to != "" {
		modes++
	}
	rangeOnly := address == "" && hash == "" && from == "" && to == "" && start != ""
	if rangeOnly {
		modes++
	}
	if modes != 1 {
		return directionalSelector{}, errors.New("txlistinternal requires exactly one address, txhash, from/to, or block-range mode")
	}
	if address != "" {
		if op != "" {
			return directionalSelector{}, errors.New("txlistinternal address mode does not accept fromto_opr")
		}
		return directionalSelector{mode: selectorLegacyAddress}, nil
	}
	if hash != "" {
		if start != "" || end != "" || op != "" {
			return directionalSelector{}, errors.New("txhash mode does not accept a block range or fromto_opr")
		}
		return directionalSelector{mode: selectorTransactionHash}, nil
	}
	if from != "" || to != "" {
		if err := validateFromToOperator(from, to, op); err != nil {
			return directionalSelector{}, err
		}
		return directionalSelector{mode: selectorDirectional, from: from, to: to, op: op}, nil
	}
	if start == "" {
		return directionalSelector{}, errors.New("block-range mode requires startblock")
	}
	if op != "" {
		return directionalSelector{}, errors.New("block-range mode does not accept fromto_opr")
	}
	return directionalSelector{mode: selectorRange}, nil
}

func validateFromToOperator(from, to, operator string) error {
	if from == "" && to == "" {
		return errors.New("fromto_opr requires from or to")
	}
	if operator != "and" && operator != "or" {
		return errors.New("fromto_opr must be and or or")
	}
	return nil
}

func optionalAddressText(raw, name string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	address, _, err := parseAddressParameter(raw, name)
	if err != nil {
		return nil, err
	}
	return strings.ToLower(address.Hex()), nil
}

func optionalAddressBytes(raw, name string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	_, address, err := parseAddressParameter(raw, name)
	if err != nil {
		return nil, err
	}
	return address, nil
}
