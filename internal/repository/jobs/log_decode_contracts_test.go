//nolint:testpackage // Exercises the search index and cursor decoding contracts.
package jobs

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDecodeSearchJobLogContracts(t *testing.T) {
	for _, tc := range []struct {
		name, document                 string
		code                           codes.Code
		event, message, stream, cursor string
		sequence                       uint32
		timestamp                      time.Time
	}{
		{
			name: "numeric sequence and highlighted message",
			document: `{"id":"document-17","event_id":"event-17","sequence_num":17,"message":"raw","stream":"stdout",
 "timestamp":"2026-01-02T03:04:05.123Z","_formatted":{"message":"<em>match</em>"}}`,
			event: "event-17", message: "<em>match</em>", stream: "stdout", cursor: "document-17", sequence: 17,
			timestamp: time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.UTC),
		},
		{
			name:     "string sequence and explicit event identity",
			document: `{"id":"document","event_id":"event","sequence_num":"4294967295","message":"plain","stream":"stderr"}`,
			event:    "event", message: "plain", stream: "stderr", cursor: "document", sequence: 4294967295,
		},
		{name: "missing optional fields", document: `{}`},
		{name: "wrong optional field types", document: `{"id":17,"event_id":false,"sequence_num":null,"message":12,"stream":false,"timestamp":1,"_formatted":{"message":false}}`},
		{name: "invalid timestamp", document: `{"timestamp":"yesterday"}`, code: codes.Internal},
		{name: "negative string sequence", document: `{"sequence_num":"-1"}`, code: codes.Internal},
		{name: "overflowing string sequence", document: `{"sequence_num":"4294967296"}`, code: codes.Internal},
		{name: "invalid formatted document", document: `{"_formatted":"not an object"}`, code: codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hit map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.document), &hit); err != nil {
				t.Fatal(err)
			}
			log, cursor, err := decodeSearchJobLog(hit)
			if status.Code(err) != tc.code {
				t.Fatalf("error=%v, want %v", err, tc.code)
			}
			if tc.code != codes.OK {
				if log != nil || cursor != (jobLogsCursor{}) {
					t.Fatalf("failed decode returned %+v %+v", log, cursor)
				}
				return
			}
			if log.EventID != tc.event || log.Message != tc.message || log.Stream != tc.stream || log.SequenceNum != tc.sequence || !log.Timestamp.Equal(tc.timestamp) {
				t.Fatalf("log=%+v", log)
			}
			if cursor.EventID != tc.cursor || cursor.Stream != tc.stream || cursor.SequenceNum != tc.sequence {
				t.Fatalf("cursor=%+v", cursor)
			}
		})
	}
}

func TestSearchHitSourceRejectsMalformedJSON(t *testing.T) {
	for _, field := range []string{"message", "_formatted"} {
		t.Run(field, func(t *testing.T) {
			hit := map[string]json.RawMessage{field: json.RawMessage(`{"unfinished":`)}
			log, cursor, decodeErr := decodeSearchJobLog(hit)
			if log != nil || cursor != (jobLogsCursor{}) || status.Code(decodeErr) != codes.Internal {
				t.Fatalf("log=%v cursor=%+v error=%v", log, cursor, decodeErr)
			}
			source, err := searchHitSource(hit)
			if source != nil || status.Code(err) != codes.Internal {
				t.Fatalf("source=%v error=%v", source, err)
			}
		})
	}
	if got := searchHitString(nil, "message"); got != "" {
		t.Fatalf("missing source message=%q", got)
	}
}

func TestJobLogsCursorRejectsMalformedPayloads(t *testing.T) {
	for _, payload := range []string{`{`, `null`, `{}`, `{"stream":"stdout"}`, `{"event_id":"event"}`, `{"stream":"stdout","event_id":"event","sequence_num":-1}`} {
		t.Run(payload, func(t *testing.T) {
			cursor, err := extractDataFromGetJobLogsCursor(base64.StdEncoding.EncodeToString([]byte(payload)))
			if cursor != (jobLogsCursor{}) || status.Code(err) != codes.InvalidArgument {
				t.Fatalf("cursor=%+v error=%v", cursor, err)
			}
		})
	}
}
