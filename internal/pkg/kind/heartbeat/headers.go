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
}

// isDeniedHeartbeatHeader reports whether a header name is denied.
func isDeniedHeartbeatHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if _, denied := deniedHeartbeatHeaders[lower]; denied {
		return true
	}
	return strings.HasPrefix(lower, "proxy-")
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
		if strings.TrimSpace(k) == "" {
			return nil, status.Error(codes.InvalidArgument, "header name must not be empty")
		}
		if isDeniedHeartbeatHeader(k) {
			return nil, status.Errorf(codes.InvalidArgument, "header %q is not allowed", k)
		}
		if len(headers) >= heartbeatMaxHeaders {
			return nil, status.Errorf(codes.InvalidArgument, "too many headers: at most %d allowed", heartbeatMaxHeaders)
		}
		switch val := v.(type) {
		case []any:
			strValues := make([]string, len(val))
			for i, iv := range val {
				strValue, ok := iv.(string)
				if !ok {
					return nil, status.Errorf(codes.InvalidArgument, "header value must be string")
				}
				strValues[i] = strValue
				headerBytes += len(k) + len(strValue)
			}
			headers[k] = strValues
		case string:
			headers[k] = []string{val}
			headerBytes += len(k) + len(val)
		default:
			return nil, status.Errorf(codes.InvalidArgument, "invalid header value for %s", k)
		}
		if headerBytes > heartbeatMaxHeaderBytes {
			return nil, status.Errorf(codes.InvalidArgument, "headers exceed maximum size of %d bytes", heartbeatMaxHeaderBytes)
		}
	}
	return headers, nil
}
