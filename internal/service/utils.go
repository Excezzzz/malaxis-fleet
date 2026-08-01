package service

import (
	"encoding/json"
)

// CanonicalJSON marshals an interface to a canonical JSON string.
// It achieves this by unmarshaling into a map[string]interface{} and then marshaling it again,
// which sorts the keys alphabetically.
func CanonicalJSON(data interface{}) (string, error) {
	// Marshal the data first
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	// Unmarshal into a map to sort keys
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		// If it's not an object, just return the original marshaled string
		return string(b), nil
	}

	// Marshal again to get canonical JSON
	canonical, err := json.Marshal(m)
	if err != nil {
		return "", err
	}

	return string(canonical), nil
}
