package runner

import "testing"

func TestProviderErrorDetail(t *testing.T) {
	s := `{"name":"UnknownError","data":{"message":"{\"message\":\"Upstream model stream stalled: no data received for 300000ms\",\"type\":\"timeout_error\",\"code\":\"stream_inactivity_timeout\"}"}}`
	if got := ErrorDetail(s); got != "Upstream model stream stalled: no data received for 300000ms" {
		t.Fatal(got)
	}
}
