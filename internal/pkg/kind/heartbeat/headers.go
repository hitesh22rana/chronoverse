package heartbeat

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// heartbeatMaxHeaders caps custom header names per check.
	heartbeatMaxHeaders = 20
	// heartbeatMaxHeaderBytes caps combined custom header name/value size.
	heartbeatMaxHeaderBytes = 8 * 1024
)

// deniedHeartbeatHeaders are hop-by-hop headers workflows must not override (case-insensitive; proxy-* by prefix).
var deniedHeartbeatHeaders = map[string]struct{}{
	"host":              {},
	"content-length":    {},
	"transfer-encoding": {},
	"connection":        {},
	"upgrade":           {},
	"keep-alive":        {},
	"trailer":           {},
	"te":                {},
}

// isDeniedHeartbeatHeader reports whether a header name is denied.
func isDeniedHeartbeatHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if _, denied := deniedHeartbeatHeaders[lower]; denied {
		return true
	}
	return strings.HasPrefix(lower, "proxy-")
}

// isValidHeartbeatHeaderName reports whether name is an RFC 7230 token.
func isValidHeartbeatHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < '!' || c > '~' || strings.IndexByte("()<>@,;:\\\"/[]?={} \t", c) >= 0 {
			return false
		}
	}
	return true
}

// parseHeartbeatHeaders validates custom headers: denies hop-by-hop names, caps count and size.
func parseHeartbeatHeaders(raw any) (map[string][]string, error) {
	headersRaw, ok := raw.(map[string]any)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "invalid headers format")
	}

	headers := make(map[string][]string)
	headerBytes := 0
	for k, v := range headersRaw {
		name := strings.TrimSpace(k)
		if !isValidHeartbeatHeaderName(name) {
			return nil, status.Errorf(codes.InvalidArgument, "header name %q is invalid", k)
		}
		if isDeniedHeartbeatHeader(name) {
			return nil, status.Errorf(codes.InvalidArgument, "header %q is not allowed", name)
		}
		if len(headers) >= heartbeatMaxHeaders {
			return nil, status.Errorf(codes.InvalidArgument, "too many headers: at most %d allowed", heartbeatMaxHeaders)
		}
		// Count the name per emitted value (once when there are no values),
		// matching how Execute adds one header line per value.
		switch val := v.(type) {
		case []any:
			if len(val) == 0 {
				headerBytes += len(name)
			}
			strValues := make([]string, len(val))
			for i, iv := range val {
				strValue, ok := iv.(string)
				if !ok {
					return nil, status.Errorf(codes.InvalidArgument, "header value must be string")
				}
				strValues[i] = strValue
				headerBytes += len(name) + len(strValue)
			}
			headers[name] = strValues
		case string:
			headers[name] = []string{val}
			headerBytes += len(name) + len(val)
		default:
			return nil, status.Errorf(codes.InvalidArgument, "invalid header value for %s", name)
		}
		if headerBytes > heartbeatMaxHeaderBytes {
			return nil, status.Errorf(codes.InvalidArgument, "headers exceed maximum size of %d bytes", heartbeatMaxHeaderBytes)
		}
	}
	return headers, nil
}
