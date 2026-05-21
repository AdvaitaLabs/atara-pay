package cli

import "fmt"

// paginated appends ?limit=&offset= to a path when the caller set them.
// limit<=0 is treated as "server default"; offset<0 is clamped to 0.
func paginated(path string, limit, offset int) string {
	if limit <= 0 && offset <= 0 {
		return path
	}
	sep := "?"
	out := path
	if limit > 0 {
		out += sep + "limit=" + itoa(limit)
		sep = "&"
	}
	if offset > 0 {
		out += sep + "offset=" + itoa(offset)
	}
	return out
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }
