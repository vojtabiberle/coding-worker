package runner

import (
	"encoding/json"
	"strings"
)

// ErrorDetail unwraps provider JSON messages without exposing whole event objects.
func ErrorDetail(s string) string {
	for n := 0; n < 6; n++ {
		var v struct {
			Message string `json:"message"`
			Data    struct {
				Message string `json:"message"`
			} `json:"data"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(s), &v) != nil {
			break
		}
		next := v.Message
		if next == "" {
			next = v.Data.Message
		}
		if next == "" && len(v.Error) > 0 {
			next = string(v.Error)
		}
		if next == "" || next == s {
			break
		}
		s = next
	}
	s = strings.Join(strings.Fields(s), " ")
	return s
}
