package verify

import (
	"bytes"
	"encoding/json"
	"errors"
)

// compactSubmission stores sources once. Variants differ only in compiler settings;
// their order remains the order selected by multipart preparation.
type compactSubmission struct {
	SubmissionV2
	VariantSettings []json.RawMessage `json:"variant_settings,omitempty"`
}

func encodeSubmission(request SubmissionV2) ([]byte, []byte, error) {
	compact := compactSubmission{SubmissionV2: request}
	compact.StandardJSONVariants = nil
	var base map[string]json.RawMessage
	if len(request.StandardJSONVariants) > 0 {
		if err := json.Unmarshal(request.StandardJSON, &base); err != nil {
			return nil, nil, err
		}
		for _, variant := range request.StandardJSONVariants {
			var document map[string]json.RawMessage
			if err := json.Unmarshal(variant, &document); err != nil {
				return nil, nil, err
			}
			settings := document["settings"]
			if len(settings) == 0 {
				settings = json.RawMessage(`{}`)
			}
			delete(document, "settings")
			withoutSettings := make(map[string]json.RawMessage, len(base))
			for key, value := range base {
				if key != "settings" {
					withoutSettings[key] = value
				}
			}
			a, _ := json.Marshal(document)
			b, _ := json.Marshal(withoutSettings)
			if !bytes.Equal(a, b) {
				return nil, nil, errors.New("compiler variants must share sources")
			}
			compact.VariantSettings = append(compact.VariantSettings, settings)
		}
	}
	payload, err := json.Marshal(compact)
	if err != nil {
		return nil, nil, err
	}
	var metadata map[string]json.RawMessage
	if err = json.Unmarshal(payload, &metadata); err != nil {
		return nil, nil, err
	}
	for _, key := range []string{"standard_json", "variant_settings", "bytecodes", "sourcify_request"} {
		delete(metadata, key)
	}
	if request.Geas != nil {
		var geas map[string]json.RawMessage
		if err = json.Unmarshal(metadata["geas"], &geas); err != nil {
			return nil, nil, err
		}
		delete(geas, "sources")
		metadata["geas"], err = json.Marshal(geas)
		if err != nil {
			return nil, nil, err
		}
	}
	if request.Target != nil {
		var target map[string]json.RawMessage
		if err = json.Unmarshal(metadata["target"], &target); err != nil {
			return nil, nil, err
		}
		delete(target, "CreationBytecode")
		delete(target, "RuntimeBytecode")
		metadata["target"], err = json.Marshal(target)
		if err != nil {
			return nil, nil, err
		}
	}
	summary, err := json.Marshal(metadata)
	return payload, summary, err
}

func decodeSubmission(payload []byte) (*SubmissionV2, error) {
	var compact compactSubmission
	if err := json.Unmarshal(payload, &compact); err != nil {
		return nil, err
	}
	if len(compact.VariantSettings) > 0 {
		var base map[string]json.RawMessage
		if err := json.Unmarshal(compact.StandardJSON, &base); err != nil {
			return nil, err
		}
		baseSettings := base["settings"]
		for _, settings := range compact.VariantSettings {
			if bytes.Equal(settings, baseSettings) {
				compact.StandardJSONVariants = append(compact.StandardJSONVariants, append(json.RawMessage(nil), compact.StandardJSON...))
				continue
			}
			base["settings"] = settings
			variant, err := json.Marshal(base)
			if err != nil {
				return nil, err
			}
			compact.StandardJSONVariants = append(compact.StandardJSONVariants, variant)
		}
	}
	return &compact.SubmissionV2, nil
}
