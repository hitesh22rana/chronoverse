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
	canonical, err := canonicalJSON(reader)
	if err != nil {
		return err
	}
	return json.Unmarshal(canonical, destination)
}

// maxJSONNestingDepth bounds recursion in decodeUniqueValue so a small
// deeply-nested body cannot exhaust gateway memory. 100 is far beyond any
// legitimate request or workflow payload shape.
const maxJSONNestingDepth = 100

func canonicalJSON(reader io.Reader) ([]byte, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	value, err := decodeUniqueValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return json.Marshal(value)
}

// CanonicalJSON returns JSON with recursively sorted object keys, preserved
// array order, and lexically preserved numeric tokens.
func CanonicalJSON(raw string) ([]byte, error) {
	return canonicalJSON(strings.NewReader(raw))
}

// CanonicalJSONObject returns canonical JSON while requiring the top-level
// value to be an object. Nested arrays and scalar values remain valid.
func CanonicalJSONObject(raw string) ([]byte, error) {
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		return nil, err
	}
	if canonical[0] != '{' {
		return nil, errors.New("top-level JSON value must be an object")
	}
	return canonical, nil
}

func decodeUniqueValue(decoder *json.Decoder, depth int) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	if depth >= maxJSONNestingDepth {
		return nil, fmt.Errorf("JSON nesting exceeds maximum depth of %d", maxJSONNestingDepth)
	}

	// At a value position, Decoder.Token only returns opening delimiters.
	if delimiter == '{' {
		object := make(map[string]any)
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return nil, keyErr
			}
			// encoding/json only returns string keys inside objects.
			key := keyToken.(string) //nolint:errcheck,forcetypeassert // Object keys are always strings.
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			value, valueErr := decodeUniqueValue(decoder, depth+1)
			if valueErr != nil {
				return nil, valueErr
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	}

	array := make([]any, 0)
	for decoder.More() {
		value, valueErr := decodeUniqueValue(decoder, depth+1)
		if valueErr != nil {
			return nil, valueErr
		}
		array = append(array, value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return array, nil
}
