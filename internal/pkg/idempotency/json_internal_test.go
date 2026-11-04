package idempotency

import "testing"

func TestDecodeParsedJSONRejectsUnsupportedValues(t *testing.T) {
	var destination any
	if err := decodeParsedJSON(make(chan int), &destination); err == nil {
		t.Fatal("expected unsupported value to be rejected")
	}
}

func TestDecodeUniqueCompositeRejectsUnexpectedDelimiter(t *testing.T) {
	if _, err := decodeUniqueComposite(nil, '}', 0); err == nil {
		t.Fatal("expected unexpected delimiter to be rejected")
	}
}
