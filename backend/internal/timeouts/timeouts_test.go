package timeouts

import (
	"testing"
	"time"
)

// Each wait must outlast the one it contains, or an outer layer gives up
// while an inner one is still about to answer.
func TestTimeoutsNest(t *testing.T) {
	if !(Firmware < Ack && Ack < Print && Print < Write) {
		t.Errorf("firmware %v, ack %v, print %v, write %v do not nest", Firmware, Ack, Print, Write)
	}
}

// Cloudflare ends a request at 100 seconds, so a print that took longer would
// come back to the browser as Cloudflare's error page, not ours.
func TestTimeoutsFitCloudflare(t *testing.T) {
	if Write >= 100*time.Second {
		t.Errorf("write timeout %v is past Cloudflare's 100 s", Write)
	}
}
