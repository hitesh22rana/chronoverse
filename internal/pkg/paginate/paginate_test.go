package paginate_test

import (
	"fmt"
	"testing"

	"github.com/hitesh22rana/chronoverse/internal/pkg/paginate"
)

func TestTrim(t *testing.T) {
	if page, more := paginate.Trim([]int{}, 2); len(page) != 0 || more {
		t.Fatalf("empty = %v, %v", page, more)
	}
	if page, more := paginate.Trim([]int{1, 2}, 2); len(page) != 2 || more {
		t.Fatalf("exact limit = %v, %v", page, more)
	}
	page, more := paginate.Trim([]int{1, 2, 3}, 2)
	if len(page) != 2 || !more || page[1] != 2 {
		t.Fatalf("over limit = %v, %v", page, more)
	}
}

func TestTrimWithCursor(t *testing.T) {
	cursorOf := func(v int) string { return fmt.Sprintf("cursor-%d", v) }

	page, cursor := paginate.TrimWithCursor([]int{1, 2}, 2, cursorOf)
	if len(page) != 2 || cursor != "" {
		t.Fatalf("exact limit = %v, %q", page, cursor)
	}
	page, cursor = paginate.TrimWithCursor([]int{1, 2, 3}, 2, cursorOf)
	if len(page) != 2 || cursor != "cursor-3" {
		t.Fatalf("over limit = %v, %q", page, cursor)
	}
}
