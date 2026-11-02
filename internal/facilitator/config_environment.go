package facilitator

import "bytes"

// resolveCredential is used only for the two process-scoped credentials. An
// explicitly selected but unreadable/oversized source never falls back to YAML.
func resolveCredential(name, fallback string, limit int64, field string, lookup func(string) (string, bool)) ([]byte, error) {
	value, valueSet := lookup(name)
	path, fileSet := lookup(name + "_FILE")
	if valueSet && fileSet {
		return nil, configError("conflicting_sources", field)
	}
	var data []byte
	if fileSet {
		var err error
		data, err = readBoundedConfigFile(path, limit, field, true)
		if err != nil {
			return nil, err
		}
	} else if valueSet {
		if int64(len(value)) > limit {
			return nil, configError("invalid_secret", field)
		}
		data = []byte(value)
	}
	if len(bytes.TrimSpace(data)) > 0 {
		return data, nil
	}
	clear(data)
	if fallback == "" {
		return nil, configError("missing_secret", field)
	}
	return readConfigFile(fallback, limit, field)
}
