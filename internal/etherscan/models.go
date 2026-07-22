package etherscan

// Etherscan represents account, token, block, and statistics integers as
// decimal strings, while its log endpoint uses lowercase hexadecimal strings.
// Dedicated wire models avoid accidental float64 conversion through
// map[string]any.
type accountTransaction struct {
	BlockNumber       string `json:"blockNumber"`
	TimeStamp         string `json:"timeStamp"`
	Hash              string `json:"hash"`
	Nonce             string `json:"nonce"`
	BlockHash         string `json:"blockHash"`
	TransactionIndex  string `json:"transactionIndex"`
	From              string `json:"from"`
	To                string `json:"to"`
	Value             string `json:"value"`
	Gas               string `json:"gas"`
	GasPrice          string `json:"gasPrice"`
	IsError           string `json:"isError"`
	ReceiptStatus     string `json:"txreceipt_status"`
	Input             string `json:"input"`
	ContractAddress   string `json:"contractAddress"`
	CumulativeGasUsed string `json:"cumulativeGasUsed"`
	GasUsed           string `json:"gasUsed"`
	Confirmations     string `json:"confirmations"`
	MethodID          string `json:"methodId"`
	FunctionName      string `json:"functionName"`
}

type minedBlock struct {
	BlockNumber string `json:"blockNumber"`
	TimeStamp   string `json:"timeStamp"`
	// The core schema does not contain consensus issuance or a complete
	// execution reward. Omitting this optional compatibility field is more
	// accurate than reporting a fabricated zero.
	BlockReward string `json:"blockReward,omitempty"`
}

type transactionErrorStatus struct {
	IsError        string `json:"isError"`
	ErrDescription string `json:"errDescription"`
}

type transactionReceiptStatus struct {
	Status string `json:"status"`
}

type logEntry struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	BlockNumber      string   `json:"blockNumber"`
	BlockHash        string   `json:"blockHash"`
	TimeStamp        string   `json:"timeStamp"`
	GasPrice         string   `json:"gasPrice"`
	GasUsed          string   `json:"gasUsed"`
	LogIndex         string   `json:"logIndex"`
	TransactionHash  string   `json:"transactionHash"`
	TransactionIndex string   `json:"transactionIndex"`
}

type blockCountdown struct {
	CurrentBlock      string `json:"CurrentBlock"`
	CountdownBlock    string `json:"CountdownBlock"`
	RemainingBlock    string `json:"RemainingBlock"`
	EstimateTimeInSec string `json:"EstimateTimeInSec"`
}

type sourceCodeResult struct {
	SourceCode           string `json:"SourceCode"`
	ABI                  string `json:"ABI"`
	ContractName         string `json:"ContractName"`
	CompilerVersion      string `json:"CompilerVersion"`
	CompilerType         string `json:"CompilerType"`
	OptimizationUsed     string `json:"OptimizationUsed"`
	Runs                 string `json:"Runs"`
	ConstructorArguments string `json:"ConstructorArguments"`
	EVMVersion           string `json:"EVMVersion"`
	Library              string `json:"Library"`
	ContractFileName     string `json:"ContractFileName"`
	LicenseType          string `json:"LicenseType"`
	Proxy                string `json:"Proxy"`
	Implementation       string `json:"Implementation"`
	SwarmSource          string `json:"SwarmSource"`
	SimilarMatch         string `json:"SimilarMatch"`
	MatchKind            string `json:"MatchKind"`
}

type contractCreationResult struct {
	ContractAddress  string `json:"contractAddress"`
	ContractCreator  string `json:"contractCreator"`
	TxHash           string `json:"txHash"`
	BlockNumber      string `json:"blockNumber"`
	Timestamp        string `json:"timestamp"`
	ContractFactory  string `json:"contractFactory"`
	CreationBytecode string `json:"creationBytecode"`
}

type tokenTransfer struct {
	BlockNumber       string `json:"blockNumber"`
	TimeStamp         string `json:"timeStamp"`
	Hash              string `json:"hash"`
	Nonce             string `json:"nonce"`
	BlockHash         string `json:"blockHash"`
	From              string `json:"from"`
	ContractAddress   string `json:"contractAddress"`
	To                string `json:"to"`
	Value             string `json:"value,omitempty"`
	TokenID           string `json:"tokenID,omitempty"`
	TokenValue        string `json:"tokenValue,omitempty"`
	TokenName         string `json:"tokenName"`
	TokenSymbol       string `json:"tokenSymbol"`
	TokenDecimal      string `json:"tokenDecimal,omitempty"`
	TransactionIndex  string `json:"transactionIndex"`
	Gas               string `json:"gas"`
	GasPrice          string `json:"gasPrice"`
	GasUsed           string `json:"gasUsed"`
	CumulativeGasUsed string `json:"cumulativeGasUsed"`
	Input             string `json:"input"`
	MethodID          string `json:"methodId"`
	FunctionName      string `json:"functionName"`
	Confirmations     string `json:"confirmations"`
}

type internalTransaction struct {
	BlockNumber     string `json:"blockNumber"`
	TimeStamp       string `json:"timeStamp"`
	Hash            string `json:"hash"`
	From            string `json:"from"`
	To              string `json:"to"`
	Value           string `json:"value"`
	ContractAddress string `json:"contractAddress"`
	Input           string `json:"input"`
	Type            string `json:"type"`
	Gas             string `json:"gas"`
	GasUsed         string `json:"gasUsed"`
	TraceID         string `json:"traceId"`
	IsError         string `json:"isError"`
	ErrCode         string `json:"errCode"`
}

type tokenInfo struct {
	ContractAddress string `json:"contractAddress"`
	TokenName       string `json:"tokenName"`
	Symbol          string `json:"symbol"`
	Divisor         string `json:"divisor,omitempty"`
	TokenType       string `json:"tokenType"`
	TotalSupply     string `json:"totalSupply,omitempty"`
}

type tokenHolder struct {
	Address  string `json:"TokenHolderAddress"`
	Quantity string `json:"TokenHolderQuantity"`
}
