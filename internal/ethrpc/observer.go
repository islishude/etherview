package ethrpc

// Observer receives only bounded architectural purpose/result labels. It
// never receives endpoint URLs, JSON-RPC methods, parameters, or errors.
type Observer interface {
	RecordRPC(purpose, result string)
}
