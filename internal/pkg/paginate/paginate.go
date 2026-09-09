// Package paginate trims limit+1 over-fetched pages.
package paginate

// Trim returns data limited to limit rows and whether more rows existed.
// Callers fetch limit+1 rows; the extra row (if any) only signals hasMore.
func Trim[T any](data []T, limit int) ([]T, bool) {
	if len(data) > limit {
		return data[:limit], true
	}
	return data, false
}

// TrimWithCursor trims the over-fetched page and builds the next cursor from
// the first unreturned row. It returns an empty cursor when there is no next page.
func TrimWithCursor[T any](data []T, limit int, cursorOf func(T) string) (page []T, cursor string) {
	if len(data) > limit {
		return data[:limit], cursorOf(data[limit])
	}
	return data, ""
}
