package etherscan

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func rejectRepeatedParameters(values url.Values) error {
	for name, items := range values {
		if name == "" || len(items) != 1 {
			return errors.New("parameters must appear exactly once")
		}
	}
	return nil
}

func canonicalBillableValues(
	method, module, action string,
	values url.Values,
	spec actionSpec,
) (url.Values, error) {
	if method != http.MethodGet && method != http.MethodPost {
		return nil, errors.New("unsupported billing method")
	}
	canonical := cloneValues(values)
	allowed := billableParameterNames(module, action, spec)
	for name := range canonical {
		if !allowed[name] {
			return nil, fmt.Errorf("unsupported parameter %s", name)
		}
	}
	chainID, err := parseCanonicalDecimal(strings.TrimSpace(canonical.Get("chainid")))
	if err != nil || chainID.Sign() <= 0 {
		return nil, errors.New("chainid must be canonical")
	}
	canonical.Set("chainid", chainID.String())

	for _, name := range append(append([]string(nil), spec.addresses...), spec.optionalAddresses...) {
		if raw := strings.TrimSpace(canonical.Get(name)); raw != "" {
			address, _, parseErr := parseAddressParameter(raw, name)
			if parseErr != nil {
				return nil, parseErr
			}
			canonical.Set(name, strings.ToLower(address.Hex()))
		}
	}
	for _, name := range spec.hashes {
		if err := canonicalizeHash(canonical, name); err != nil {
			return nil, err
		}
	}
	if spec.hashOptional != "" && canonical.Get(spec.hashOptional) != "" {
		if err := canonicalizeHash(canonical, spec.hashOptional); err != nil {
			return nil, err
		}
	}
	if spec.state {
		if err := latestTag(canonical); err != nil {
			return nil, err
		}
		canonical.Set("tag", "latest")
	}
	if spec.list || spec.pageOnly {
		pagination, parseErr := parsePagination(canonical)
		if parseErr != nil {
			return nil, parseErr
		}
		page := strings.TrimSpace(canonical.Get("page"))
		if page == "" {
			page = "1"
		}
		canonical.Set("page", page)
		canonical.Set("offset", fmt.Sprintf("%d", pagination.limit))
		if spec.list {
			canonical.Set("sort", strings.ToLower(pagination.direction))
		} else {
			canonical.Del("sort")
		}
	}

	switch module + "." + action {
	case "account.balancemulti":
		if err := canonicalizeAddressList(canonical, "address", 20, false); err != nil {
			return nil, err
		}
	case "account.txlist":
		if _, err := normalTransactionSelector(canonical); err != nil {
			return nil, err
		}
		if err := canonicalizeDecimalRange(canonical, "startblock", "endblock", true); err != nil {
			return nil, err
		}
		canonical.Set("fromto_opr", strings.ToLower(canonical.Get("fromto_opr")))
		if canonical.Get("fromto_opr") == "" {
			canonical.Del("fromto_opr")
		}
	case "account.tokentx", "account.tokennfttx", "account.token1155tx":
		if _, err := tokenTransferSelector(canonical); err != nil {
			return nil, err
		}
		if err := canonicalizeDecimalRange(canonical, "startblock", "endblock", true); err != nil {
			return nil, err
		}
		canonical.Set("fromto_opr", strings.ToLower(canonical.Get("fromto_opr")))
		if canonical.Get("fromto_opr") == "" {
			canonical.Del("fromto_opr")
		}
	case "account.txlistinternal":
		if err := canonicalizeInternalTransactionParameters(canonical); err != nil {
			return nil, err
		}
	case "account.getminedblocks":
		blockType := strings.ToLower(strings.TrimSpace(canonical.Get("blocktype")))
		if blockType == "" {
			blockType = "blocks"
		}
		if blockType != "blocks" && blockType != "uncles" {
			return nil, errors.New("blocktype must be blocks or uncles")
		}
		canonical.Set("blocktype", blockType)
	case "contract.getcontractcreation":
		if err := canonicalizeAddressList(canonical, "contractaddresses", 5, true); err != nil {
			return nil, err
		}
	case "logs.getLogs":
		if err := canonicalizeLogs(canonical); err != nil {
			return nil, err
		}
	case "block.getblocknobytime":
		if err := canonicalizeDecimal(canonical, "timestamp"); err != nil {
			return nil, err
		}
		closest := strings.ToLower(strings.TrimSpace(canonical.Get("closest")))
		if closest != "before" && closest != "after" {
			return nil, errors.New("closest must be before or after")
		}
		canonical.Set("closest", closest)
	case "block.getblockcountdown":
		if err := canonicalizeDecimal(canonical, "blockno"); err != nil {
			return nil, err
		}
	case "block.getblocktxnscount":
		if err := canonicalizeDecimal(canonical, "blockno"); err != nil {
			return nil, err
		}
	case "account.txsBeaconWithdrawal":
		if err := canonicalizeDecimalRange(canonical, "startblock", "endblock", false); err != nil {
			return nil, err
		}
	case "account.addresstokenbalance", "account.addresstokennftbalance", "account.addresstokennftinventory":
		if holdingWindowExceeded(canonical) {
			return nil, ErrHoldingWindowUnavailable
		}
	}
	return canonical, nil
}

func billableParameterNames(module, action string, spec actionSpec) map[string]bool {
	result := actionParameterNames(spec)
	switch module + "." + action {
	case "account.txlist", "account.txlistinternal", "account.tokentx",
		"account.tokennfttx", "account.token1155tx":
		result["startblock"], result["endblock"] = true, true
	case "account.getminedblocks":
		result["blocktype"] = true
	case "logs.getLogs":
		result["fromBlock"], result["toBlock"] = true, true
		for index := range 4 {
			result[fmt.Sprintf("topic%d", index)] = true
		}
		for left := range 4 {
			for right := left + 1; right < 4; right++ {
				result[fmt.Sprintf("topic%d_%d_opr", left, right)] = true
			}
		}
	}
	return result
}

func canonicalizeHash(values url.Values, name string) error {
	hash, _, err := parseHashParameter(strings.TrimSpace(values.Get(name)), name)
	if err != nil {
		return err
	}
	values.Set(name, strings.ToLower(hash.Hex()))
	return nil
}

func canonicalizeDecimal(values url.Values, name string) error {
	value, err := parseDecimal(values.Get(name), name)
	if err != nil {
		return err
	}
	values.Set(name, value.String())
	return nil
}

func canonicalizeDecimalRange(values url.Values, startName, endName string, allowLatest bool) error {
	start := "0"
	if raw := strings.TrimSpace(values.Get(startName)); raw != "" {
		value, err := parseDecimal(raw, startName)
		if err != nil {
			return err
		}
		start = value.String()
	}
	values.Set(startName, start)
	if raw := strings.TrimSpace(values.Get(endName)); raw != "" {
		if allowLatest && raw == "latest" {
			values.Del(endName)
			return nil
		}
		value, err := parseDecimal(raw, endName)
		if err != nil {
			return err
		}
		if value.Cmp(mustBig(start)) < 0 {
			return fmt.Errorf("%s is less than %s", endName, startName)
		}
		values.Set(endName, value.String())
	} else {
		values.Del(endName)
	}
	return nil
}

func canonicalizeAddressList(values url.Values, name string, maximum int, rejectDuplicates bool) error {
	raw := strings.Split(values.Get(name), ",")
	if len(raw) == 0 || len(raw) > maximum {
		return fmt.Errorf("%s contains an invalid number of addresses", name)
	}
	seen := make(map[string]bool, len(raw))
	result := make([]string, len(raw))
	for index, item := range raw {
		address, _, err := parseAddressParameter(strings.TrimSpace(item), name)
		if err != nil {
			return err
		}
		result[index] = strings.ToLower(address.Hex())
		if rejectDuplicates && seen[result[index]] {
			return fmt.Errorf("%s contains a duplicate address", name)
		}
		seen[result[index]] = true
	}
	values.Set(name, strings.Join(result, ","))
	return nil
}

func canonicalizeInternalTransactionParameters(values url.Values) error {
	selector, err := internalTransactionSelector(values)
	if err != nil {
		return err
	}
	address := strings.TrimSpace(values.Get("address"))
	hash := strings.TrimSpace(values.Get("txhash"))
	if address != "" {
		parsed, _, err := parseAddressParameter(address, "address")
		if err != nil {
			return err
		}
		values.Set("address", strings.ToLower(parsed.Hex()))
	}
	if hash != "" {
		return canonicalizeHash(values, "txhash")
	}
	if selector.mode == selectorDirectional {
		values.Set("fromto_opr", selector.op)
	} else {
		values.Del("fromto_opr")
	}
	if err := canonicalizeDecimalRange(values, "startblock", "endblock", true); err != nil {
		return err
	}
	return nil
}

func canonicalizeLogs(values url.Values) error {
	if err := canonicalizeDecimalRange(values, "fromBlock", "toBlock", false); err != nil {
		return err
	}
	filters, err := buildTopicFilter(values)
	if err != nil {
		return err
	}
	for name := range values {
		if strings.HasPrefix(name, "topic") {
			values.Del(name)
		}
	}
	for index, filter := range filters {
		values.Set(fmt.Sprintf("topic%d", filter.Index), filter.Value)
		if index > 0 {
			previous := filters[index-1]
			values.Set(
				fmt.Sprintf("topic%d_%d_opr", previous.Index, filter.Index),
				strings.ToLower(filter.Operator),
			)
		}
	}
	return nil
}
