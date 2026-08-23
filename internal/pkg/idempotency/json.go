package idempotency

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DecodeUniqueJSON decodes one JSON value while rejecting duplicate object
// keys at every nesting level. Numbers retain their original lexical token.
func DecodeUniqueJSON(reader io.Reader, destination any) error {
	value, err := parseUniqueJSON(reader)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(canonical, destination)
}

func parseUniqueJSON(reader io.Reader) (any, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	value, err := decodeUniqueValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return value, nil
}

// CanonicalJSON returns JSON with recursively sorted object keys, preserved
// array order, and lexically preserved numeric tokens.
func CanonicalJSON(raw string) ([]byte, error) {
	value, err := parseUniqueJSON(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// CanonicalJSONObject returns canonical JSON while requiring the top-level
// value to be an object. Nested arrays and scalar values remain valid.
func CanonicalJSONObject(raw string) ([]byte, error) {
	value, err := parseUniqueJSON(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("top-level JSON value must be an object")
	}
	return json.Marshal(value)
}

func decodeUniqueValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}

	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return nil, keyErr
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key must be a string")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			value, valueErr := decodeUniqueValue(decoder)
			if valueErr != nil {
				return nil, valueErr
			}
			object[key] = value
		}
		if _, err = decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, valueErr := decodeUniqueValue(decoder)
			if valueErr != nil {
				return nil, valueErr
			}
			array = append(array, value)
		}
		if _, err = decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}
