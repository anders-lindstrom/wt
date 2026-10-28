package keepstate

import (
	"testing"
	"time"
)

func TestFmtEvery(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Minute: "30m", time.Hour: "1h", 90 * time.Minute: "1h30m", 45 * time.Second: "45s", 2 * time.Hour: "2h", 70 * time.Second: "1m10s", 80 * time.Second: "1m20s", 90 * time.Second: "1m30s", time.Hour + 5*time.Second: "1h0m5s"} {
		if got := FmtEvery(d); got != want {
			t.Errorf("fmtEvery(%s) = %q, want %q", d, got, want)
		}
	}
}
