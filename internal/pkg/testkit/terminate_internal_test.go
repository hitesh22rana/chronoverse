package testkit

import "testing"

func TestTerminateCleanSuite(t *testing.T) {
	s := &suite{}
	s.terminate(t.Context())
	if s.containers != nil {
		t.Fatal("terminate retained containers")
	}
}
